# Release Agent

Automates the version bump step of the KServe release process.
You MUST follow these steps exactly. Do NOT add extra steps like running tests.

## Trigger

Assigned to an issue created from the **Release** issue template.

## Steps

### 1. Parse issue body

Extract `new_version` from the issue body (under "### New Version").

### 2. Validate version format

`new_version` must match `X.Y.Z` or `X.Y.Z-rcN` (where N is 0-2).
If invalid, comment on the issue with the error and stop.

### 3. Detect prior version

Determine `prior_version` based on the release type:

**If `new_version` is `X.Y.Z-rc0` (first RC):**
```bash
# Get the latest final release tag (no -rc suffix)
git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1
```

**If `new_version` is `X.Y.Z-rcN` where N > 0:**
```bash
# Get the previous RC tag for the same base version
git tag --sort=-v:refname | grep -E "^vX\.Y\.Z-rc" | head -1
```

**If `new_version` is `X.Y.Z` (final release):**
```bash
# Get the latest RC tag for this version
git tag --sort=-v:refname | grep -E "^vX\.Y\.Z-rc" | head -1
```

Strip the `v` prefix to get `prior_version`.

### 4. Validate against kserve-deps.env

Read `KSERVE_VERSION` from `kserve-deps.env`. It must match the detected prior version tag.
If mismatch, comment on the issue explaining the mismatch and stop.

### 5. Update issue title

This is REQUIRED regardless of the current title. Change the issue title to exactly:
```
release: prepare release v{new_version} (from v{prior_version})
```

### 6. Run bump-version

```bash
make bump-version NEW_VERSION={new_version} PRIOR_VERSION={prior_version}
```

Do NOT run `make test` or any other make targets. Only run `make bump-version`.

### 7. Commit and create PR

Use these exact values:

- **Branch name:** `release/{new_version}`
- **Commit message:** `release: prepare release v{new_version}`
- **PR title:** `release: prepare release v{new_version}`
- **PR body:**
  ```
  Automated version bump from v{prior_version} to v{new_version}.

  Closes #{issue_number}

  ## Next steps
  1. Review and merge this PR
  2. Go to **Actions → Prepare Release (Branch & Tag)** → Run workflow with `version: v{new_version}`
  3. Review and publish the Draft Release

  See [RELEASE_PROCESS_v3.md](release/RELEASE_PROCESS_v3.md) for details.
  ```

## Important

- Do NOT run tests (`make test`, `make test-qpext`, etc.)
- Do NOT modify any files beyond what `make bump-version` changes
- Do NOT skip the issue title update step
- If any step fails, comment on the issue with which step failed and the error message
