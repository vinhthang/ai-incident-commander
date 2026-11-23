package state

import (
	"context"
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

func TestWaitForSoak_CancelsEarlyOnResolve(t *testing.T) {
	resetState()
	ClearResolveDebounceForTest()

	fp := "soak-early-cancel"
	if !AcquireIncidentLock(fp, "firing") {
		t.Fatalf("expected firing lock to be acquired")
	}

	done := make(chan bool, 1)
	start := time.Now()
	go func() {
		done <- WaitForSoak(context.Background(), fp, 5*time.Second)
	}()

	time.Sleep(25 * time.Millisecond)
	if !AcquireResolveLock(fp) {
		t.Fatalf("expected resolve lock to be acquired during soak")
	}

	resolvedEarly := <-done
	elapsed := time.Since(start)

	if !resolvedEarly {
		t.Fatalf("expected WaitForSoak to return resolvedEarly=true when resolved arrived")
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("expected WaitForSoak to cancel immediately (<500ms), took %v", elapsed)
	}
}

func TestWaitForSoak_CompletesFullWindowWhenNotResolved(t *testing.T) {
	resetState()
	ClearResolveDebounceForTest()

	fp := "soak-full-window"
	if !AcquireIncidentLock(fp, "firing") {
		t.Fatalf("expected firing lock to be acquired")
	}

	done := make(chan bool, 1)
	start := time.Now()
	go func() {
		done <- WaitForSoak(context.Background(), fp, 45*time.Millisecond)
	}()

	time.Sleep(15 * time.Millisecond)
	if AcquireIncidentLock(fp, "firing") {
		t.Fatalf("expected duplicate firing webhook to be dropped while Status == SOAKING")
	}

	resolvedEarly := <-done
	elapsed := time.Since(start)

	if resolvedEarly {
		t.Fatalf("expected WaitForSoak to return false when not resolved")
	}
	if elapsed < 35*time.Millisecond {
		t.Fatalf("expected WaitForSoak to sleep >= 35ms, elapsed %v", elapsed)
	}
}

func TestPruneExpiredCooldownAndResolveDebounce(t *testing.T) {
	resetState()
	ClearResolveDebounceForTest()

	now := time.Now()
	StateMu.Lock()
	ActiveIncidents["stale-cooldown"] = &IncidentState{
		Status:      "COOLING_DOWN",
		LastAttempt: now.Add(-10 * time.Minute),
	}
	ActiveIncidents["fresh-cooldown"] = &IncidentState{
		Status:      "COOLING_DOWN",
		LastAttempt: now.Add(-1 * time.Minute),
	}
	ActiveIncidents["active-fixing"] = &IncidentState{
		Status:      "FIXING",
		LastAttempt: now.Add(-10 * time.Minute),
	}
	resolvedDebounce["stale-resolved"] = now.Add(-5 * time.Minute)
	resolvedDebounce["fresh-resolved"] = now.Add(-10 * time.Second)
	StateMu.Unlock()

	if !AcquireIncidentLock("fresh-fp", "firing") {
		t.Fatalf("expected AcquireIncidentLock to succeed for fresh-fp")
	}

	StateMu.RLock()
	if _, ok := ActiveIncidents["stale-cooldown"]; ok {
		t.Errorf("expected expired COOLING_DOWN entry to be pruned")
	}
	if _, ok := ActiveIncidents["fresh-cooldown"]; !ok {
		t.Errorf("expected fresh COOLING_DOWN entry to be preserved")
	}
	if _, ok := ActiveIncidents["active-fixing"]; !ok {
		t.Errorf("expected active FIXING entry to be preserved")
	}
	if _, ok := ActiveIncidents["fresh-fp"]; !ok {
		t.Errorf("expected newly acquired fresh-fp entry to exist")
	}
	if _, ok := resolvedDebounce["stale-resolved"]; ok {
		t.Errorf("expected expired resolvedDebounce entry to be pruned")
	}
	if _, ok := resolvedDebounce["fresh-resolved"]; !ok {
		t.Errorf("expected fresh resolvedDebounce entry to be preserved")
	}
	StateMu.RUnlock()

	// Verify WaitForSoak edge cases: non-positive duration and cancelled context
	if WaitForSoak(context.Background(), "fresh-fp", 0) {
		t.Errorf("expected WaitForSoak with 0 duration to return false")
	}
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if !WaitForSoak(cancelledCtx, "fresh-fp", 5*time.Second) {
		t.Errorf("expected WaitForSoak with cancelled context to return true")
	}
	if !HasActiveIncident("fresh-fp") {
		t.Errorf("expected HasActiveIncident(fresh-fp) == true")
	}
	if HasActiveIncident("missing-fp") {
		t.Errorf("expected HasActiveIncident(missing-fp) == false")
	}
}
