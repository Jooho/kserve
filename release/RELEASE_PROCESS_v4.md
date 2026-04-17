# KServe Release Process v4

Agentic release process using GitHub Copilot Cloud Agent for automated version bumping.
Two paths are available: standard (GitHub UI) and Claude Code (CLI-based orchestration).

## Quick Reference

| Week | Event |
|------|-------|
| 1-4  | Development |
| 4    | Announce feature freeze |
| 5    | RC0 Released |
| 6    | RC1+ Released (if needed) |
| 7    | Final Release |

## What Changed from v3

| Area | v3 | v4 |
|------|----|----|
| Version bump | Manual `make bump-version` + PR | Copilot Agent via GitHub Issue |
| Branch & Tag | GitHub Actions (manual trigger) | Auto-triggered on PR merge |
| Release publish | GitHub UI (manual) | `publish-release.sh --draft` → review → publish |
| Orchestration | Manual each step | Claude Code `/release` skill (optional) |

## Prerequisites

### Path A (GitHub UI)

- Listed as **approver** in [OWNERS](../OWNERS) file (GitHub write access required)
- GitHub Copilot enabled on the repo (for agent-based bump)

### Path B (Claude Code)

- Listed as **approver** in [OWNERS](../OWNERS) file (GitHub write access required)
- `gh` CLI authenticated (`gh auth login`)
- Claude Code CLI installed
- GitHub Copilot enabled on the repo (for agent-based bump)

---

## Path A: Standard (GitHub UI)

For users without Claude Code. Uses GitHub Issue + Copilot Agent for version bump.

### RC0

**Step 0: Set Release Variables**

```bash
export NEW_VERSION=0.18.0
export PRIOR_VERSION=0.17.0
```

**Step 1: Create GitHub Issue (Version Bump)**

1. Go to **Issues** → **New Issue** → **Release** template
2. Fill in `New Version: ${NEW_VERSION}-rc0`
3. Click **Submit**
4. Go to **Assignees** → **Assign to Agent** → select `release-agent`

The Copilot Agent will:

- Run `make bump-version NEW_VERSION=${NEW_VERSION}-rc0 PRIOR_VERSION=${PRIOR_VERSION}`
- Create a PR titled `[WIP] Prepare release for version ${NEW_VERSION}-rc0`
- Remove `[WIP]` when done

**Step 2: Review and Merge PR**

Wait until the PR title no longer has `[WIP]`. Then:

1. Review the bump PR
2. Verify CI passes
3. Merge (squash)

**Step 3: Prepare Release (Branch & Tag)**

The `Prepare Release (Branch & Tag)` workflow triggers on master merge when `kserve-deps.env` changes, but internally checks the PR title — it only proceeds if the title starts with `release: prepare`. The Copilot Agent sets this title automatically.

If it doesn't trigger automatically, run manually:

1. Go to **Actions** → **Prepare Release (Branch & Tag)** → **Run workflow**
2. Set `version: v${NEW_VERSION}-rc0`, `dry_run: false`

This creates:

- Branch: `release-${NEW_VERSION%.*}` (e.g., `release-0.18`)
- Tag: `v${NEW_VERSION}-rc0`

**Step 4: Publish Release**

1. Go to **Releases** → **New release** → select tag `v${NEW_VERSION}-rc0`
2. Attach all files from the `install/v${NEW_VERSION}-rc0/` directory
3. Check **Set as a pre-release**
4. Click **Publish release**

Publishing automatically triggers:

- `python-publish` → PyPI
- `helm-publish` → GHCR

**Step 5: Announce**

```bash
echo "📢 KServe v${NEW_VERSION}-rc0 is now available!"
echo "Release: https://github.com/kserve/kserve/releases/tag/v${NEW_VERSION}-rc0"
echo "Please test and report bugs. Feature freeze is now in effect."
```

---

### RC1+

**Step 0: Set Release Variables**

```bash
export NEW_VERSION=0.18.0
export PRIOR_VERSION=0.18.0-rc0
```

