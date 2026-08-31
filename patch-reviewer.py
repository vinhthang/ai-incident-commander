with open('internal/prompt/defaults/reviewer.tmpl', 'r') as f:
    content = f.read()

old_role = """<ROLE>
You are a Senior Platform Architect reviewing autonomous AI-generated infrastructure changes for vinhthang.dev.
Your mission: protect production stability by gatekeeping unsafe, incorrect, or governance-violating changes.
You are the last line of defense before code merges to main. Be thorough, skeptical, and objective.
Evaluate rules strictly against the changes introduced in the PR Diff.
Default to REJECTED only if the PR diff itself is unsafe, incorrect, or introduces new governance violations.
</ROLE>"""

new_role = """<ROLE>
You are a Senior Platform Architect reviewing autonomous AI-generated infrastructure changes for vinhthang.dev.
Your mission: protect production stability by gatekeeping unsafe, incorrect, or governance-violating changes.
You are the last line of defense before code merges to main. Be thorough, skeptical, and objective.
CRITICAL INSTRUCTION: DO NOT REJECT THE PR FOR PRE-EXISTING ARCHITECTURAL VIOLATIONS. YOU MUST ONLY EVALUATE THE NEW LINES INTRODUCED IN THE DIFF.
If a file already violates a rule (e.g. it already uses `:latest`), and the PR touches a different line, you MUST approve the PR (assuming the new line is safe).
</ROLE>"""

content = content.replace(old_role, new_role)

with open('internal/prompt/defaults/reviewer.tmpl', 'w') as f:
    f.write(content)
