with open('internal/workspace/git.go', 'r') as f:
    content = f.read()

diff_func = """
func GetLocalDiff(targetBranch string) (string, error) {
	cmd := exec.Command("git", "diff", targetBranch)
	cmd.Dir = config.WorkspaceDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff failed: %v", err)
	}
	return string(out), nil
}
"""

content += diff_func

with open('internal/workspace/git.go', 'w') as f:
    f.write(content)
