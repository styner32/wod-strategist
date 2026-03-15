package controllers

import (
	"fmt"
	"testing"
)

func BenchmarkAllowedMovementsContainsAll(b *testing.B) {
	validReq := []string{"Burpee", "Row", "Box Jump"}
	invalidReq := []string{"Invalid1", "Invalid2", "Invalid3"}
	mixedReq := []string{"Invalid1", "Burpee", "Invalid2"}

	worstCaseReq := make([]string, 100)
	for i := 0; i < 100; i++ {
		worstCaseReq[i] = fmt.Sprintf("Invalid%d", i)
	}

	lateMatchReq := make([]string, 100)
	for i := 0; i < 100; i++ {
		if i == 50 {
			lateMatchReq[i] = "Burpee"
			continue
		}
		lateMatchReq[i] = fmt.Sprintf("Invalid%d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		allowedMovements.containsAll(validReq)
		allowedMovements.containsAll(invalidReq)
		allowedMovements.containsAll(mixedReq)
		allowedMovements.containsAll(worstCaseReq)
		allowedMovements.containsAll(lateMatchReq)
	}
}
