package workspace

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

func InitWorkspace() {
	_ = exec.Command("git", "config", "--global", "user.email", "ai-incident-commander@vinhthang.dev").Run()
	_ = exec.Command("git", "config", "--global", "user.name", "AI Incident Commander").Run()
	if config.GithubToken != "" {
		key := fmt.Sprintf("url.https://oauth2:%s@github.com/.insteadOf", config.GithubToken)
		_ = exec.Command("git", "config", "--global", key, "https://github.com/").Run()
	}

	_ = os.MkdirAll("/app/workspace", 0755)
	if _, err := os.Stat(config.WorkspaceDir); os.IsNotExist(err) {
		log.Printf("Cloning workspace...")
		cmd := exec.Command("git", "clone", fmt.Sprintf("https://github.com/%s/%s.git", config.GithubOwner, config.GithubRepo), config.WorkspaceDir)
		cmd.Dir = "/app/workspace"
		_ = cmd.Run()
	} else {
		log.Printf("Workspace exists. Pulling latest.")
		cmd := exec.Command("git", "checkout", "main")
		cmd.Dir = config.WorkspaceDir
		_ = cmd.Run()
		cmd = exec.Command("git", "pull")
		cmd.Dir = config.WorkspaceDir
		_ = cmd.Run()
	}
}

func CreateAndCheckoutBranch(branchName string) error {
	trimmed := strings.TrimSpace(branchName)
	if trimmed == "" || trimmed == "main" || trimmed == "master" {
		return fmt.Errorf("invalid branch name for autonomous fix: %s", branchName)
	}

	cmd := exec.Command("git", "checkout", "-b", trimmed)
	cmd.Dir = config.WorkspaceDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create branch %s: %v", trimmed, err)
	}
	return nil
}

func ResetToMain() {
	cmd := exec.Command("git", "checkout", "main")
	cmd.Dir = config.WorkspaceDir
	_ = cmd.Run()
}
