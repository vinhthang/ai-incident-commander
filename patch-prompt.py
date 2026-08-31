import re

with open('internal/prompt/prompt.go', 'r') as f:
    content = f.read()

# 1. Update FixerData
old_fixer_data = """type FixerData struct {
	IssueNumber int
	BranchName  string
	AlertName   string
	Diagnosis   string
	Telemetry   string
}"""

new_fixer_data = """type FixerData struct {
	IssueNumber    int
	BranchName     string
	AlertName      string
	Diagnosis      string
	Telemetry      string
	ReviewFeedback string
}"""

content = content.replace(old_fixer_data, new_fixer_data)

# 2. Add SeniorFixerData
senior_fixer_code = """
type SeniorFixerData struct {
	IssueNumber int
	BranchName  string
	Diagnosis   string
	LocalDiff   string
}

func RenderSeniorFixerPrompt(data SeniorFixerData) (string, error) {
	return renderTemplate("senior_fixer.tmpl", data)
}
"""

content += senior_fixer_code

with open('internal/prompt/prompt.go', 'w') as f:
    f.write(content)
