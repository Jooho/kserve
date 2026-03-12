#!/usr/bin/env bash
# Helper script to bump the KServe version
# Usage:
#   ./hack/release/prepare-for-release.sh <prior_version> <new_version>
#   ./hack/release/prepare-for-release.sh <prior_version> <new_version> --execute
#   ./hack/release/prepare-for-release.sh <prior_version> <new_version> --create-pr

# KServe standard pattern
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]:-$0}")" &>/dev/null && pwd 2>/dev/null)"
source "${SCRIPT_DIR}/../setup/common.sh"

# Global variables
PRIOR_VERSION=""
NEW_VERSION=""
DRY_RUN=true
CREATE_PR=false
DRAFT_PR=true
SED_INPLACE=()

# ============================================================
# Argument parsing
# ============================================================

parse_arguments() {
    # Positional arguments (backward compatibility)
    if [[ $# -ge 2 ]] && [[ "$1" != --* ]]; then
        PRIOR_VERSION="$1"
        NEW_VERSION="$2"
        shift 2
    fi

    # Parse flags
    while [[ $# -gt 0 ]]; do
        case $1 in
            --execute)
                DRY_RUN=false
                shift
                ;;
            --create-pr)
                DRY_RUN=false
                CREATE_PR=true
                shift
                ;;
            --no-draft)
                DRAFT_PR=false
                shift
                ;;
            -h|--help)
                cat <<EOF
Usage: $0 <prior_version> <new_version> [OPTIONS]

Bump KServe version across charts, Python packages, and install manifests.

Arguments:
  prior_version         Current version (e.g., 0.16.0)
  new_version           Target version (e.g., 0.17.0-rc0)

Options:
  (no options)          Dry-run mode (default) - validate and show plan
  --execute             Execute version update (no git operations)
  --create-pr           Execute + create draft PR to master (default)
  --no-draft            Create regular PR (use with --create-pr)
  -h, --help            Show this help

Examples:
  # Dry-run (validate and show plan)
  $0 0.16.0 0.17.0-rc0

  # Execute version update
  $0 0.16.0 0.17.0-rc0 --execute

  # Execute + create draft PR
  $0 0.16.0 0.17.0-rc0 --create-pr

  # Execute + create regular PR
  $0 0.16.0 0.17.0-rc0 --create-pr --no-draft

Workflow:
  1. Run dry-run to validate versions
  2. Execute to update files locally
  3. Review changes (git diff)
  4. Create PR or commit manually
EOF
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                echo "Use --help for usage information"
                exit 1
                ;;
        esac
    done

    # Validate required arguments
    if [[ -z "$PRIOR_VERSION" ]] || [[ -z "$NEW_VERSION" ]]; then
        log_error "Usage: $0 <prior_version> <new_version> [--execute|--create-pr] [--no-draft]"
        exit 1
    fi

    # Validate --no-draft requires --create-pr
    if [ "$DRAFT_PR" = false ] && [ "$CREATE_PR" != true ]; then
        log_error "--no-draft requires --create-pr"
        exit 1
    fi
}

# ============================================================
# Validation
# ============================================================

validate_environment() {
    if [ "$CREATE_PR" = true ]; then
        check_cli_exist gh
    fi
}

detect_os_and_sed() {
    if [[ "$OSTYPE" == "darwin"* ]]; then
        SED_INPLACE=(-i '')
    else
        SED_INPLACE=(-i)
    fi
}

validate_version_format() {
    local pattern="^[0-9]+\.[0-9]{2}\.[0-9]+(-[a-zA-Z0-9]{1,3})?$"

    if [[ ! ${NEW_VERSION} =~ $pattern ]]; then
        log_error "New version does not match the required pattern"
        echo "  Expected: X.YY.Z or X.YY.Z-rcN (e.g., 0.17.0 or 0.17.0-rc0)"
        echo "  Got: $NEW_VERSION"
        exit 1
    fi
}

