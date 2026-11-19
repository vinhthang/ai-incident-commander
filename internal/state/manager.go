package state

import (
	"log"
	"sync"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

type IncidentState struct {
	IssueNumber  int
	AttemptCount int
	LastAttempt  time.Time
	Status       string // "TRIAGING", "FIXING", "COOLING_DOWN"
}

const resolveDebounceWindow = 1 * time.Minute

var (
	ActiveIncidents  = make(map[string]*IncidentState)
	resolvedDebounce = make(map[string]time.Time)
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
// ActiveIncidents is empty, while debouncing duplicate resolved webhooks within 1 minute.
func AcquireResolveLock(incidentKey string) bool {
	StateMu.Lock()
	defer StateMu.Unlock()

	if lastResolved, ok := resolvedDebounce[incidentKey]; ok && time.Since(lastResolved) < resolveDebounceWindow {
		log.Printf("Incident %s resolved webhook debounced (last handled %s ago).", incidentKey, time.Since(lastResolved).Round(time.Second))
		return false
	}
	resolvedDebounce[incidentKey] = time.Now()

	if st, exists := ActiveIncidents[incidentKey]; exists {
		st.Status = "RESOLVING"
	}
	return true
}

// ClearResolveDebounceForTest clears the resolve debounce cache in unit tests.
func ClearResolveDebounceForTest() {
	StateMu.Lock()
	defer StateMu.Unlock()
	resolvedDebounce = make(map[string]time.Time)
}

func AcquireIncidentLock(incidentKey, status string) bool {
	StateMu.Lock()
	defer StateMu.Unlock()

	state, exists := ActiveIncidents[incidentKey]
	if !exists {
		if status == "resolved" {
			return false
		}
		ActiveIncidents[incidentKey] = &IncidentState{
			AttemptCount: 0,
			LastAttempt:  time.Now(),
			Status:       "TRIAGING",
		}
		return true
	}

	if status == "resolved" {
		state.Status = "RESOLVING"
		return true
	}

	if state.Status == "TRIAGING" || state.Status == "FIXING" {
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
