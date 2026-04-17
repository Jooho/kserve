# /release-direct - KServe Release Orchestrator (Direct Mode)

Orchestrates the entire KServe release process without GitHub Copilot Agent.
Claude Code performs the version bump, cherry-picking, PR creation, and CI monitoring directly.

## Usage

```bash
/release-direct 0.18.0-rc1
```

## Arguments

| Argument  | Description                | Example                   |
|-----------|----------------------------|---------------------------|
| `version` | Version without 'v' prefix | `0.18.0-rc0`, `0.18.0-rc1`, `0.18.0` |

## Prerequisites

- Listed as **approver** in [OWNERS](../OWNERS) file (GitHub write access required)
- `gh` CLI authenticated (`gh auth login`)
- Git remotes configured:
  - `origin` → your fork (e.g., `jooho/kserve`)
  - `upstream` → `kserve/kserve`

## Git Remote Setup

```bash
git remote add upstream https://github.com/kserve/kserve.git
git remote -v  # verify
```

## Flow

### Phase 1: Prepare

1. Read current version from `kserve-deps.env` (`KSERVE_VERSION`)
2. Verify git remotes:
   ```bash
   git remote get-url origin    # should be your fork
   git remote get-url upstream  # should be kserve/kserve
   ```
   Confirm: "origin={ORIGIN}, upstream={UPSTREAM}. Correct? (y/n)"
3. If version argument is omitted, suggest candidates:
   - `vX.Y.Z-rc(N+1)` — next rc (if current is rc)
   - `vX.(Y+1).0-rc0` — minor bump
   - `vX.Y.(Z+1)-rc0` — patch bump
   - `vX.Y.Z` — final release (strip `-rcN`)
   - Present as numbered list; ask user to pick or enter custom
4. Validate version format: `X.Y.Z` or `X.Y.Z-rcN`
5. Detect prior version from `kserve-deps.env`
6. Detect release type:
   - `rc0` → RC0 flow (Phase 2A)
   - `rc1+` or final → RC1+ flow (Phase 2B)
7. Confirm: "Release v{VERSION} from v{PRIOR} [{RELEASE_TYPE}]. Proceed?"

---

### Phase 2A: RC0 — Bump + PR (master)

1. Fetch upstream master and create bump branch:
   ```bash
   git fetch upstream master
   git checkout -b release/v{VERSION} upstream/master
   ```
2. Run version bump:
   ```bash
   yes "" | make bump-version NEW_VERSION={VERSION} PRIOR_VERSION={PRIOR}
   ```
3. Commit and push to origin:
   ```bash
   git add .
   git commit -m "release: prepare release v{VERSION}"
   git push origin release/v{VERSION}
   ```
4. Create PR targeting upstream master:
   ```bash
   gh pr create \
     --repo {UPSTREAM_REPO} \
     --base master \
     --head {GITHUB_USER}:release/v{VERSION} \
     --title "release: prepare release v{VERSION}" \
     --label release \
     --body "Automated version bump for v{VERSION}."
   ```
   > Title starts with `release: prepare` → triggers `release-branch-tag.yml` on merge
5. Report: "Bump PR #{number} created: {URL}"
6. → Continue to **Phase 3: Monitor CI**

---

### Phase 2B: RC1+ / Final — Bump on master + Cherry-pick to release branch

#### Step 1: Bump PR on master

1. Fetch upstream master and create bump branch:
   ```bash
   git fetch upstream master
   git checkout -b bump/v{VERSION} upstream/master
   ```
2. Run version bump:
   ```bash
   yes "" | make bump-version NEW_VERSION={VERSION} PRIOR_VERSION={PRIOR}
   ```
3. Commit and push to origin:
   ```bash
   git add .
   git commit -m "chore: bump version to v{VERSION}"
   git push origin bump/v{VERSION}
   ```
4. Create PR targeting upstream master:
   ```bash
   gh pr create \
     --repo {UPSTREAM_REPO} \
     --base master \
     --head {GITHUB_USER}:bump/v{VERSION} \
     --title "chore: bump version to v{VERSION}" \
     --label release \
     --label cherrypick-approved \
     --body "Version bump for v{VERSION}. Will be cherry-picked to release-{MAJOR}.{MINOR}."
   ```
   > Title does NOT start with `release: prepare` → `release-branch-tag.yml` does NOT trigger on merge
5. Wait for CI and merge (same as Phase 3~4)

#### Step 2: Cherry-pick to release branch

After bump PR merges to master:

1. Find all PRs with `cherrypick-approved` label but NOT `cherrypicked`:
   ```bash
   gh pr list --repo {UPSTREAM_REPO} --state merged \
     --label cherrypick-approved \
     --json number,title,mergedAt,labels \
     --jq '[.[] | select(.labels | map(.name) | contains(["cherrypicked"]) | not)]'
   ```
