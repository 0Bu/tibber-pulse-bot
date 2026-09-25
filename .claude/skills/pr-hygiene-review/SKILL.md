---
name: pr-hygiene-review
description: Check a PR's commit range, title/description and diff for personal information, secrets and non-English prose that the mechanical scripts/check-pr-hygiene.sh cannot recognise by shape alone — a real name in running text, a spelled-out address, a pasted log with a hostname or SSID, mixed-language phrasing. Use before opening a PR and again before merge (the merge gate requires its stamp). Read-only unless the user asks for a rewrite.
---

# pr-hygiene-review

This project is public. Anything in a pushed commit — message, diff or a
file that was later deleted — stays in the commit objects. The bridge password
has already leaked once this way (a "sticker PIN, e.g. …" example in a skill
file). This skill is the human half of the check; the mechanical half runs
automatically.

## 1. Run the mechanical check

```bash
scripts/check-pr-hygiene.sh            # commit messages + per-commit patches vs origin/main
```

It catches what has a reliable shape: non-noreply emails, phone numbers, GPS
coordinates, private keys, GitHub/AWS tokens, bridge-password-shaped codes
(4+4 uppercase alphanumerics mixing letters and digits; the placeholder
`XXXX-XXXX` is allowed) and German prose. The
pre-push hook runs it on every push; the `pr-policy` workflow runs it on the
live PR title/description and every commit's patch.

## 2. Read what the script cannot judge

Read `git log origin/main..HEAD` (full messages), the PR title and body as
GitHub renders them, and the diff itself:

- a real name, employer or household member in prose; a street address or
  location spelled out in words;
- a pasted log, `curl` output or MQTT dump carrying a bridge password, a WiFi
  SSID/password, a meter serial that isn't the documented example
  `LGZ-81199038`, or a public IP / hostname;
- prose in any language other than English — the script only recognises German,
  and only when a line has three or more German function words.

The home-lab LAN addresses already used as examples throughout the docs
(`192.168.107.118`, `192.168.1.27`) are accepted and need no finding.

## 3. If something is found

Fix the text. If the finding is already pushed, editing the PR description or
adding a follow-up commit does **not** remove it: say so, and let the user
decide whether to rewrite history on their own branch. Never rewrite a branch
you do not own. If it is a real credential, it must be rotated regardless
(CLAUDE.md > Security).

## 4. Record the gate

When both halves are clean on the current head, tick the line in the PR body's
**Merge gates** section with the bare head SHA (`git rev-parse --short=12 HEAD`):

```
- [x] `$pr-hygiene-review` clean — merge gate @ 1a2b3c4d5e6f
```

Any later push re-stales the stamp; re-run both halves before re-stamping.
