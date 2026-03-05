# KServe Release Process v3 (Automated)

Simplified and automated KServe release process(6~8 weeks) using scripts and GitHub Actions.

## Quick Reference

| Week | Event |
|------|-------|
| 1-4  | Development|
| 4   | Announce feature freeze |
| 5   | RC0 Released |
| 6   | RC1+ Released (if needed) |
| 7   | Final Release |

## Prerequisites for executing gitaction

- Listed in [OWNERS](../OWNERS) file (reviewer+)
- Push access to kserve/kserve

## Release Types

| Version | Branch Created | Use Case |
|---------|----------------|----------|
| v0.17.0-rc0 | ✅ Yes | First release candidate |
| v0.17.0-rc1 | ❌ No | Bug fixes after RC0 |
| v0.17.0 | ❌ No | Final production release |

---

## RC0: Initial Release Candidate

> **Note:** This guide uses v0.17.0 as an example. Replace with your actual release version.

### 1. Phase 1: Prepare Release Files

```bash
git clone git@github.com:YOUR_ORG/kserve.git
cd kserve

git checkout -b release/0.17.0-rc0

# Phase 1: Update manifests and version files
make bump-version NEW_VERSION=0.17.0-rc0 PRIOR_VERSION=0.16.0

# Push Phase 1 changes (then create PR to master via GitHub UI)
git add .
git commit -m "prepare release v0.17.0-rc0"
git push -u origin release/0.17.0-rc0
```

**Create PR with title (MUST copy exactly):**

```text
release: prepare KServe v0.17.0-rc0
```

**After PR merge:**

- PyPI packages are **automatically published** via `python-publish.yml` (triggered by `python/VERSION` change)
- Verify: **Actions** → **Upload Python Package**

### 2. Phase 2: Update Lock Files

**After PyPI packages are published, run Phase 2:**

```bash
# Phase 2: Update uv.lock and run precommit
make bump-version NEW_VERSION=0.17.0-rc0 PRIOR_VERSION=0.16.0 PHASE=2

# Push Phase 2 changes
git add .
git commit -m "release v0.17.0-rc0"
git push
```

**Create PR with title (MUST copy exactly):**

```text
release: KServe v0.17.0-rc0
```

### 3. Create Release

**After Phase 2 PR is merged:**

**GitHub Actions (Recommended):**
1. Go to **Actions** → **Create Release** → **Run workflow**
2. Set `version: v0.17.0-rc0`, `dry_run: true`
3. Review output, then run with `dry_run: false`

**Local Script (only for testing):**
```bash
./hack/create-release.sh v0.17.0-rc0 --dry-run
```

**Published Helm Charts**

Helm charts will be published automatically by `helm-publish` gitaction that will be triggered when release created.

**Verify Helm Charts:**

- Check workflow: **Actions** → **helm-publish**
- Verify:
  - GHCR: <https://github.com/orgs/kserve/packages>
  - Release Assets: Check .tgz files in GitHub Release

### 5. Announce
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

### 3. Phase 1: Prepare Release Files

```bash
# Phase 1: Update manifests and version files
make bump-version NEW_VERSION=0.17.0-rc1 PRIOR_VERSION=0.17.0-rc0

# Create PR with cherrypick-approved label, merge to master
# Then cherry-pick: /cherry-pick release-0.17
```

**Create PR with title (MUST copy exactly):**

```text
release: prepare KServe v0.17.0-rc1
```

**After PR merge:**

- PyPI packages are **automatically published** via `python-publish.yml` (triggered by `python/VERSION` change)
- Verify: **Actions** → **Upload Python Package**

### 4. Phase 2: Update Lock Files

**After PyPI packages are published, run Phase 2:**

```bash
# Phase 2: Update uv.lock and run precommit
make bump-version NEW_VERSION=0.17.0-rc1 PRIOR_VERSION=0.17.0-rc0 PHASE=2

# Create PR and cherry-pick to release branch
```

**Create PR with title (MUST copy exactly):**

```text
release: KServe v0.17.0-rc1
```

### 5. Create Release

**After Phase 2 PR is merged:**

**GitHub Actions (Recommended):**

1. Go to **Actions** → **Create Release** → **Run workflow**
2. Set `version: v0.17.0-rc1`, `dry_run: true`
3. Review output, then run with `dry_run: false`

**Local Script (only for testing):**

```bash
./hack/create-release.sh v0.17.0-rc1 --dry-run
```

**Published Helm Charts**

Helm charts will be published automatically by `helm-publish` gitaction that will be triggered when release created.

**Verify Helm Charts:**

- Check workflow: **Actions** → **helm-publish**
- Verify:
  - GHCR: <https://github.com/orgs/kserve/packages>
  - Release Assets: Check .tgz files in GitHub Release

---

## Final Release

### 1. Phase 1: Prepare Release Files

```bash
# Phase 1: Update manifests and version files
make bump-version NEW_VERSION=0.17.0 PRIOR_VERSION=0.17.0-rc1

# Create PR with cherrypick-approved label, merge to master
# Then cherry-pick: /cherry-pick release-0.17
```

**Create PR with title (MUST copy exactly):**

```text
release: prepare KServe v0.17.0
```

**After PR merge:**

- PyPI packages are **automatically published** via `python-publish.yml` (triggered by `python/VERSION` change)
- Verify: **Actions** → **Upload Python Package**

### 2. Phase 2: Update Lock Files

**After PyPI packages are published, run Phase 2:**

```bash
# Phase 2: Update uv.lock and run precommit
make bump-version NEW_VERSION=0.17.0 PRIOR_VERSION=0.17.0-rc1 PHASE=2

# Create PR and cherry-pick to release branch
```

**Create PR with title (MUST copy exactly):**

```text
release: KServe v0.17.0
```

### 3. Create Release

**After Phase 2 PR is merged:**

**GitHub Actions (Recommended):**

1. Go to **Actions** → **Create Release** → **Run workflow**
2. Set `version: v0.17.0`, `dry_run: true`
3. Review output, then run with `dry_run: false`

**Local Script (only for testing):**

```bash
./hack/create-release.sh v0.17.0 --dry-run
```

**Published Helm Charts**

Helm charts will be published automatically by `helm-publish` gitaction that will be triggered when release created.

**Verify Helm Charts:**

- Check workflow: **Actions** → **helm-publish**
- Verify:
  - GHCR: <https://github.com/orgs/kserve/packages>
  - Release Assets: Check .tgz files in GitHub Release

### 5. Announce

```text
🎉 KServe v0.17.0 is now available!
Release: https://github.com/kserve/kserve/releases/tag/v0.17.0
PyPI: https://pypi.org/project/kserve/
```

---

## Resources

- Scripts: [`prepare-for-release.sh`](../hack/prepare-for-release.sh), [`create-release.sh`](../hack/create-release.sh)
- Workflows: [`create-release.yml`](../.github/workflows/create-release.yml), [`python-publish.yml`](../.github/workflows/python-publish.yml)
- Help: `./hack/create-release.sh --help`
