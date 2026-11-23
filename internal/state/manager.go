package state

import (
	"context"
	"log"
	"sync"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

type IncidentState struct {
	IssueNumber  int
	AttemptCount int
	LastAttempt  time.Time
	Status       string // "SOAKING", "TRIAGING", "FIXING", "COOLING_DOWN"
}

const resolveDebounceWindow = 1 * time.Minute

var (
	ActiveIncidents  = make(map[string]*IncidentState)
	resolvedDebounce = make(map[string]time.Time)
	soakWaiters      = make(map[string]chan struct{})
	StateMu          sync.RWMutex
)

// HasActiveIncident reports whether the incident fingerprint currently has active in-memory state.
func HasActiveIncident(incidentKey string) bool {
	StateMu.RLock()
	defer StateMu.RUnlock()
	_, exists := ActiveIncidents[incidentKey]
	return exists
}

// AcquireResolveLock authorizes handling a resolved webhook even after pod restarts when
// ActiveIncidents is empty, while debouncing duplicate resolved webhooks within 1 minute
// and immediately cancelling any in-flight soak waiter for the same incidentKey.
func AcquireResolveLock(incidentKey string) bool {
	StateMu.Lock()
	defer StateMu.Unlock()

	now := time.Now()
	pruneExpiredLocked(now)
	notifyResolvedLocked(incidentKey)

	if lastResolved, ok := resolvedDebounce[incidentKey]; ok && now.Sub(lastResolved) < resolveDebounceWindow {
		log.Printf("Incident %s resolved webhook debounced (last handled %s ago).", incidentKey, now.Sub(lastResolved).Round(time.Second))
		return false
	}
	resolvedDebounce[incidentKey] = now

	if st, exists := ActiveIncidents[incidentKey]; exists {
		st.Status = "RESOLVING"
	}
	return true
}

func pruneExpiredLocked(now time.Time) {
	for fp, ts := range resolvedDebounce {
		if now.Sub(ts) >= resolveDebounceWindow {
			delete(resolvedDebounce, fp)
		}
	}
	for fp, st := range ActiveIncidents {
		if st == nil || (st.Status == "COOLING_DOWN" && now.Sub(st.LastAttempt) >= config.CooldownMins) {
			delete(ActiveIncidents, fp)
		}
	}
}

// NotifyResolved wakes and cancels any active soak wait for incidentKey.
func NotifyResolved(incidentKey string) {
	StateMu.Lock()
	defer StateMu.Unlock()
	notifyResolvedLocked(incidentKey)
}

func notifyResolvedLocked(incidentKey string) {
	if ch, ok := soakWaiters[incidentKey]; ok {
		close(ch)
		delete(soakWaiters, incidentKey)
	}
}

// WaitForSoak waits up to duration to verify an alert does not quickly self-resolve.
// It returns true if a resolved event or context cancellation interrupted the soak early.
func WaitForSoak(ctx context.Context, incidentKey string, duration time.Duration) bool {
	if duration <= 0 {
		return false
	}

	ch := make(chan struct{})
	StateMu.Lock()
	if st, exists := ActiveIncidents[incidentKey]; exists {
		if st.Status == "RESOLVING" {
			StateMu.Unlock()
			return true
		}
		st.Status = "SOAKING"
	}
	if prev, ok := soakWaiters[incidentKey]; ok {
		close(prev)
	}
	soakWaiters[incidentKey] = ch
	StateMu.Unlock()

	defer func() {
		StateMu.Lock()
		if cur, ok := soakWaiters[incidentKey]; ok && cur == ch {
			delete(soakWaiters, incidentKey)
		}
		if st, exists := ActiveIncidents[incidentKey]; exists && st.Status == "SOAKING" {
			st.Status = "TRIAGING"
		}
		StateMu.Unlock()
	}()

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}

// ClearResolveDebounceForTest clears the resolve debounce cache in unit tests.
func ClearResolveDebounceForTest() {
	StateMu.Lock()
	defer StateMu.Unlock()
	resolvedDebounce = make(map[string]time.Time)
	for k, ch := range soakWaiters {
		close(ch)
		delete(soakWaiters, k)
	}
}

func AcquireIncidentLock(incidentKey, status string) bool {
	StateMu.Lock()
	defer StateMu.Unlock()

	now := time.Now()
	pruneExpiredLocked(now)

	state, exists := ActiveIncidents[incidentKey]
	if !exists {
		if status == "resolved" {
			return false
		}
		ActiveIncidents[incidentKey] = &IncidentState{
			AttemptCount: 0,
			LastAttempt:  now,
			Status:       "TRIAGING",
		}
		return true
	}

	if status == "resolved" {
		state.Status = "RESOLVING"
		return true
	}

	if state.Status == "SOAKING" || state.Status == "TRIAGING" || state.Status == "FIXING" {
		log.Printf("Incident %s is currently %s. Dropping concurrent webhook.", incidentKey, state.Status)
		return false
	}

	if time.Since(state.LastAttempt) < config.CooldownMins {
		log.Printf("Incident %s is within cooldown. Dropping webhook.", incidentKey)
		return false
	}

	state.Status = "TRIAGING"
	state.LastAttempt = time.Now()
	return true
}

func ReleaseIncidentLock(incidentKey string, newStatus string) {
	StateMu.Lock()
	defer StateMu.Unlock()
	if state, exists := ActiveIncidents[incidentKey]; exists {
		state.Status = newStatus
	}
}
