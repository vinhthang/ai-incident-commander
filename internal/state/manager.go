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

var (
	ActiveIncidents = make(map[string]*IncidentState)
	StateMu         sync.RWMutex
)

func AcquireIncidentLock(alertName, status string) bool {
	StateMu.Lock()
	defer StateMu.Unlock()

	state, exists := ActiveIncidents[alertName]
	if !exists {
		if status == "resolved" {
			return false
		}
		ActiveIncidents[alertName] = &IncidentState{
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
		log.Printf("Alert %s is currently %s. Dropping concurrent webhook.", alertName, state.Status)
		return false
	}

	if time.Since(state.LastAttempt) < config.CooldownMins {
		log.Printf("Alert %s is within cooldown. Dropping webhook.", alertName)
		return false
	}

	state.Status = "TRIAGING"
	state.LastAttempt = time.Now()
	return true
}

func ReleaseIncidentLock(alertName string, newStatus string) {
	StateMu.Lock()
	defer StateMu.Unlock()
	if state, exists := ActiveIncidents[alertName]; exists {
		state.Status = newStatus
	}
}