**Step 1: Fix Bugs and Cherry-pick**

- Fix bugs in master, label PR with `cherrypick-approved`
- After merge, comment on the PR:

```text
/cherry-pick release-${NEW_VERSION%.*}
```

**Step 2: Create GitHub Issue (Version Bump)**

Same as RC0 Step 1, with `New Version: ${NEW_VERSION}-rc1`.
The agent detects prior version from `kserve-deps.env` automatically.

**Step 3: Review and Merge PR**

Same as RC0 Step 2.

**Step 4: Prepare Release (Tag Only)**

Same as RC0 Step 3. Creates tag only (branch already exists).

**Step 5: Publish Release**

Same as RC0 Step 4, using tag `v${NEW_VERSION}-rc1` and files from `install/v${NEW_VERSION}-rc1/`.

---

### Final Release

> ⚠️ **Not yet tested with v4 process. Validate before executing.**

**Step 0: Set Release Variables**

```bash
export NEW_VERSION=0.18.0
export PRIOR_VERSION=0.18.0-rc1  # Last RC
```

**Step 1: Fix Bugs and Cherry-pick**

Same as RC1+ Step 1.

**Step 2: Create GitHub Issue (Version Bump)**

Same as RC0 Step 1, with `New Version: ${NEW_VERSION}` (no `-rcN` suffix).

**Step 3: Review and Merge PR**

Same as RC0 Step 2. Verify the bump removes the `-rcN` suffix everywhere.

**Step 4: Prepare Release (Tag Only)**

Same as RC0 Step 3.

**Step 5: Publish Final Release**

Same as RC0 Step 4, using tag `v${NEW_VERSION}` and files from `install/v${NEW_VERSION}/`.
Ensure **Set as a pre-release** is **unchecked**.

---

## Path B: Claude Code (`/release` skill)

For users with Claude Code CLI. Automates the entire flow from a single command.

### Usage

```bash
/release 0.18.0-rc0
```

### What It Does

| Phase | Action |
|-------|--------|
| 1 | Read current version, detect repo, confirm with user |
| 2 | Create GitHub Issue + assign Copilot `release-agent` |
| 3 | Poll until `[WIP]` removed from PR title, then check CI |
| 4 | Ask user to merge, then merge PR |
| 5 | Trigger/verify `release-branch-tag` workflow, check branch + tag |
| 6 | Run `publish-release.sh --draft`, ask user to confirm, publish |
| 7 | Poll `python-publish` and `helm-publish` workflows |
| 8 | Run `validate-release.sh` — verify all artifacts (files, branch, tag, release, helm, PyPI, images) |
| 9 | Installation smoke test — install components and verify pods Running |

### RC0

```bash
/release 0.18.0-rc0
```

Follow prompts:

1. Confirm target repo (auto-detected from git remote)
2. Confirm version
3. Wait for Copilot Agent to complete PR (~20min for version bump, ~1hr total including CI)
4. Approve merge when CI passes
5. Approve publish after reviewing draft release

### RC1+

```bash
/release 0.18.0-rc1
```

Same flow. Agent detects prior version from `kserve-deps.env`.

### Final Release

> ⚠️ **Not yet tested. Validate before executing.**

```bash
/release 0.18.0
```

---

## Resources

- Scripts: [`prepare-for-release.sh`](../hack/release/prepare-for-release.sh), [`create-branch-tag.sh`](../hack/release/create-branch-tag.sh), [`publish-release.sh`](../hack/release/publish-release.sh)
- Workflows: [`release-branch-tag.yml`](../.github/workflows/release-branch-tag.yml), [`python-publish.yml`](../.github/workflows/python-publish.yml), [`helm-publish.yml`](../.github/workflows/helm-publish.yml)
- Agent: [`.github/agents/release-agent.md`](../.github/agents/release-agent.md)
- Claude Code Skill: [`.claude/skills/release/SKILL.md`](../.claude/skills/release/SKILL.md)
- Previous process: [`RELEASE_PROCESS_v3.md`](./RELEASE_PROCESS_v3.md)
