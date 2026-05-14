package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/auth"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CookieConfig holds settings for the web auth cookie.
type CookieConfig struct {
	Domain string // e.g. ".wod-strategist.com"; empty = origin-only
	Secure bool   // true in prod, false for local HTTP dev
	MaxAge int    // seconds; should match auth.TokenTTL
}

// AuthController handles auth-related HTTP endpoints.
type AuthController struct {
	authSvc   *auth.Service
	database  *gorm.DB
	cookieCfg CookieConfig
}

// NewAuthController creates a new AuthController.
func NewAuthController(authSvc *auth.Service, database *gorm.DB, cookieCfg CookieConfig) *AuthController {
	return &AuthController{authSvc: authSvc, database: database, cookieCfg: cookieCfg}
}

type signupRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type deleteAccountRequest struct {
	Password string `json:"password" binding:"required"`
}

type authResponse struct {
	Token  string `json:"token"`
	UserID uint   `json:"user_id"`
}

type webAuthResponse struct {
	UserID uint `json:"user_id"`
}

type meResponse struct {
	UserID   uint              `json:"user_id"`
	Username string            `json:"username"`
	Profiles []ProfileResponse `json:"profiles"`
}

// Signup handles POST /auth/signup (mobile — returns token in body).
// NOTE: Signup is intentionally disabled for the friends-only preview.
// Users are created manually via the create-user CLI.
func (ac *AuthController) Signup(c *gin.Context) {
	logger.Log.Warn("Signup: rejected (signup disabled)")
	c.JSON(http.StatusForbidden, gin.H{"error": "signup is disabled"})
}

// Login handles POST /auth/login (mobile — returns token in body).
func (ac *AuthController) Login(c *gin.Context) {
	logger.Log.Debug("Login request received")
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Debug("Login: failed to bind JSON", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
		return
	}

	logger.Log.Debug("Login: calling authSvc.Login", zap.String("username", req.Username))
	token, userID, err := ac.authSvc.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		// Same error for "not found" and "wrong password" to prevent enumeration
		logger.Log.Debug("Login: invalid credentials", zap.String("username", req.Username), zap.Error(err))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	logger.Log.Info("Login: success", zap.Uint("user_id", userID), zap.String("username", req.Username))
	c.JSON(http.StatusOK, authResponse{Token: token, UserID: userID})
}

