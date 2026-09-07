---
round: 2
round_created_at: 2026-09-07T16:24:41.347579Z
status: resolved
file: .gitignore
line: 5
severity: high
author: reviewer
---

# Issue 002: Host .env with credentials is not ignored

## Review Comment

The repository instructions and updated README use a host `.env` containing credentials, but `.gitignore` adds only `.env.inspection` at line5. `git status --short --untracked-files=all` reports `?? .env`, and `git check-ignore -v .env` produces no match. This leaves the documented local secret file easy to stage and commit accidentally. Ignore `.env` while keeping `.env.example` tracked.

## Triage

- Decision: `VALID`
- Root cause: the repository documents a host `.env` file for local credentials, but `.gitignore` only ignores the alternate `.env.inspection` filename. The existing untracked `.env` was therefore eligible for accidental staging.
- Fix: add the exact `.env` pattern to `.gitignore`. `.env.example` remains tracked because the pattern matches only the credentials file name, and `git check-ignore` will verify that `.env` is ignored while `.env.example` is not.