2. Sort results in **reverse merge order** (oldest first → newest last, apply oldest first to avoid conflicts):
   ```bash
   gh pr list ... | sort by mergedAt ascending
   ```
3. Fetch release branch and create cherry-pick branch:
   ```bash
   git fetch upstream release-{MAJOR}.{MINOR}
   git checkout -b cherrypick/v{VERSION} upstream/release-{MAJOR}.{MINOR}
   ```
4. Cherry-pick each PR's merge commit one by one:
   ```bash
   git cherry-pick -x {MERGE_COMMIT_SHA}
   ```
   - On conflict: attempt auto-resolve
   - If not 100% confident → pause and ask user to resolve, then `git cherry-pick --continue`
5. Push to origin:
   ```bash
   git push origin cherrypick/v{VERSION}
   ```
6. Create PR targeting upstream release branch:
   ```bash
   gh pr create \
     --repo {UPSTREAM_REPO} \
     --base release-{MAJOR}.{MINOR} \
     --head {GITHUB_USER}:cherrypick/v{VERSION} \
     --title "release: prepare release v{VERSION}" \
     --label release \
     --body "Cherry-pick backport for v{VERSION}.\n\nPRs included:\n{PR_LIST}"
   ```
   > Title starts with `release: prepare` so merging this PR triggers `release-branch-tag.yml`
7. Report: "Cherry-pick PR #{number} created: {URL}"
8. → Continue to **Phase 3: Monitor CI** (for cherry-pick PR)

---

### Phase 3: Monitor CI

1. Poll CI status (every 60s, max 60min):
   ```bash
   gh pr checks {PR_NUMBER} --repo {UPSTREAM_REPO}
   ```
2. If any check fails, leave rerun comment:
   ```bash
   gh pr comment {PR_NUMBER} --repo {UPSTREAM_REPO} --body "/rerun-all"
   ```
   Continue polling. After 3 consecutive failures → ask user how to proceed.
3. When all checks pass: "CI passed. Ready to merge?"

### Phase 4: Merge

On user approval:
```bash
gh pr merge {PR_NUMBER} --repo {UPSTREAM_REPO} --squash
```
Report: "PR #{number} merged."

### Phase 5: Verify Branch & Tag

**RC0**: workflow triggers automatically on bump PR merge (targets master + title starts with `release: prepare`)

**RC1+**: cherry-pick PR targets `release-X.Y` (not master) → does NOT auto-trigger. Manually trigger:
```bash
gh workflow run release-branch-tag.yml \
  --repo {UPSTREAM_REPO} \
  -f version=v{VERSION} \
  -f dry_run=false
```

1. Wait for workflow to complete (poll every 30s, max 10min):
   ```bash
   gh run list --repo {UPSTREAM_REPO} --workflow=release-branch-tag.yml --limit 1 --json status,conclusion
   ```
2. Verify branch exists (rc0 only):
   ```bash
   gh api repos/{UPSTREAM_REPO}/branches/release-{MAJOR}.{MINOR}
   ```
3. Verify tag exists:
   ```bash
   gh api repos/{UPSTREAM_REPO}/git/ref/tags/v{VERSION}
   ```
4. Report: "Branch `release-{MAJOR}.{MINOR}` and tag `v{VERSION}` created."

### Phase 6: Publish Release

1. Ask user: "Ready to create draft release v{VERSION}?"
2. On approval, create draft:
   ```bash
   ./hack/release/publish-release.sh v{VERSION} --repo={UPSTREAM_REPO} --draft
   ```
3. Show draft release URL. Ask: "Draft looks good? Publish it? (y/n)"
4. On approval, publish:
   ```bash
   gh release edit v{VERSION} --repo={UPSTREAM_REPO} --draft=false
   ```
5. Report final release URL.

### Phase 7: Validate Downstream

1. Poll downstream workflows (every 60s, max 15min):
   ```bash
   gh run list --repo {UPSTREAM_REPO} --workflow=python-publish.yml --limit 1 --json status,conclusion
   gh run list --repo {UPSTREAM_REPO} --workflow=helm-publish.yml --limit 1 --json status,conclusion
   ```
2. Report: PyPI pass/fail, Helm pass/fail
3. Display announce message:
   ```text
   📢 KServe v{VERSION} is now available!
   Release: https://github.com/{UPSTREAM_REPO}/releases/tag/v{VERSION}
   Please test and report bugs. Feature freeze is now in effect.
   ```

### Phase 8: Artifact Validation

```bash
./hack/release/validate-release.sh v{VERSION} --repo={UPSTREAM_REPO}
```

Checks: install files, branch, tag, GitHub Release, Helm (GHCR), PyPI, container images (`docker.io/kserve`).