validate_version_ordering() {
    # Check if versions are the same
    if [ "${PRIOR_VERSION}" == "${NEW_VERSION}" ]; then
        log_error "Versions cannot be the same"
        exit 1
    fi

    # Extract base versions
    local p=$(echo ${PRIOR_VERSION} | cut -d '-' -f 1)
    local n=$(echo ${NEW_VERSION} | cut -d '-' -f 1)

    # Not allow same version to be update to rc1, e.g. 0.14.0 to 0.14.0-rc1
    if [[ ${PRIOR_VERSION} != *"-"* ]] && [[ ${NEW_VERSION} == *"-"* ]]; then
        if [[ ${PRIOR_VERSION} == ${n} ]]; then
            log_error "New version must be greater than the prior version"
            echo "  Cannot update from final release to RC of same version"
            echo "  Prior: $PRIOR_VERSION, New: $NEW_VERSION"
            exit 1
        fi
    fi

    # Check version ordering
    if [[ $(printf '%s\n' "${PRIOR_VERSION}" "${NEW_VERSION}" | sort -V | head -n1) != "${PRIOR_VERSION}" ]]; then
        # Handle update from rc to final version, e.g. 0.14.0-rc1 to 0.14.0
        if [[ ${PRIOR_VERSION} == *"-"* ]] && [[ ${NEW_VERSION} != *"-"* ]]; then
            # Allow update from rc to final version
            :
        else
            log_error "New version must be greater than the prior version"
            echo "  Prior: $PRIOR_VERSION"
            echo "  New: $NEW_VERSION"
            exit 1
        fi
    fi
}

# ============================================================
# Version update operations
# ============================================================

update_versions() {
    log_info "Updating version strings..."

    # Normalized versions for chart badges (double dashes)
    local pversion=""
    local nversion=""

    if [[ ${NEW_VERSION} == *"-"* ]]; then
        nversion=$(echo ${NEW_VERSION} | sed 's/-/--/g')
    else
        nversion=${NEW_VERSION}
    fi

    if [[ ${PRIOR_VERSION} == *"-"* ]]; then
        pversion=$(echo ${PRIOR_VERSION} | sed 's/-/--/g')
    else
        pversion=${PRIOR_VERSION}
    fi

    # Update charts
    log_info "Updating charts..."
    for readmeFile in `find charts -name README.md`; do
        echo "  - ${readmeFile}"
        sed "${SED_INPLACE[@]}" \
            -e "s/Version-v${pversion}/Version-v${nversion}/g" \
            -e "s/\bv${PRIOR_VERSION}\b/v${NEW_VERSION}/g" \
            ${readmeFile}
    done

    for yaml in `find charts \( -name "Chart.yaml" -o -name "values.yaml" \)`; do
        if [ -s "${yaml}" ]; then
            echo "  - ${yaml}"
            sed "${SED_INPLACE[@]}" "s/\bv${PRIOR_VERSION}\b/v${NEW_VERSION}/g" ${yaml}
        fi
    done

    # Update generate-install.sh RELEASES array
    log_info "Updating hack/release/generate-install.sh..."
    if grep -q "\"v${NEW_VERSION}\"" hack/release/generate-install.sh; then
        log_warning "Version v${NEW_VERSION} already exists in generate-install.sh, skipping..."
    else
        sed "${SED_INPLACE[@]}" "/\"v${PRIOR_VERSION}\"/a \\
    \"v${NEW_VERSION}\"" hack/release/generate-install.sh
    fi

    # Update kserve-deps.env
    log_info "Updating kserve-deps.env..."
    sed "${SED_INPLACE[@]}" "s/KSERVE_VERSION=v${PRIOR_VERSION}/KSERVE_VERSION=v${NEW_VERSION}/g" kserve-deps.env
    sed "${SED_INPLACE[@]}" "s/\bv${PRIOR_VERSION}\b/v${NEW_VERSION}/g" charts/_common/common-sections.yaml

    # Update python/kserve and docs versions
    log_info "Updating python/kserve and docs versions..."
    local new_no_dash_version=$(echo ${NEW_VERSION} | sed 's/-//g')
    local prior_no_dash_version=$(echo ${PRIOR_VERSION} | sed 's/-//g')

    echo "${new_no_dash_version}" > python/VERSION

    for file in $(find python docs \( -name 'pyproject.toml' -o -name 'uv.lock' \)); do
        echo "  - ${file}"
        if [[ ${file} == *"uv.lock" ]]; then
            sed "${SED_INPLACE[@]}" "/name = \"kserve\"/{N;s|${prior_no_dash_version}|${new_no_dash_version}|;}" "${file}"
        else
            sed "${SED_INPLACE[@]}" \
                -e "s|version = \"${prior_no_dash_version}\"|version = \"${new_no_dash_version}\"|" \
                -e "s|kserve-storage==${prior_no_dash_version}|kserve-storage==${new_no_dash_version}|g" \
                "${file}"
        fi
    done

    # Update Python dependency lock files
    log_info "Updating Python dependency lock files (uv-lock)..."
    make uv-lock
    if [ $? -ne 0 ]; then
        log_error "Failed to update uv.lock files"
        exit 1
    fi

    # Run precommit checks
    log_info "Running precommit checks (lint, format, vet)..."
    make precommit
    if [ $? -ne 0 ]; then
        log_error "Precommit checks failed. Please fix the issues and re-run."
        exit 1
    fi

    # Generate install manifests
    log_info "Generating install manifests..."
    ./hack/release/generate-install.sh "v${NEW_VERSION}"
    if [ $? -ne 0 ]; then
        log_error "Failed to generate install manifests"
        exit 1
    fi

    log_success "✓ All version updates completed successfully!"
}

