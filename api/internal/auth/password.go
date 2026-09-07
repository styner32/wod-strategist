package auth

import "golang.org/x/crypto/bcrypt"

var dummyHash []byte

func init() {
	var err error
	dummyHash, err = bcrypt.GenerateFromPassword([]byte("dummy"), bcrypt.DefaultCost)
	if err != nil {
		panic(err) // Secure initialization requirement
	}
}

// DummyVerifyPassword compares a plaintext password against a precomputed dummy hash
// to burn CPU time and prevent user enumeration timing attacks when a user is not found.
func DummyVerifyPassword(plain string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(plain))
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
