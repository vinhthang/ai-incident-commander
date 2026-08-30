package prompt

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
)

//go:embed defaults/*.tmpl
var defaultTemplates embed.FS

const ConfigMapTemplateDir = "/etc/commander/templates"

func renderTemplate(name string, data interface{}) (string, error) {
	var tmplContent []byte
	var err error

	// Try reading from ConfigMap first
	customPath := filepath.Join(ConfigMapTemplateDir, name)
	tmplContent, err = os.ReadFile(customPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Fallback to embedded default
			tmplContent, err = defaultTemplates.ReadFile(filepath.Join("defaults", name))
			if err != nil {
				return "", fmt.Errorf("failed to read embedded template %s: %w", name, err)
			}
		} else {
			return "", fmt.Errorf("failed to read custom template %s: %w", customPath, err)
		}
	}

	tmpl, err := template.New(name).Parse(string(tmplContent))
	if err != nil {
		return "", fmt.Errorf("failed to parse template %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template %s: %w", name, err)
	}

	return buf.String(), nil
}

type TriageData struct {
	AlertName   string
	Labels      map[string]string
	Annotations map[string]string
	Telemetry   string
}

func RenderTriagePrompt(data TriageData) (string, error) {
	return renderTemplate("triage.tmpl", data)
}

type FixerData struct {
	IssueNumber int
	BranchName  string
	AlertName   string
	Diagnosis   string
	Telemetry   string
}

func RenderFixerPrompt(data FixerData) (string, error) {
	return renderTemplate("fixer.tmpl", data)
}

type ReviewerData struct {
	PRNumber          int
	BranchName        string
	OriginalDiagnosis string
	PRDiff            string
}

func RenderReviewerPrompt(data ReviewerData) (string, error) {
	return renderTemplate("reviewer.tmpl", data)
}