# ============================================================
# Dry-run display
# ============================================================

show_dry_run_plan() {
    echo ""
    echo "=================================================="
    echo -e "${BLUE}DRY-RUN MODE - Validation passed${RESET}"
    echo "=================================================="
    echo ""
    echo "📋 Version Update Plan:"
    echo "  Prior Version:  $PRIOR_VERSION"
    echo "  New Version:    $NEW_VERSION"
    echo ""
    echo "📝 Files to be updated:"
    echo "  - charts/**/README.md (badges + versions)"
    echo "  - charts/**/Chart.yaml, values.yaml"
    echo "  - hack/release/generate-install.sh (RELEASES array)"
    echo "  - kserve-deps.env (KSERVE_VERSION)"
    echo "  - charts/_common/common-sections.yaml"
    echo "  - python/VERSION"
    echo "  - python/*/pyproject.toml, docs/pyproject.toml"
    echo "  - python/**/uv.lock files"
    echo "  - Install manifests (install/v${NEW_VERSION}/)"
    echo ""
    echo "🔨 Steps to be executed:"
    echo "  1. Update version strings in files"
    echo "  2. Run make uv-lock (Python dependencies)"
    echo "  3. Run make precommit (lint, format, vet)"
    echo "  4. Generate install manifests"
    echo ""
    echo "=================================================="
    log_success "All validations passed!"
    echo "=================================================="
    echo ""
    echo "🚀 To execute this version bump:"
    echo "   $0 $PRIOR_VERSION $NEW_VERSION --execute"
    echo ""
    echo "🤖 use --create-pr for automation (creates draft PR by default):"
    echo "   $0 $PRIOR_VERSION $NEW_VERSION --create-pr"
    echo ""
    echo "   Note: Draft PR is created by default."
    echo "   Review at https://github.com/kserve/kserve/pulls and mark 'Ready for review'"
    echo ""
    echo "📝 Or use manual steps:"
    echo "   # Review changes"
    echo "   git status"
    echo "   git diff"
    echo ""
    echo "   # Commit and push"
    echo "   git checkout -b release/${NEW_VERSION}"
    echo "   git add ."
    echo "   git commit -S -s -m \"release: prepare release v${NEW_VERSION}\""
    echo "   git push -u origin release/${NEW_VERSION}"
    echo ""
    echo "   # Create PR"
    echo "   gh pr create --title \"release: prepare release v${NEW_VERSION}\" --label release-tracking --draft"
    echo ""
    echo ""
}

