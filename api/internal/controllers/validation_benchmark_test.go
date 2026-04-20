package controllers

import (
	"fmt"
	"testing"
)

func BenchmarkValidateMovements(b *testing.B) {
	validReq := []string{"Burpee", "Row", "Box Jump"}
	emptyNameReq := []string{"Burpee", "", "Row"}

	worstCaseReq := make([]string, 99)
	for i := 0; i < 99; i++ {
		worstCaseReq[i] = fmt.Sprintf("Custom Movement %d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validateMovements(validReq)
		validateMovements(emptyNameReq)
		validateMovements(worstCaseReq)
	}
}
