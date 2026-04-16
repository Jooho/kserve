# Release Agent

Automates the version bump step of the KServe release process.

## Trigger

Assigned to an issue created from the **Release** issue template.

## Steps

### 1. Parse issue body

Extract `new_version` from the issue body (under "### New Version").

### 2. Validate version format

`new_version` must match `X.Y.Z` or `X.Y.Z-rcN` (where N is 0-2).
If invalid, comment on the issue with the error and stop.

### 3. Detect prior version

Run:
```bash
git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+' | head -1
```

This gives the latest release tag (e.g., `v0.17.0`). Strip the `v` prefix to get `prior_version`.

### 4. Validate against kserve-deps.env

Read `KSERVE_VERSION` from `kserve-deps.env`. It must match the detected prior version tag.
If mismatch, comment on the issue with the error and stop.

Example:
- Latest tag: `v0.17.0`
- `KSERVE_VERSION=v0.17.0` → match, proceed
- `KSERVE_VERSION=v0.16.0` → mismatch, stop

### 5. Update issue title

Change the issue title to:
```
release: prepare release v{new_version} (from v{prior_version})
```

### 6. Run bump-version

```bash
make bump-version NEW_VERSION={new_version} PRIOR_VERSION={prior_version}
```

This runs `hack/release/prepare-for-release.sh` which updates version strings across the repo and generates install manifests.

### 7. Commit and create PR

- Branch name: `release/{new_version}`
- Commit message: `release: prepare release v{new_version}`
- PR title: `release: prepare release v{new_version}`
- PR body:
  ```
  Automated version bump from v{prior_version} to v{new_version}.

  Triggered by #{issue_number}.

  ## Next steps
  1. Review and merge this PR
  2. Go to Actions → Prepare Release (Branch & Tag) → Run workflow
  3. Review and publish the Draft Release

  See [RELEASE_PROCESS_v3.md](../release/RELEASE_PROCESS_v3.md) for details.
  ```
- Link the PR to the issue using `Closes #{issue_number}`

## Error handling

If any step fails, comment on the issue with:
- Which step failed
- The error message
- Do not create a PR with partial changes
