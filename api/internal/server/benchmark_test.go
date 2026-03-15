package server

import (
	"fmt"
	"testing"
)

func BenchmarkValidateMovements(b *testing.B) {
	// Create test cases
	validReq := []string{"Burpee", "Row", "Box Jump"}
	invalidReq := []string{"Invalid1", "Invalid2", "Invalid3"}
	mixedReq := []string{"Invalid1", "Burpee", "Invalid2"}

	// Worst case: No valid movements, check all 100 movements
	// If N is 100, M is 30, then 3000 checks.
	worstCaseReq := make([]string, 100)
	for i := 0; i < 100; i++ {
		worstCaseReq[i] = fmt.Sprintf("Invalid%d", i)
	}

	// Valid but late match: 50th element is valid
	// Checks 50 * 30 = 1500 times.
	lateMatchReq := make([]string, 100)
	for i := 0; i < 100; i++ {
		if i == 50 {
			lateMatchReq[i] = "Burpee"
		} else {
			lateMatchReq[i] = fmt.Sprintf("Invalid%d", i)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validateMovements(validReq)
		validateMovements(invalidReq)
		validateMovements(mixedReq)
		validateMovements(worstCaseReq)
		validateMovements(lateMatchReq)
	}
}
