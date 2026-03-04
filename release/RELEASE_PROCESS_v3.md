# KServe Release Process v3 (Automated)

Simplified and automated KServe release process(1~2 months) using scripts and GitHub Actions.

## Quick Reference

| Week | Event |
|------|-------|
| 4   | Announce feature freeze |
| 5   | RC0 Released |
| 6   | RC1+ Released (if needed) |
| 7   | Final Release |

## Prerequisites

- Listed in [OWNERS](../OWNERS) file (reviewer+)
- `gh` CLI installed (for GitHub Actions validation)
- Push access to kserve/kserve

## Release Types

| Version | Branch Created | Use Case |
|---------|----------------|----------|
| v0.17.0-rc0 | ✅ Yes | First release candidate |
| v0.17.0-rc1 | ❌ No | Bug fixes after RC0 |
| v0.17.0 | ❌ No | Final production release |

---

## RC0: Initial Release Candidate

### 1. Prepare and Merge
```bash
# Prepare release
./hack/prepare-for-release.sh 0.16.0 0.17.0-rc0

# Create PR and merge to master
git add .
git commit -m "Prepare release v0.17.0-rc0"
git push origin HEAD
```

### 2. Create Release

**GitHub Actions (Recommended):**
1. Go to **Actions** → **Create Release** → **Run workflow**
2. Set `version: v0.17.0-rc0`, `dry_run: true`
3. Review output, then run with `dry_run: false`

**Local Script:**
```bash
./hack/create-release.sh v0.17.0-rc0 --dry-run --github-actions
./hack/create-release.sh v0.17.0-rc0 --github-actions
```

> **Note:** `--github-actions` flag is required for GitHub Release duplicate check

### 3. Announce
```
📢 KServe v0.17.0-rc0 is now available!
Release: https://github.com/kserve/kserve/releases/tag/v0.17.0-rc0
Please test and report bugs. Feature freeze is now in effect.
```

---

## RC1+: Bug Fix Release Candidates

### 1. Fix Bugs
- Fix bugs in master
- Label PR with `cherrypick-approved`
- Merge to master

### 2. Cherry-pick
```bash
# In merged PR, comment:
/cherry-pick release-0.17
```

### 3. Prepare and Merge
```bash
./hack/prepare-for-release.sh 0.17.0-rc0 0.17.0-rc1
# Create PR with cherrypick-approved label, merge to master
# Cherry-pick: /cherry-pick release-0.17
```

### 4. Create Release
```bash
./hack/create-release.sh v0.17.0-rc1 --dry-run --github-actions
./hack/create-release.sh v0.17.0-rc1 --github-actions
```

---

## Final Release

### 1. Prepare and Merge
```bash
./hack/prepare-for-release.sh 0.17.0-rc1 0.17.0
# Create PR with cherrypick-approved label, merge to master
# Cherry-pick: /cherry-pick release-0.17
```

### 2. Create Release
```bash
./hack/create-release.sh v0.17.0 --dry-run --github-actions
./hack/create-release.sh v0.17.0 --github-actions
```

> **Note:** PyPI publishing happens automatically via `python-publish.yml` workflow

### 3. Announce
```
🎉 KServe v0.17.0 is now available!
Release: https://github.com/kserve/kserve/releases/tag/v0.17.0
PyPI: https://pypi.org/project/kserve/
```

---

## Script Options

### create-release.sh

| Option | Description |
|--------|-------------|
| `--dry-run` | Validate and show plan (no changes) |
| `--validate-only` | Run validations only |
| `--github-actions` | Enable gh CLI checks (required for GitHub Release validation) |

**Examples:**
```bash
# Local validation (no gh CLI needed)
./hack/create-release.sh v0.17.0-rc0 --validate-only

# Full validation with gh CLI
./hack/create-release.sh v0.17.0-rc0 --dry-run
```

**Remote Detection:**
- With `--github-actions`: Uses `origin` remote
- Without `--github-actions`: Auto-detects kserve/kserve remote (supports forks)

---

## Troubleshooting

### Version mismatch
```
❌ Version mismatch! Input: v0.17.0-rc0, kserve-deps.env: v0.16.0
```
**Fix:** Run `prepare-for-release.sh` first

### Tag already exists
```
❌ Tag v0.17.0-rc0 already exists!
```
**Fix:** Use different version or delete existing tag

### Branch missing (RC1+)
```
❌ Branch release-0.17 does not exist!
```
**Fix:** Create RC0 first

### Permission denied
```
❌ Permission denied! Only OWNERS (reviewer+) can run this workflow.
```
**Fix:** Must be in [OWNERS](../OWNERS) file

---

## Quick Commands

**RC0:**
```bash
./hack/prepare-for-release.sh 0.16.0 0.17.0-rc0
# PR → merge
./hack/create-release.sh v0.17.0-rc0 --dry-run --github-actions
./hack/create-release.sh v0.17.0-rc0 --github-actions
```

**RC1+:**
```bash
# Fix bugs → PR with cherrypick-approved → merge → /cherry-pick release-0.17
./hack/prepare-for-release.sh 0.17.0-rc0 0.17.0-rc1
# PR with cherrypick-approved → merge → /cherry-pick release-0.17
./hack/create-release.sh v0.17.0-rc1 --dry-run --github-actions
./hack/create-release.sh v0.17.0-rc1 --github-actions
```

**Final:**
```bash
./hack/prepare-for-release.sh 0.17.0-rc1 0.17.0
# PR with cherrypick-approved → merge → /cherry-pick release-0.17
./hack/create-release.sh v0.17.0 --dry-run --github-actions
./hack/create-release.sh v0.17.0 --github-actions
```

---

## Resources

- Scripts: [`prepare-for-release.sh`](../hack/prepare-for-release.sh), [`create-release.sh`](../hack/create-release.sh)
- Workflows: [`create-release.yml`](../.github/workflows/create-release.yml), [`python-publish.yml`](../.github/workflows/python-publish.yml)
- Help: `./hack/create-release.sh --help`
