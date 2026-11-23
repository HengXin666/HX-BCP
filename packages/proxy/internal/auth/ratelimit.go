package auth

import (
	"sync"
	"time"
)

// loginLimiter tracks consecutive login failures per client key and locks
// the key out after maxFailures for lockout duration. State is in-memory:
// a restart clears it, which is acceptable for a single-admin control plane
// because the account also stays protected by Argon2id cost.
type loginLimiter struct {
	mutex       sync.Mutex
	failures    map[string]*failureState
	maxFailures int
	lockout     time.Duration
}

type failureState struct {
	count       int
	lockedUntil time.Time
	lastFailure time.Time
}

func newLoginLimiter(maxFailures int, lockout time.Duration) *loginLimiter {
	return &loginLimiter{
		failures:    make(map[string]*failureState),
		maxFailures: maxFailures,
		lockout:     lockout,
	}
}

func (l *loginLimiter) Allow(key string, now time.Time) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.sweep(now)
	state, exists := l.failures[key]
	if !exists {
		return true
	}
	return now.After(state.lockedUntil)
}

func (l *loginLimiter) RecordFailure(key string, now time.Time) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	state, exists := l.failures[key]
	if !exists {
		state = &failureState{}
		l.failures[key] = state
	}
	state.count++
	state.lastFailure = now
	if state.count >= l.maxFailures {
		state.lockedUntil = now.Add(l.lockout)
	}
}

func (l *loginLimiter) Reset(key string) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	delete(l.failures, key)
}

// sweep drops stale entries so the map stays bounded even under scanning.
func (l *loginLimiter) sweep(now time.Time) {
	if len(l.failures) < 1024 {
		return
	}
	for key, state := range l.failures {
		if now.Sub(state.lastFailure) > l.lockout*2 {
			delete(l.failures, key)
		}
	}
}
