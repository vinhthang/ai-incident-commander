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

func TestConcurrentDistinctFingerprintsDoNotCollide(t *testing.T) {
	resetState()

	if !AcquireIncidentLock("fp-endpoint-a", "firing") {
		t.Errorf("Expected to acquire lock for fp-endpoint-a")
	}
	if !AcquireIncidentLock("fp-endpoint-b", "firing") {
		t.Errorf("Expected to acquire lock for concurrent distinct fingerprint fp-endpoint-b")
	}
	if AcquireIncidentLock("fp-endpoint-a", "firing") {
		t.Errorf("Expected duplicate lock acquisition for fp-endpoint-a to fail while active")
	}
}

func TestAcquireResolveLock_RestartSafeAndDebounced(t *testing.T) {
	resetState()
	ClearResolveDebounceForTest()

	// Ensure existing unresolved GhostAlert contract still holds
	if AcquireIncidentLock("GhostAlert", "resolved") {
		t.Fatalf("AcquireIncidentLock must still return false for unknown resolved key")
	}

	// Restart-safe resolve lock must succeed even when ActiveIncidents is empty
	if !AcquireResolveLock("RestartedPodAlert") {
		t.Fatalf("expected AcquireResolveLock to succeed for first resolved delivery after restart")
	}

	// Second delivery within 1-minute debounce must be suppressed
	if AcquireResolveLock("RestartedPodAlert") {
		t.Fatalf("expected AcquireResolveLock to debounce immediate duplicate resolved delivery")
	}

	// Simulate expired debounce window (> 1 minute ago)
	StateMu.Lock()
	resolvedDebounce["RestartedPodAlert"] = time.Now().Add(-2 * time.Minute)
	StateMu.Unlock()

	if !AcquireResolveLock("RestartedPodAlert") {
		t.Fatalf("expected AcquireResolveLock to succeed after debounce window expired")
	}
}
