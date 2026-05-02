package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/auth"
)

// AuthController handles auth-related HTTP endpoints.
type AuthController struct {
	authSvc *auth.Service
}

// NewAuthController creates a new AuthController.
func NewAuthController(authSvc *auth.Service) *AuthController {
	return &AuthController{authSvc: authSvc}
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
	UserID string `json:"user_id"`
}

// Signup handles POST /auth/signup.
func (ac *AuthController) Signup(c *gin.Context) {
	var req signupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
		return
	}

	token, userID, err := ac.authSvc.Signup(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		switch err {
		case auth.ErrUsernameTaken:
			c.JSON(http.StatusConflict, gin.H{"error": "username already taken"})
		case auth.ErrInvalidUsername:
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case auth.ErrPasswordTooShort:
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}
		return
	}

	c.JSON(http.StatusCreated, authResponse{Token: token, UserID: userID})
}

// Login handles POST /auth/login.
func (ac *AuthController) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
		return
	}

	token, userID, err := ac.authSvc.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		// Same error for "not found" and "wrong password" to prevent enumeration
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	c.JSON(http.StatusOK, authResponse{Token: token, UserID: userID})
}

// Logout handles POST /auth/logout. Requires AuthMiddleware.
func (ac *AuthController) Logout(c *gin.Context) {
	userID := UserIDFromContext(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if err := ac.authSvc.Logout(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.Status(http.StatusNoContent)
}

// DeleteAccount handles DELETE /auth/account. Requires AuthMiddleware.
func (ac *AuthController) DeleteAccount(c *gin.Context) {
	userID := UserIDFromContext(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req deleteAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is required"})
		return
	}

	_, err := ac.authSvc.DeleteAccount(c.Request.Context(), userID, req.Password)
	if err != nil {
		if err == auth.ErrInvalidCredentials {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// TODO: enqueue GCS cleanup job for gcsPrefixes

	c.Status(http.StatusNoContent)
}

// UserIDFromContext extracts the user ID set by AuthMiddleware.
func UserIDFromContext(c *gin.Context) string {
	v, exists := c.Get("user_id")
	if !exists {
		return ""
	}
	s, _ := v.(string)
	return s
}
