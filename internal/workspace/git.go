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

func CreateAndCheckoutBranch(branchName string) error {
	trimmed := strings.TrimSpace(branchName)
	if trimmed == "" || trimmed == "main" || trimmed == "master" {
		return fmt.Errorf("invalid branch name for autonomous fix: %s", branchName)
	}

	cmd := exec.Command("git", "checkout", "-b", trimmed)
	cmd.Dir = config.WorkspaceDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create branch %s: %v (%s)", trimmed, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ResetToMain switches the workspace back to main and fast-forwards from origin.
func ResetToMain() error {
	checkoutCmd := exec.Command("git", "checkout", "main")
	checkoutCmd.Dir = config.WorkspaceDir
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout main failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	pullCmd := exec.Command("git", "pull", "--ff-only")
	pullCmd.Dir = config.WorkspaceDir
	if out, err := pullCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git pull failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func GetLocalDiff(targetBranch string) (string, error) {
	cmd := exec.Command("git", "diff", targetBranch)
	cmd.Dir = config.WorkspaceDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff failed: %v", err)
	}
	return string(out), nil
}
