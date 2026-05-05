package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenTTL is the lifetime of issued JWT tokens (30 days).
const TokenTTL = 30 * 24 * time.Hour

// Claims are the custom JWT claims used by the auth system.
type Claims struct {
	UserID       uint   `json:"uid"`
	TokenVersion int    `json:"ver"`
	jwt.RegisteredClaims
}

// IssueToken creates a signed HS256 JWT for the given user.
func IssueToken(secret []byte, userID uint, username string, tokenVersion int) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:       userID,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenTTL)),
			Issuer:    "wod-strategist",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// ParseToken validates and parses a raw JWT string, returning the custom claims.
func ParseToken(secret []byte, raw string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(raw, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}
