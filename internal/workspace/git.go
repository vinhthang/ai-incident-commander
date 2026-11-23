package workspace

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

var workspaceMu sync.Mutex

// Lock acquires the exclusive workspace mutex before mutating git branches or files.
func Lock() {
	workspaceMu.Lock()
}

// Unlock releases the exclusive workspace mutex.
func Unlock() {
	workspaceMu.Unlock()
}

func InitWorkspace() {
	if out, err := exec.Command("git", "config", "--global", "user.email", "ai-incident-commander@vinhthang.dev").CombinedOutput(); err != nil {
		log.Printf("⚠️ Failed to configure git user.email: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "config", "--global", "user.name", "AI Incident Commander").CombinedOutput(); err != nil {
		log.Printf("⚠️ Failed to configure git user.name: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if config.GithubToken != "" {
		key := fmt.Sprintf("url.https://oauth2:%s@github.com/.insteadOf", config.GithubToken)
		if _, err := exec.Command("git", "config", "--global", key, "https://github.com/").CombinedOutput(); err != nil {
			log.Printf("⚠️ Failed to configure git token URL rewrite: %v", err)
		}
	}

	parentDir := filepath.Dir(config.WorkspaceDir)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		log.Printf("⚠️ Failed to create workspace parent directory %s: %v", parentDir, err)
	}
	if _, err := os.Stat(config.WorkspaceDir); os.IsNotExist(err) {
		log.Printf("Cloning workspace...")
		cmd := exec.Command("git", "clone", fmt.Sprintf("https://github.com/%s/%s.git", config.GithubOwner, config.GithubRepo), config.WorkspaceDir)
		cmd.Dir = parentDir
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("🚨 Failed to clone workspace %s: %v (%s)", config.WorkspaceDir, err, strings.TrimSpace(string(out)))
		}
	} else {
		log.Printf("Workspace exists. Pulling latest.")
		if err := ResetToMain(); err != nil {
			log.Printf("⚠️ Workspace refresh on startup failed: %v", err)
		}
	}
}

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = config.WorkspaceDir
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		return trimmed, fmt.Errorf("git %s failed: %w (%s)", strings.Join(args, " "), err, trimmed)
	}
	return trimmed, nil
}

func CreateAndCheckoutBranch(branchName string) error {
	trimmed := strings.TrimSpace(branchName)
	if trimmed == "" || trimmed == "main" || trimmed == "master" {
		return fmt.Errorf("invalid branch name for autonomous fix: %s", branchName)
	}

	if _, err := runGit("checkout", "-b", trimmed); err != nil {
		return fmt.Errorf("failed to create branch %s: %w", trimmed, err)
	}
	return nil
}

// ResetToMain force-switches the workspace back to main, discarding dirty tracked/untracked leftovers.
func ResetToMain() error {
	if _, originErr := runGit("remote", "get-url", "origin"); originErr == nil {
		if _, err := runGit("fetch", "origin", "main"); err != nil {
			return err
		}
		if _, err := runGit("checkout", "-f", "main"); err != nil {
			return err
		}
		if _, err := runGit("reset", "--hard", "origin/main"); err != nil {
			return err
		}
		if _, err := runGit("clean", "-fd"); err != nil {
			return err
		}
		return nil
	}

	if _, err := runGit("checkout", "-f", "main"); err != nil {
		return err
	}
	if _, err := runGit("clean", "-fd"); err != nil {
		return err
	}
	return nil
}

func GetLocalDiff(targetBranch string) (string, error) {
	cmd := exec.Command("git", "diff", targetBranch)
	cmd.Dir = config.WorkspaceDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// VerifyBranchPushed confirms branchName is checked out with a clean working tree and HEAD matching origin.
func VerifyBranchPushed(branchName string) error {
	trimmed := strings.TrimSpace(branchName)
	if trimmed == "" || trimmed == "main" || trimmed == "master" {
		return fmt.Errorf("invalid branch name for verification: %s", branchName)
	}

	statusOut, err := runGit("status", "--porcelain")
	if err != nil {
		return err
	}
	if statusOut != "" {
		return fmt.Errorf("workspace has uncommitted or untracked changes:\n%s", statusOut)
	}

	curBranch, err := runGit("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	if curBranch != trimmed {
		return fmt.Errorf("workspace is on branch %q instead of %q", curBranch, trimmed)
	}

	localHead, err := runGit("rev-parse", "HEAD")
	if err != nil {
		return err
	}

	lsOut, err := runGit("ls-remote", "--heads", "origin", trimmed)
	if err != nil {
		return err
	}
	fields := strings.Fields(lsOut)
	if len(fields) == 0 {
		return fmt.Errorf("branch %s has not been pushed to origin", trimmed)
	}
	if fields[0] != localHead {
		return fmt.Errorf("branch %s local HEAD (%s) differs from origin (%s)", trimmed, localHead, fields[0])
	}
	return nil
}
