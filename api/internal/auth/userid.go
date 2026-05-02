package auth

import (
	"fmt"
	"math/rand"

	petname "github.com/dustinkirkland/golang-petname"
)

// GenerateUserID creates a human-readable user ID using golang-petname
// with a 2-digit random suffix for collision resistance.
// Format: "adjective-noun-NN" (e.g. "happy-tiger-42")
func GenerateUserID() string {
	name := petname.Generate(2, "-")
	suffix := rand.Intn(100) //nolint:gosec // Not security-critical; just collision avoidance
	return fmt.Sprintf("%s-%02d", name, suffix)
}
