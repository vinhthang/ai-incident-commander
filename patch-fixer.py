with open('internal/prompt/defaults/fixer.tmpl', 'r') as f:
    content = f.read()

injection = """<INPUT>
Issue: #{{ .IssueNumber }} — Alert '{{ .AlertName }}'
Target Branch: {{ .BranchName }}

Triage Diagnosis:
{{ .Diagnosis }}
{{ if .ReviewFeedback }}
<FEEDBACK>
YOUR PREVIOUS FIX ATTEMPT WAS REJECTED BY THE REVIEWER. 
Please read the feedback carefully and correct your code:
{{ .ReviewFeedback }}
</FEEDBACK>
{{ end }}
Telemetry:
{{ .Telemetry }}
</INPUT>"""

old_input = """<INPUT>
Issue: #{{ .IssueNumber }} — Alert '{{ .AlertName }}'
Target Branch: {{ .BranchName }}

Triage Diagnosis:
{{ .Diagnosis }}

Telemetry:
{{ .Telemetry }}
</INPUT>"""

content = content.replace(old_input, injection)

with open('internal/prompt/defaults/fixer.tmpl', 'w') as f:
    f.write(content)
