package auth

import "golang.org/x/crypto/bcrypt"

var dummyHash string

func init() {
	// Precompute a dummy hash to use for timing attack mitigation.
	var err error
	dummyHash, err = HashPassword("dummy")
	if err != nil {
		panic(err)
	}
}

// HashPassword creates a bcrypt hash of the plaintext password using the default cost.
func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword compares a plaintext password against a bcrypt hash.
// Returns true if they match.
func VerifyPassword(plain, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
