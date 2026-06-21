package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
)

var (
	// ErrInvalidCredentials is a generic auth failure (login or signup).
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrUsernameTaken is returned when signup attempts to use an existing username.
	ErrUsernameTaken = errors.New("username already taken")
	// ErrInvalidUsername is returned when a username doesn't match the format rules.
	ErrInvalidUsername = errors.New("invalid username: must be 3-20 lowercase alphanumeric or underscore characters")
	// ErrPasswordTooShort is returned when a password is shorter than 8 characters.
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	// ErrAccountDeleted is returned when a deleted account attempts to authenticate.
	ErrAccountDeleted = errors.New("account has been deleted")
)

var usernameRegex = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

// cachedUser holds a validated user lookup with a TTL.
type cachedUser struct {
	tokenVersion int
	fetchedAt    time.Time
}

const userCacheTTL = 30 * time.Second

// Service is the core auth service that handles signup, login, token validation,
// logout, and account deletion.
type Service struct {
	db        *gorm.DB
	jwtSecret []byte
	cache     sync.Map // map[uint]cachedUser
}

// NewService creates a new auth Service.
func NewService(database *gorm.DB, jwtSecret []byte) *Service {
	return &Service{
		db:        database,
		jwtSecret: jwtSecret,
	}
}

// Signup creates a new user account, auto-creates a default profile, and returns a JWT.
func (s *Service) Signup(ctx context.Context, username, password string) (token string, userID uint, err error) {
	if !usernameRegex.MatchString(username) {
		return "", 0, ErrInvalidUsername
	}
	if len(password) < 8 {
		return "", 0, ErrPasswordTooShort
	}

	hash, err := HashPassword(password)
	if err != nil {
		return "", 0, fmt.Errorf("hash password: %w", err)
	}

	user := db.User{
		Username:     username,
		PasswordHash: hash,
		TokenVersion: 1,
	}

	result := s.db.WithContext(ctx).Create(&user)
	if result.Error != nil {
		if isUniqueViolation(result.Error) {
			return "", 0, ErrUsernameTaken
		}
		return "", 0, fmt.Errorf("create user: %w", result.Error)
	}

	// Auto-create a default profile for the user
	defaultProfile := db.Profile{
		UserID:       user.ID,
		Name:         username,
		FitnessLevel: "intermediate",
	}
	if err := s.db.WithContext(ctx).Create(&defaultProfile).Error; err != nil {
		return "", 0, fmt.Errorf("create default profile: %w", err)
	}

	token, err = s.IssueTokenByUser(&user)
	if err != nil {
		return "", 0, err
	}

	return token, user.ID, nil
}

// Issue token for a user
func (s *Service) IssueTokenByUser(u *db.User) (token string, err error) {
	return IssueToken(s.jwtSecret, u.ID, u.Username, u.TokenVersion)
}

// Login authenticates a user and returns a JWT.
func (s *Service) Login(ctx context.Context, username, password string) (token string, userID uint, err error) {
	var user db.User
	if err := s.db.WithContext(ctx).
		Where("LOWER(username) = LOWER(?) AND deleted_at IS NULL", username).
		First(&user).Error; err != nil {
		return "", 0, ErrInvalidCredentials
	}

	if !VerifyPassword(password, user.PasswordHash) {
		return "", 0, ErrInvalidCredentials
	}

	token, err = s.IssueTokenByUser(&user)
	if err != nil {
		return "", 0, err
	}

	return token, user.ID, nil
}

// ValidateToken parses a raw JWT and verifies the user is still active with a matching token version.
// Uses an in-memory cache with a 30s TTL to avoid DB queries on every request.
func (s *Service) ValidateToken(ctx context.Context, rawToken string) (uint, error) {
	claims, err := ParseToken(s.jwtSecret, rawToken)
	if err != nil {
		return 0, err
	}

	// Check cache first
	if cached, ok := s.cache.Load(claims.UserID); ok {
		cu := cached.(cachedUser)
		if time.Since(cu.fetchedAt) < userCacheTTL && cu.tokenVersion == claims.TokenVersion {
			return claims.UserID, nil
		}
	}

	// Cache miss or stale — check DB
	var user db.User
	if err := s.db.WithContext(ctx).
		Where("id = ? AND deleted_at IS NULL", claims.UserID).
		First(&user).Error; err != nil {
		return 0, ErrAccountDeleted
	}

	if user.TokenVersion != claims.TokenVersion {
		return 0, ErrInvalidCredentials
	}

	// Update cache
	s.cache.Store(claims.UserID, cachedUser{
		tokenVersion: user.TokenVersion,
		fetchedAt:    time.Now(),
	})

	return claims.UserID, nil
}

// Logout bumps the token version for the user, invalidating all existing tokens.
func (s *Service) Logout(ctx context.Context, userID uint) error {
	result := s.db.WithContext(ctx).
		Model(&db.User{}).
		Where("id = ?", userID).
		Update("token_version", gorm.Expr("token_version + 1"))
	if result.Error != nil {
		return fmt.Errorf("bump token version: %w", result.Error)
	}

	// Invalidate cache
	s.cache.Delete(userID)
	return nil
}

// DeleteAccount soft-deletes the user and all their data.
// Returns the list of GCS prefixes that should be cleaned up asynchronously.
func (s *Service) DeleteAccount(ctx context.Context, userID uint, password string) (gcsPrefixes []string, err error) {
	var user db.User
	if err := s.db.WithContext(ctx).
		Where("id = ? AND deleted_at IS NULL", userID).
		First(&user).Error; err != nil {
		return nil, ErrInvalidCredentials
	}

	if !VerifyPassword(password, user.PasswordHash) {
		return nil, ErrInvalidCredentials
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Collect profile IDs
		var profileIDs []uint
		if err := tx.Model(&db.Profile{}).
			Where("user_id = ?", userID).
			Pluck("id", &profileIDs).Error; err != nil {
			return fmt.Errorf("collect profile ids: %w", err)
		}

		if len(profileIDs) > 0 {
			// 2. Delete leaf data (analysis results, chunks, highlights, token usage)
			for _, model := range []any{
				&db.AnalysisResult{},
				&db.ChunkAnalysisResult{},
				&db.HighlightResult{},
				&db.TokenUsage{},
			} {
				if err := tx.Where("profile_id IN ?", profileIDs).Delete(model).Error; err != nil {
					return fmt.Errorf("delete leaf data: %w", err)
				}
			}

			// 3. Collect GCS prefixes for async cleanup
			for _, pid := range profileIDs {
				gcsPrefixes = append(gcsPrefixes, fmt.Sprintf("videos/%d/", pid))
			}

			// 4. Delete profiles
			if err := tx.Where("user_id = ?", userID).Delete(&db.Profile{}).Error; err != nil {
				return fmt.Errorf("delete profiles: %w", err)
			}
		}

		// 5. Soft-delete user: scrub password, bump token version
		now := time.Now()
		updates := map[string]any{
			"deleted_at":    now,
			"password_hash": "DELETED",
			"token_version": gorm.Expr("token_version + 1"),
		}
		if err := tx.Model(&db.User{}).Where("id = ?", userID).Updates(updates).Error; err != nil {
			return fmt.Errorf("soft-delete user: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Invalidate cache
	s.cache.Delete(userID)

	return gcsPrefixes, nil
}

// isUniqueViolation checks if a GORM error is a Postgres unique constraint violation.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// Postgres error code 23505 = unique_violation
	return contains(err.Error(), "23505") || contains(err.Error(), "duplicate key")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
