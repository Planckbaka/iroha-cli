<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-23 | Updated: 2026-05-23 -->

# .github

## Purpose
GitHub-specific configuration: CI/CD workflows, issue templates, and pull request templates.

## Key Files
| File | Description |
|------|-------------|
| `PULL_REQUEST_TEMPLATE.md` | PR description template |
| `ISSUE_TEMPLATE/bug_report.md` | Bug report issue template |
| `ISSUE_TEMPLATE/feature_request.md` | Feature request issue template |
| `workflows/ci.yml` | CI pipeline — build, lint (golangci-lint), test |
| `workflows/release.yml` | Release pipeline — GoReleaser build and publish |

## For AI Agents

### Working In This Directory
- CI runs on push to main and on pull requests
- Linting uses `.golangci.yml` config at repo root
- Releases use GoReleaser with `.goreleaser.yml` config

<!-- MANUAL: -->