### Phase 9: Installation Smoke Test

Ask user: "Run installation smoke test with kind? (y/n)"

On approval, execute the following steps autonomously:

**Step 1: Check image availability before proceeding**

Check that the KServe controller image is available on Docker Hub:
```bash
docker manifest inspect docker.io/kserve/kserve-controller:v{VERSION}
```
- If image exists → proceed to Step 2
- If image does not exist → notify user:
  > "Docker images for v{VERSION} are not yet available (image build pipeline still running, typically takes a few hours after release publish).
  > Please say **'smoke test 실행해줘'** when you're ready to run it later.
  > You can also ask me to wait and poll automatically."

  If user asks to wait/poll: re-check every 5 minutes, up to 4 hours, then notify when image is available and proceed automatically.

**Step 2: Create kind cluster**
```bash
./hack/setup/dev/manage.kind-with-registry.sh
```

**Step 3: Install KServe**
```bash
./hack/kserve-install.sh --type kserve,localmodel,llmisvc --raw --kserve-version v{VERSION}
```

**Step 4: Test ISVC (sklearn-iris) — then cleanup before LLMIsvc**

Deploy and wait for ISVC to be Ready (poll every 30s, timeout 10min):
```bash
kubectl apply -f docs/samples/v1beta1/sklearn/v1/sklearn.yaml -n kserve
```
Poll until Ready:
```bash
kubectl get isvc sklearn-iris -n kserve -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
```
- If `True` → report "ISVC sklearn-iris is Ready" then delete it:
  ```bash
  kubectl delete isvc sklearn-iris -n kserve
  kubectl wait --for=delete pod -l serving.kserve.io/inferenceservice=sklearn-iris -n kserve --timeout=120s
  ```
- If timeout (10min) → report failure with:
  ```bash
  kubectl get pods -n kserve
  kubectl describe isvc sklearn-iris -n kserve
  ```
  Ask: "ISVC smoke test timed out. Abort or wait longer? (abort/wait)"

**Step 5: Test LLMIsvc (facebook-opt-125m) — after ISVC cleanup**

Deploy and wait for LLMIsvc to be Ready (poll every 30s, timeout 20min):
```bash
kubectl apply -f docs/samples/llmisvc/opt-125m-cpu/llm-inference-service-facebook-opt-125m-cpu.yaml -n kserve
```
Poll until Ready:
```bash
kubectl get llmisvc facebook-opt-125m-single -n kserve -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
```
- If `True` → report "LLMIsvc facebook-opt-125m-single is Ready" then delete it:
  ```bash
  kubectl delete llmisvc facebook-opt-125m-single -n kserve
  ```
  Then notify user: "Smoke test passed! ISVC and LLMIsvc both verified. v{VERSION} release complete!"
- If timeout (20min) → report failure with:
  ```bash
  kubectl get pods -n kserve
  kubectl describe llmisvc facebook-opt-125m-single -n kserve
  ```
  Ask: "LLMIsvc smoke test timed out. Abort or wait longer? (abort/wait)"

Cleanup: `./hack/setup/dev/manage.kind-with-registry.sh --uninstall`

---

## RC0 vs RC1+ Summary

| Step | RC0 | RC1+ |
|------|-----|------|
| Bump PR title | `release: prepare release v{VERSION}` | `chore: bump version to v{VERSION}` |
| Bump PR target | master | master |
| Auto-triggers `release-branch-tag.yml`? | Yes (on bump PR merge) | No |
| Cherry-pick step | No | Yes (separate PR to release branch) |
| Cherry-pick PR title | — | `release: prepare release v{VERSION}` |
| Auto-triggers `release-branch-tag.yml`? | — | Yes (on cherry-pick PR merge) |
| Branch created | Yes (`release-{MAJOR}.{MINOR}`) | No (already exists) |
| Tag created | Yes | Yes |

## User Approval Points

1. **Phase 1** — Confirm version, remotes, release type
2. **Phase 4** — CI passed → merge bump PR
3. **Phase 4** (RC1+ only) — CI passed → merge cherry-pick PR
4. **Phase 6** — Create draft release
5. **Phase 6** — Publish draft
6. **Phase 9** — Run smoke test

## Error Handling

| Situation | Action |
|-----------|--------|
| `make bump-version` fails | Report error, show output |
| Cherry-pick conflict (confident) | Auto-resolve and continue |
| Cherry-pick conflict (uncertain) | Pause, show diff, ask user |
| CI failure (≤3 retries) | Leave `/rerun-all` comment |
| CI failure (>3 retries) | Ask user how to proceed |
| Branch/tag missing | Suggest re-running `release-branch-tag.yml` |
| Any phase failure | Report error + suggest manual fallback |