// Logout handles POST /auth/logout. Requires AuthMiddleware.
func (ac *AuthController) Logout(c *gin.Context) {
	userID := UserIDFromContext(c)
	logger.Log.Debug("Logout request received", zap.Uint("user_id", userID))
	if err := ac.authSvc.Logout(c.Request.Context(), userID); err != nil {
		logger.Log.Error("Logout: failed", zap.Uint("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	logger.Log.Debug("Logout: success", zap.Uint("user_id", userID))
	c.Status(http.StatusNoContent)
}

// DeleteAccount handles DELETE /auth/account. Requires AuthMiddleware.
func (ac *AuthController) DeleteAccount(c *gin.Context) {
	userID := UserIDFromContext(c)
	logger.Log.Debug("DeleteAccount request received", zap.Uint("user_id", userID))
	var req deleteAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Debug("DeleteAccount: failed to bind JSON", zap.Uint("user_id", userID), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is required"})
		return
	}

	_, err := ac.authSvc.DeleteAccount(c.Request.Context(), userID, req.Password)
	if err != nil {
		if err == auth.ErrInvalidCredentials {
			logger.Log.Debug("DeleteAccount: invalid credentials", zap.Uint("user_id", userID))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			return
		}
		logger.Log.Error("DeleteAccount: unexpected error", zap.Uint("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// TODO: enqueue GCS cleanup job for gcsPrefixes

	logger.Log.Info("DeleteAccount: success", zap.Uint("user_id", userID))
	c.Status(http.StatusNoContent)
}

// WebSignup handles POST /auth/web/signup.
// NOTE: Signup is intentionally disabled for the friends-only preview.
// Users are created manually via the create-user CLI.
func (ac *AuthController) WebSignup(c *gin.Context) {
	logger.Log.Warn("WebSignup: rejected (signup disabled)")
	c.JSON(http.StatusForbidden, gin.H{"error": "signup is disabled"})
}

// WebLogin handles POST /auth/web/login — sets httpOnly cookie, returns { user_id } only.
func (ac *AuthController) WebLogin(c *gin.Context) {
	logger.Log.Debug("WebLogin request received")
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Log.Debug("WebLogin: failed to bind JSON", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
		return
	}

	logger.Log.Debug("WebLogin: calling authSvc.Login", zap.String("username", req.Username))
	token, userID, err := ac.authSvc.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		logger.Log.Debug("WebLogin: invalid credentials", zap.String("username", req.Username), zap.Error(err))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	logger.Log.Info("WebLogin: success", zap.Uint("user_id", userID), zap.String("username", req.Username))
	ac.setAuthCookie(c, token)
	c.JSON(http.StatusOK, webAuthResponse{UserID: userID})
}

// WebLogout handles POST /auth/web/logout — clears cookie + bumps token_version.
func (ac *AuthController) WebLogout(c *gin.Context) {
	userID := UserIDFromContext(c)
	logger.Log.Debug("WebLogout request received", zap.Uint("user_id", userID))
	if err := ac.authSvc.Logout(c.Request.Context(), userID); err != nil {
		logger.Log.Error("WebLogout: failed", zap.Uint("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	ac.clearAuthCookie(c)
	logger.Log.Debug("WebLogout: success", zap.Uint("user_id", userID))
	c.Status(http.StatusNoContent)
}

// GetMe handles GET /auth/me — returns current user + profiles for SPA bootstrap.
func (ac *AuthController) GetMe(c *gin.Context) {
	userID := UserIDFromContext(c)
	logger.Log.Debug("GetMe request received", zap.Uint("user_id", userID))

	var user db.User
	if err := ac.database.WithContext(c.Request.Context()).
		Where("id = ? AND deleted_at IS NULL", userID).
		First(&user).Error; err != nil {
		logger.Log.Debug("GetMe: user not found or deleted", zap.Uint("user_id", userID), zap.Error(err))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var profiles []db.Profile
	if err := ac.database.WithContext(c.Request.Context()).
		Where("user_id = ? AND archived_at IS NULL", userID).
		Order("created_at asc").
		Find(&profiles).Error; err != nil {
		logger.Log.Error("GetMe: failed to fetch profiles", zap.Uint("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	logger.Log.Debug("GetMe: success", zap.Uint("user_id", userID), zap.Int("profile_count", len(profiles)))

	profileResponses := make([]ProfileResponse, len(profiles))
	for i, p := range profiles {
		profileResponses[i] = ProfileResponse{
			ID:           p.ID,
			Name:         p.Name,
			FitnessLevel: p.FitnessLevel,
		}
	}

	c.JSON(http.StatusOK, meResponse{
		UserID:   user.ID,
		Username: user.Username,
		Profiles: profileResponses,
	})
}

// setAuthCookie writes the JWT as an httpOnly cookie.
func (ac *AuthController) setAuthCookie(c *gin.Context, token string) {
	logger.Log.Debug("setAuthCookie: setting jwt cookie",
		zap.String("domain", ac.cookieCfg.Domain),
		zap.Bool("secure", ac.cookieCfg.Secure),
		zap.Int("max_age", ac.cookieCfg.MaxAge),
	)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "jwt",
		Value:    token,
		Path:     "/",
		Domain:   ac.cookieCfg.Domain,
		MaxAge:   ac.cookieCfg.MaxAge,
		Secure:   ac.cookieCfg.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearAuthCookie removes the auth cookie.
func (ac *AuthController) clearAuthCookie(c *gin.Context) {
	logger.Log.Debug("clearAuthCookie: clearing jwt cookie",
		zap.String("domain", ac.cookieCfg.Domain),
		zap.Bool("secure", ac.cookieCfg.Secure),
	)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "jwt",
		Value:    "",
		Path:     "/",
		Domain:   ac.cookieCfg.Domain,
		MaxAge:   -1,
		Secure:   ac.cookieCfg.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// UserIDFromContext extracts the user ID set by AuthMiddleware.
func UserIDFromContext(c *gin.Context) uint {
	v, exists := c.Get("user_id")
	if !exists {
		return 0
	}
	u, _ := v.(uint)
	return u
}
