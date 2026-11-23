package fleet

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

var lastSummaryMu sync.RWMutex
var lastSummary Summary

// SetLastSummary records the most recent sweep result for the status endpoint.
func (s *Service) SetLastSummary(summary Summary) {
	lastSummaryMu.Lock()
	defer lastSummaryMu.Unlock()
	lastSummary = summary
}

// LastSummary returns the most recent sweep result.
func (s *Service) LastSummary() Summary {
	lastSummaryMu.RLock()
	defer lastSummaryMu.RUnlock()
	return lastSummary
}

func randRead(buffer []byte) (int, error) {
	return rand.Read(buffer)
}

func hexEncode(raw []byte) string {
	return hex.EncodeToString(raw)
}
