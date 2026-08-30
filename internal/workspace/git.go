package workspace

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

func InitWorkspace() {
	_ = exec.Command("git", "config", "--global", "user.email", "ai-incident-commander@vinhthang.dev").Run()
	_ = exec.Command("git", "config", "--global", "user.name", "AI Incident Commander").Run()
	if config.GithubToken != "" {
		cmd := exec.Command("sh", "-c", fmt.Sprintf("git config --global url.\"https://oauth2:%s@github.com/\".insteadOf \"https://github.com/\"", config.GithubToken))
		_ = cmd.Run()
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
	cmd := exec.Command("git", "checkout", "-b", branchName)
	cmd.Dir = config.WorkspaceDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create branch %s: %v", branchName, err)
	}
	return nil
}

func ResetToMain() {
	cmd := exec.Command("git", "checkout", "main")
	cmd.Dir = config.WorkspaceDir
	_ = cmd.Run()
}
