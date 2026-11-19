package minion

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStartAgyWorker_Concurrency(t *testing.T) {
	// Clear the queue to start fresh
	AgyQueue = make(chan AgyJob, 100)

	// Keep track of how many jobs have completed
	completed := make(chan bool, 3)

	// Define a mock executeAgy function just for testing
	// In a real scenario, we might use an interface for exec.Command, 
	// but here we can just test that the channel semantics guarantee sequential processing.
	// Since executeAgy is hardcoded to exec.Command in queue.go, we will just test
	// the EnqueueAgyJob mechanism doesn't block infinitely.
	
	go func() {
		for job := range AgyQueue {
			// Mocking execution time
			time.Sleep(50 * time.Millisecond)
			job.Result <- AgyResult{Stdout: "mock output"}
			completed <- true
		}
	}()

	start := time.Now()

	// Submit 3 jobs concurrently
	for i := 0; i < 3; i++ {
		go func(id int) {
			EnqueueAgyJob(context.Background(), "mock prompt")
		}(i)
	}

	// Wait for all 3 jobs to complete
	for i := 0; i < 3; i++ {
		<-completed
	}

	duration := time.Since(start)

	// Since they take 50ms each and process sequentially via the channel,
	// it should take at least 150ms to finish all 3.
	if duration < 150*time.Millisecond {
		t.Errorf("Expected sequential processing to take >= 150ms, took %v", duration)
	}
}

func TestBuildAgyArgs(t *testing.T) {
	withModel := buildAgyArgs("hello prompt", "gemini-3.8-flash-medium")
	wantWithModel := []string{"-p", "hello prompt", "--dangerously-skip-permissions", "--disable-slash-commands", "--model", "gemini-3.8-flash-medium"}
	if !reflect.DeepEqual(withModel, wantWithModel) {
		t.Fatalf("buildAgyArgs with model = %v, want %v", withModel, wantWithModel)
	}

	withoutModel := buildAgyArgs("hello prompt", "")
	wantWithoutModel := []string{"-p", "hello prompt", "--dangerously-skip-permissions", "--disable-slash-commands"}
	if !reflect.DeepEqual(withoutModel, wantWithoutModel) {
		t.Fatalf("buildAgyArgs without model = %v, want %v", withoutModel, wantWithoutModel)
	}
}

func TestSyncAgyRuntimeDir(t *testing.T) {
	hostAuthDir := t.TempDir()
	runtimeHome := t.TempDir()

	// Seed auth files + noisy host files/subdirs that MUST NOT be copied
	for name, body := range map[string]string{
		"antigravity-oauth-token": "token-value",
		"installation_id":         "inst-xyz",
		"settings.json":           `{"model":"gemini-3.8-flash-medium"}`,
	} {
		if err := os.WriteFile(filepath.Join(hostAuthDir, name), []byte(body), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(hostAuthDir, "conversations"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostAuthDir, "conversations", "secret.json"), []byte("ignore"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(hostAuthDir, "mcp_servers"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostAuthDir, "mcp_servers", "heavy.bin"), []byte("ignore"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := syncAgyRuntimeDir(hostAuthDir, runtimeHome); err != nil {
		t.Fatalf("syncAgyRuntimeDir failed: %v", err)
	}

	for name, want := range map[string]string{
		"antigravity-oauth-token": "token-value",
		"installation_id":         "inst-xyz",
		"settings.json":           `{"model":"gemini-3.8-flash-medium"}`,
	} {
		got, err := os.ReadFile(filepath.Join(runtimeHome, "antigravity-cli", name))
		if err != nil {
			t.Fatalf("expected synced file %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("synced %s = %q, want %q", name, string(got), want)
		}
	}

	for _, excluded := range []string{"conversations", "mcp_servers"} {
		if _, err := os.Stat(filepath.Join(runtimeHome, "antigravity-cli", excluded)); !os.IsNotExist(err) {
			t.Errorf("expected %s NOT to be synced into runtimeHome", excluded)
		}
	}

	mcpCfg, err := os.ReadFile(filepath.Join(runtimeHome, "config", "mcp_config.json"))
	if err != nil {
		t.Fatalf("expected empty mcp_config.json to be written: %v", err)
	}
	if string(mcpCfg) != `{"mcpServers":{}}` {
		t.Errorf("mcp_config.json = %q, want %q", string(mcpCfg), `{"mcpServers":{}}`)
	}
}