# ============================================================
# PR creation
# ============================================================

create_pull_request() {
    local branch_name="release/${NEW_VERSION}"
    local current_branch=$(git branch --show-current)

    # Check for uncommitted changes
    if ! git diff-index --quiet HEAD --; then
        log_error "Working directory has uncommitted changes"
        echo ""
        echo "Please commit or stash your changes first:"
        git status --short
        exit 1
    fi

    # Check if branch already exists
    if git rev-parse --verify "$branch_name" >/dev/null 2>&1; then
        log_error "Branch $branch_name already exists"
        echo ""
        echo "Please delete it first:"
        echo "  git branch -D $branch_name"
        exit 1
    fi

    log_info "Creating branch: $branch_name"
    git checkout -b "$branch_name"

    log_info "Staging changes..."
    git add .

    log_info "Committing changes..."
    git commit -S -s -m "release: prepare release v${NEW_VERSION}"

    log_info "Pushing to origin..."
    git push -u origin "$branch_name"

    # Extract owner from origin URL
    local origin_url=$(git remote get-url origin 2>/dev/null || echo "")
    local owner=""

    if [[ "$origin_url" =~ github\.com[:/]([^/]+)/[^/]+(.git)?$ ]]; then
        owner="${BASH_REMATCH[1]}"
    else
        log_error "Cannot extract owner from origin URL: $origin_url"
        exit 1
    fi

    local pr_body="Prepare release v${NEW_VERSION}

## Changes
- Update version from v${PRIOR_VERSION} to v${NEW_VERSION}
- Update charts, Python packages, and documentation
- Regenerate install manifests

## Files updated
$(git diff --name-only HEAD~1 | sed 's/^/- /')

---
🤖 Generated with automation script"

    local draft_flag=""
    [ "$DRAFT_PR" = true ] && draft_flag="--draft"

    log_info "Creating PR to kserve/kserve... $draft_flag"
    gh pr create --repo kserve/kserve --base master --head "$owner:$branch_name" \
        --title "release: prepare release v${NEW_VERSION}" \
        --body "$pr_body" \
        --label "release-tracking" \
        $draft_flag

    log_success "✓ PR created successfully!"
    echo ""
    echo "Next steps:"
    echo "  1. Review PR on GitHub"
    echo "  2. Add 'cherrypick-approved' label if needed"
    echo "  3. Merge to master"
}

# ============================================================
# Main
# ============================================================

main() {
    # Check if running from repository root
    if [[ ! -f "kserve-deps.env" ]]; then
        log_error "This script must be run from the repository root directory"
        exit 1
    fi

    # Parse arguments
    parse_arguments "$@"

    # Validate environment (only for PR creation)
    validate_environment

    # Detect OS and set sed flags
    detect_os_and_sed

    # Validate version format
    validate_version_format

    # Validate version ordering
    validate_version_ordering

    # Dry-run mode
    if [ "$DRY_RUN" = true ]; then
        show_dry_run_plan
        exit 0
    fi

    # Execute version update
    update_versions

    # Show next steps if not creating PR
    if [ "$CREATE_PR" != true ]; then
        echo ""
        log_success "✓ Version update completed!"
        echo ""
        echo "Next steps:"
        echo "  1. Review changes: git status && git diff"
        echo "  2. Create branch and commit:"
        echo "     git checkout -b release/${NEW_VERSION}"
        echo "     git add ."
        echo "     git commit -S -s -m \"release: prepare release v${NEW_VERSION}\""
        echo "  3. Push and create PR:"
        echo "     git push -u origin release/${NEW_VERSION}"
        echo "     gh pr create --title \"release: prepare release v${NEW_VERSION}\" --label release-tracking --draft"
        echo ""
        echo "Or run with --create-pr to automate PR creation:"
        echo "  $0 $PRIOR_VERSION $NEW_VERSION --create-pr"
        echo ""
        exit 0
    fi

    # Create PR
    create_pull_request
}

main "$@"
