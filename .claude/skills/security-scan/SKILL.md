---
name: security-scan
description: Run comprehensive security and vulnerability checks on tibber-pulse-bot. Scans Go dependencies with govulncheck, checks git history and uncommitted diffs for bridge passwords or credentials, and verifies .gitignore isolation.
disable-model-invocation: true
---

# security-scan

Run security checks to ensure no vulnerabilities or credentials exist in the codebase.

## 1. Vulnerability Scanning (`govulncheck`)

Run Go's official vulnerability analyzer against all packages and direct/transitive dependencies:

```bash
command -v govulncheck >/dev/null 2>&1 && govulncheck ./... || echo "govulncheck not installed (run in CI or go install golang.org/x/vuln/cmd/govulncheck@latest)"
```

## 2. Pre-Push Credential Gate

Execute the repository's pre-push secret audit script:

```bash
bash .claude/hooks/pre-push-secret-gate.sh --scan
```

This checks:
- No tracked `.env` or `.env.*` files (only `.env.example` allowed).
- No raw bridge password regex matches in staged or unstaged diffs.
- No private key files (`*.pem`, `*.key`) accidentally staged.

## 3. Git Isolation & Status

Verify that git status is clean and ignored files are not tracked:

```bash
git status --ignored
git ls-files | grep -E '\.env$|\.pem$|\.key$' && echo "FAIL: sensitive file tracked" || echo "PASS: no sensitive files tracked"
```
