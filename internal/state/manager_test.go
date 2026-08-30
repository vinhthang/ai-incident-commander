package state

import (
	"testing"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

func resetState() {
	StateMu.Lock()
	defer StateMu.Unlock()
	ActiveIncidents = make(map[string]*IncidentState)
	config.CooldownMins = 5 * time.Minute
}

func TestIncidentLockAcquisition(t *testing.T) {
	resetState()
	alertName := "TestAlert"

	if !AcquireIncidentLock(alertName, "firing") {
		t.Errorf("Expected to acquire lock on new firing alert")
	}

	if AcquireIncidentLock(alertName, "firing") {
		t.Errorf("Expected lock acquisition to fail for concurrent webhook stampede")
	}

	StateMu.Lock()
	state := ActiveIncidents[alertName]
	if state.Status != "TRIAGING" {
		t.Errorf("Expected status TRIAGING, got %s", state.Status)
	}
	StateMu.Unlock()

	ReleaseIncidentLock(alertName, "COOLING_DOWN")

	if AcquireIncidentLock(alertName, "firing") {
		t.Errorf("Expected lock acquisition to fail due to 5-minute cooldown")
	}

	StateMu.Lock()
	ActiveIncidents[alertName].LastAttempt = time.Now().Add(-10 * time.Minute)
	StateMu.Unlock()

	if !AcquireIncidentLock(alertName, "firing") {
		t.Errorf("Expected to acquire lock after cooldown expired")
	}
}

func TestResolveNonExistentIncident(t *testing.T) {
	resetState()
	if AcquireIncidentLock("GhostAlert", "resolved") {
		t.Errorf("Should not acquire lock or track resolved incidents that have no active state")
	}
}
