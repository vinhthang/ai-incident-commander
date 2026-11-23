package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

func TestWorkspaceLock_SerializesConcurrentAccess(t *testing.T) {
	var currentHolders atomic.Int32
	var maxHolders atomic.Int32
	var wg sync.WaitGroup

	start := time.Now()
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Lock()
			defer Unlock()

			cur := currentHolders.Add(1)
			for {
				prev := maxHolders.Load()
				if cur <= prev || maxHolders.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
			currentHolders.Add(-1)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	if maxHolders.Load() != 1 {
		t.Fatalf("expected workspace.Lock() to limit concurrent holders to 1, got %d", maxHolders.Load())
	}
	if elapsed < 70*time.Millisecond {
		t.Fatalf("expected 3 serialized 25ms critical sections to take >= 70ms, took %v", elapsed)
	}
}

func runGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed in %s: %v (%s)", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func setupBareOriginAndClone(t *testing.T) (bareDir, cloneDir string) {
	t.Helper()
	base := t.TempDir()
	bareDir = filepath.Join(base, "origin.git")
	seedDir := filepath.Join(base, "seed")
	cloneDir = filepath.Join(base, "clone")

	if out, err := exec.Command("git", "init", "--bare", "-b", "main", bareDir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare failed: %v (%s)", err, string(out))
	}
	if out, err := exec.Command("git", "clone", bareDir, seedDir).CombinedOutput(); err != nil {
		t.Fatalf("git clone seed failed: %v (%s)", err, string(out))
	}
	runGitTest(t, seedDir, "checkout", "-B", "main")
	runGitTest(t, seedDir, "config", "user.email", "test@vinhthang.dev")
	runGitTest(t, seedDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(seedDir, "values.yaml"), []byte("replicas: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, seedDir, "add", "values.yaml")
	runGitTest(t, seedDir, "commit", "-m", "init main")
	runGitTest(t, seedDir, "push", "-u", "origin", "main")

	if out, err := exec.Command("git", "clone", bareDir, cloneDir).CombinedOutput(); err != nil {
		t.Fatalf("git clone workspace failed: %v (%s)", err, string(out))
	}
	runGitTest(t, cloneDir, "config", "user.email", "test@vinhthang.dev")
	runGitTest(t, cloneDir, "config", "user.name", "Test")
	return bareDir, cloneDir
}

func TestResetToMain_CleansDirtyTrackedAndUntrackedFiles(t *testing.T) {
	origWS := config.WorkspaceDir
	defer func() { config.WorkspaceDir = origWS }()

	_, cloneDir := setupBareOriginAndClone(t)
	config.WorkspaceDir = cloneDir

	if err := CreateAndCheckoutBranch("fix/alert-1"); err != nil {
		t.Fatalf("CreateAndCheckoutBranch failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "values.yaml"), []byte("replicas: 99\n"), 0644); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(cloneDir, "leftover-scratch.yaml")
	if err := os.WriteFile(untracked, []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ResetToMain(); err != nil {
		t.Fatalf("ResetToMain failed on dirty tree: %v", err)
	}

	branch := runGitTest(t, cloneDir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "main" {
		t.Fatalf("expected branch main after ResetToMain, got %q", branch)
	}
	data, err := os.ReadFile(filepath.Join(cloneDir, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "replicas: 1\n" {
		t.Fatalf("expected tracked file restored to origin/main, got %q", string(data))
	}
	if _, err := os.Stat(untracked); !os.IsNotExist(err) {
		t.Fatalf("expected untracked file %s to be cleaned by ResetToMain", untracked)
	}
}

func TestVerifyBranchPushed(t *testing.T) {
	origWS := config.WorkspaceDir
	defer func() { config.WorkspaceDir = origWS }()

	_, cloneDir := setupBareOriginAndClone(t)
	config.WorkspaceDir = cloneDir

	for _, bad := range []string{"", "main", "master"} {
		if err := VerifyBranchPushed(bad); err == nil {
			t.Fatalf("expected VerifyBranchPushed(%q) to reject protected/empty branch", bad)
		}
	}

	if err := CreateAndCheckoutBranch("fix/alert-42"); err != nil {
		t.Fatalf("CreateAndCheckoutBranch failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "values.yaml"), []byte("replicas: 2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Uncommitted dirty worktree must fail
	if err := VerifyBranchPushed("fix/alert-42"); err == nil {
		t.Fatalf("expected VerifyBranchPushed to fail with dirty working tree")
	}

	// Committed locally but unpushed must fail
	runGitTest(t, cloneDir, "add", "values.yaml")
	runGitTest(t, cloneDir, "commit", "-m", "fix: bump replicas")
	if err := VerifyBranchPushed("fix/alert-42"); err == nil {
		t.Fatalf("expected VerifyBranchPushed to fail when branch is not pushed to origin")
	}

	// Pushed cleanly must pass
	runGitTest(t, cloneDir, "push", "-u", "origin", "fix/alert-42")
	if err := VerifyBranchPushed("fix/alert-42"); err != nil {
		t.Fatalf("expected VerifyBranchPushed to succeed after push, got %v", err)
	}

	// New unpushed local commit must fail again
	if err := os.WriteFile(filepath.Join(cloneDir, "values.yaml"), []byte("replicas: 3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, cloneDir, "add", "values.yaml")
	runGitTest(t, cloneDir, "commit", "-m", "fix: bump again")
	if err := VerifyBranchPushed("fix/alert-42"); err == nil {
		t.Fatalf("expected VerifyBranchPushed to fail when local HEAD is ahead of origin")
	}
}
