#!/bin/bash

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]:-$0}")" &>/dev/null && pwd 2>/dev/null)"
source "${SCRIPT_DIR}/../setup/common.sh"

readonly LABEL_APPROVED="cherrypick-approved"
readonly LABEL_COMPLETED="cherrypicked"

DRY_RUN=true
PUSH_BRANCH=false
CREATE_PR=false
DRAFT_PR=false
TARGET_BRANCH=""
NEW_BRANCH=""
STATE_FILE=""
BRANCH_POINT_DATE=""
PR_TARGET_REPO="kserve/kserve"

parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --execute)
                DRY_RUN=false
                shift
                ;;
            --push)
                DRY_RUN=false
                PUSH_BRANCH=true
                shift
                ;;
            --create-pr)
                DRY_RUN=false
                CREATE_PR=true
                shift
                ;;
            --draft)
                DRAFT_PR=true
                shift
                ;;
            --pr-repo)
                if [ -z "${2:-}" ]; then
                    log_error "--pr-repo requires OWNER/REPO argument"
                    exit 1
                fi
                PR_TARGET_REPO="$2"
                shift 2
                ;;
            -h|--help)
                echo "Usage: $0 [OPTIONS]"
                echo ""
                echo "Batch cherry-pick PRs with '$LABEL_APPROVED' label to release branch."
                echo ""
                echo "Options:"
                echo "  (no options)          Dry-run (default)"
                echo "  --execute             Cherry-pick commits locally"
                echo "  --push                Push cherry-pick branch to origin"
                echo "  --create-pr           Create PR from pushed branch"
                echo "  --draft               Create PR as draft (use with --create-pr)"
                echo "  --pr-repo OWNER/REPO  PR target repo (default: kserve/kserve)"
                echo "  -h, --help            Show this help"
                echo ""
                echo "Workflow:"
                echo "  $0                    # 1. Preview cherry-pick targets"
                echo "  $0 --execute          # 2. Cherry-pick locally"
                echo "  $0 --push             # 3. Push branch to origin"
                echo "  $0 --create-pr        # 4. Create PR"
                echo ""
                echo "Note:"
                echo "  - PRs are always fetched from kserve/kserve"
                echo "  - --pr-repo only changes where the PR is created (for testing)"
                echo ""
                echo "Examples:"
                echo "  $0 --pr-repo user/kserve --create-pr  # Test: PR to your fork"
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                echo "Use --help for usage information"
                exit 1
                ;;
        esac
    done

    if [ "$DRAFT_PR" = true ] && [ "$CREATE_PR" != true ]; then
        log_error "--draft requires --create-pr"
        exit 1
    fi

    if [ "$PUSH_BRANCH" = true ] && [ "$CREATE_PR" = true ]; then
        log_error "--push and --create-pr cannot be used together"
        exit 1
    fi
}

validate_environment() {
    check_cli_exist gh jq git
}

detect_target_branch() {
    if [ -z "${KSERVE_VERSION:-}" ]; then
        log_error "KSERVE_VERSION not set"
        exit 1
    fi

    local minor_version=$(echo "$KSERVE_VERSION" | sed -E 's/^v([0-9]+\.[0-9]+).*/\1/')
    TARGET_BRANCH="release-${minor_version}"
    log_info "Detected target branch: $TARGET_BRANCH from version $KSERVE_VERSION"
}

load_state() {
    if [ ! -f "$STATE_FILE" ] || [ ! -s "$STATE_FILE" ]; then
        return 1
    fi

    log_info "Found pending state: $(wc -l < "$STATE_FILE") PR(s) remaining"

    # Find and switch to the cherry-pick branch
    detect_cherry_pick_branch
    local current_branch=$(git branch --show-current)
    if [[ "$current_branch" != "$NEW_BRANCH" ]]; then
        log_info "Switching to $NEW_BRANCH..."
        git checkout "$NEW_BRANCH"
    fi

    if git rev-parse CHERRY_PICK_HEAD >/dev/null 2>&1; then
        log_error "Cherry-pick in progress. Resolve conflicts first:"
        echo -e "  ${GREEN}git add <files>${RESET}"
        echo -e "  ${GREEN}git cherry-pick --continue${RESET}"
        echo -e "  Then re-run: ${GREEN}$0 --execute${RESET}"
        exit 1
    fi

    local last_pr=$(git log -1 --format="%s" 2>/dev/null | grep -oE '#[0-9]+' | head -1 | tr -d '#')
    local first_pr=$(head -n 1 "$STATE_FILE" | awk '{print $2}')

    if [ -n "$last_pr" ] && [ "$last_pr" == "$first_pr" ]; then
        log_success "Last PR #$first_pr already completed"
        tail -n +2 "$STATE_FILE" > "${STATE_FILE}.tmp" && mv "${STATE_FILE}.tmp" "$STATE_FILE"
    fi

    # All PRs already done
    if [ ! -s "$STATE_FILE" ]; then
        log_success "All PRs already completed"
        rm -f "$STATE_FILE"
        return 1
    fi

    return 0
}

fetch_approved_prs() {
    local branch_point_date="$1"
    log_info "Fetching PRs..."

    local jq_filter='
        [.[]
        | select(.labels | map(.name) | contains([$completed_label]) | not)
        | select(.mergedAt > $branch_date)]
        | reverse
        | .[]
        | "\(.mergeCommit.oid) \(.number) \(.title)"
    '

    gh pr list --repo kserve/kserve --state merged --base master --label "$LABEL_APPROVED" --limit 100 \
        --json number,mergeCommit,title,labels,mergedAt | \
        jq --arg branch_date "$branch_point_date" --arg completed_label "$LABEL_COMPLETED" -r "$jq_filter" > "$STATE_FILE"

    if [ ! -s "$STATE_FILE" ]; then
        log_warning "No PRs found"
        [ "$DRY_RUN" = true ] && rm -f "$STATE_FILE"
        return 1
    fi
    log_success "Found $(wc -l < "$STATE_FILE") PR(s)"
    return 0
}

cherry_pick_loop() {
    log_info "Starting cherry-pick. Remaining: $(wc -l < "$STATE_FILE")"
    echo ""

    while IFS= read -r line; do
        local commit_sha=$(echo "$line" | awk '{print $1}')
        local pr_number=$(echo "$line" | awk '{print $2}')
        local pr_title=$(echo "$line" | cut -d' ' -f3-)

        echo -e "${GREEN}Cherry-picking PR #$pr_number: $pr_title${RESET}"
        echo "Commit: $commit_sha"

        if git cherry-pick "$commit_sha"; then
            log_success "✓ PR #$pr_number done"
            tail -n +2 "$STATE_FILE" > "${STATE_FILE}.tmp" && mv "${STATE_FILE}.tmp" "$STATE_FILE"
            echo ""
        else
            log_error "✗ Conflict on PR #$pr_number"
            echo ""
            echo -e "${YELLOW}=== CONFLICT ===${RESET}"
            echo -e "1. Resolve conflicts and: ${GREEN}git add <files>${RESET}"
            echo -e "2. ${GREEN}git cherry-pick --continue${RESET}"
            echo -e "3. ${GREEN}$0 --execute${RESET}"
            echo ""
            echo -e "To skip: ${GREEN}git cherry-pick --abort${RESET}"
            echo -e "Then: ${GREEN}tail -n +2 \"$STATE_FILE\" > \"${STATE_FILE}.tmp\" && mv \"${STATE_FILE}.tmp\" \"$STATE_FILE\"${RESET}"
            echo -e "Run: ${GREEN}$0 --execute${RESET}"
            exit 1
        fi
    done < "$STATE_FILE"

    log_success "All done!"
    rm -f "$STATE_FILE"
}

push_branch() {
    local origin_url=$(git remote get-url origin 2>/dev/null || echo "")
    if [[ "$origin_url" =~ kserve/kserve ]]; then
        log_error "Cannot push to kserve/kserve directly"
        log_error "Please use a fork. Current origin: $origin_url"
        exit 1
    fi

    local commit_count=$(git rev-list --count "upstream/$TARGET_BRANCH..$NEW_BRANCH")

    if [ "$commit_count" -eq 0 ]; then
        log_warning "No commits to push"
        return 0
    fi

    log_info "Pushing branch $NEW_BRANCH to origin ($commit_count commits)..."
    git push origin "$NEW_BRANCH"
    log_success "✓ Branch pushed to origin"

    echo ""
    echo -e "${BLUE}Next (create PR):${RESET}"
    if [ "$PR_TARGET_REPO" != "kserve/kserve" ]; then
        echo -e "  ${GREEN}$0 --pr-repo $PR_TARGET_REPO --create-pr${RESET}"
    else
        echo -e "  ${GREEN}$0 --create-pr${RESET}"
    fi
}

create_pull_request() {
    local origin_url=$(git remote get-url origin 2>/dev/null || echo "")
    if [[ "$origin_url" =~ kserve/kserve ]]; then
        log_error "Cannot create PR from kserve/kserve directly"
        log_error "Please use a fork. Current origin: $origin_url"
        exit 1
    fi

    # Extract owner from origin URL (e.g., git@github.com:Jooho/kserve.git or https://github.com/Jooho/kserve.git)
    local owner=""
    if [[ "$origin_url" =~ github\.com[:/]([^/]+)/[^/]+(.git)?$ ]]; then
        owner="${BASH_REMATCH[1]}"
    else
        log_error "Cannot extract owner from origin URL: $origin_url"
        exit 1
    fi

    local commit_count=$(git rev-list --count "upstream/$TARGET_BRANCH..$NEW_BRANCH")

    if [ "$commit_count" -eq 0 ]; then
        log_warning "No commits for PR"
        return 0
    fi

    local pr_body="Cherry-picks to \`$TARGET_BRANCH\`:

$(git log --oneline "upstream/$TARGET_BRANCH..$NEW_BRANCH" | sed 's/^/* /')

PRs: $(git log --oneline "upstream/$TARGET_BRANCH..$NEW_BRANCH" | grep -oE '#[0-9]+' | sort -u | tr '\n' ' ')"

    local draft_flag=""
    [ "$DRAFT_PR" = true ] && draft_flag="--draft"

    log_info "Creating PR to $PR_TARGET_REPO ($commit_count commits)... $draft_flag"
    gh pr create --repo "$PR_TARGET_REPO" --base "$TARGET_BRANCH" --head "$owner:$NEW_BRANCH" \
        --title "release: cherry-pick batch commits to $TARGET_BRANCH" --body "$pr_body" $draft_flag

    log_success "✓ PR created to $PR_TARGET_REPO! $draft_flag"
}

batch_cleanup() {
    local exit_code=$?
    [ "$DRY_RUN" = true ] && [ -f "${STATE_FILE:-}" ] && rm -f "$STATE_FILE"
    if [ $exit_code -ne 0 ]; then
        log_error "Failed (exit $exit_code)"
        [ -f "${STATE_FILE:-}" ] && log_info "State: $STATE_FILE"
    fi
    cleanup
}

detect_cherry_pick_branch() {
    local branches
    branches=$(git branch --list 'cherry-pick-batch-*' --format='%(refname:short)' | sort -r)
    local count=$(echo "$branches" | grep -c . 2>/dev/null || true)

    if [ "$count" -eq 0 ]; then
        log_error "No cherry-pick branch found"
        log_error "Run '--execute' first to cherry-pick commits"
        exit 1
    fi

    NEW_BRANCH=$(echo "$branches" | head -1)

    if [ "$count" -gt 1 ]; then
        log_warning "Multiple cherry-pick branches found, using latest: $NEW_BRANCH"
        log_info "To clean up old branches:"
        echo "$branches" | tail -n +2 | while read -r b; do
            echo "  git branch -D $b"
        done
        echo ""
    fi

    log_info "Detected branch: $NEW_BRANCH"
}

setup_upstream() {
    local expected_url="https://github.com/kserve/kserve.git"

    if ! git remote get-url upstream >/dev/null 2>&1; then
        log_info "Adding upstream remote..."
        git remote add upstream "$expected_url"
    else
        local current_url=$(git remote get-url upstream)
        if [[ "$current_url" != "$expected_url" ]]; then
            log_warning "upstream remote exists but points to: $current_url"
            log_warning "Expected: $expected_url"
            log_warning "Run: git remote set-url upstream $expected_url"
        fi
    fi

    log_info "Fetching from upstream..."
    local fetch_output
    if ! fetch_output=$(git fetch upstream "$@" 2>&1); then
        log_error "Fetch failed"
        [ -n "$fetch_output" ] && echo "$fetch_output"
        exit 1
    fi
    echo "$fetch_output" | grep -v "^From" || true
}

main() {
    parse_arguments "$@"
    validate_environment
    detect_target_branch

    # --push: push current cherry-pick branch to origin
    if [ "$PUSH_BRANCH" = true ]; then
        detect_cherry_pick_branch
        setup_upstream "$TARGET_BRANCH"
        push_branch
        return
    fi

    # --create-pr: create PR from current cherry-pick branch
    if [ "$CREATE_PR" = true ]; then
        detect_cherry_pick_branch
        setup_upstream "$TARGET_BRANCH"
        create_pull_request
        return
    fi

    # dry-run or --execute: fetch PRs and cherry-pick
    NEW_BRANCH="cherry-pick-batch-$(date +%Y%m%d-%H%M%S)"
    STATE_FILE="/tmp/cherry-pick-state-$TARGET_BRANCH.txt"

    [ "$DRY_RUN" = false ] && log_info "Target: $TARGET_BRANCH, Branch: $NEW_BRANCH"

    setup_upstream "$TARGET_BRANCH" master

    MERGE_BASE=$(git merge-base upstream/master "upstream/$TARGET_BRANCH" 2>&1) || {
        log_error "Merge base not found"; exit 1
    }

    BRANCH_POINT_DATE=$(git log -1 --format="%aI" "$MERGE_BASE")
    log_info "Branch point: $BRANCH_POINT_DATE"
    echo ""

    local resuming=false
    [ "$DRY_RUN" = false ] && load_state && resuming=true

    if [ "$resuming" = false ]; then
        [ "$DRY_RUN" = true ] && STATE_FILE="/tmp/cherry-pick-dry-run-$$.txt"
        fetch_approved_prs "$BRANCH_POINT_DATE" || exit 0

        if [ "$DRY_RUN" = true ]; then
            echo -e "${BLUE}=== DRY RUN ===${RESET}"
            echo "From: master → To: $TARGET_BRANCH"
            echo "Branch point: $BRANCH_POINT_DATE"
            echo "New branch: $NEW_BRANCH"
            echo ""
            echo "PRs ($(wc -l < "$STATE_FILE")):"

            local count=1
            while IFS= read -r line; do
                echo -e "${GREEN}$count.${RESET} PR #$(echo "$line" | awk '{print $2}'): $(echo "$line" | cut -d' ' -f3-)"
                count=$((count + 1))
            done < "$STATE_FILE"

            echo ""
            echo -e "${BLUE}Next:${RESET}"
            echo -e "  ${GREEN}$0 --execute${RESET}"
            echo ""
            echo "Manual:"
            echo "  git fetch upstream $TARGET_BRANCH && git checkout -b $NEW_BRANCH upstream/$TARGET_BRANCH"
            while IFS= read -r line; do
                echo "  git cherry-pick $(echo "$line" | awk '{print $1}')  # PR #$(echo "$line" | awk '{print $2}')"
            done < "$STATE_FILE"

            rm -f "$STATE_FILE"
            exit 0
        fi

        if ! git diff-index --quiet HEAD --; then
            log_error "Working directory has uncommitted changes"
            log_error "Please commit or stash your changes first"
            git status --short
            exit 1
        fi

        if git rev-parse --verify "$NEW_BRANCH" >/dev/null 2>&1; then
            log_error "Branch $NEW_BRANCH already exists"
            log_error "Please delete it first: git branch -D $NEW_BRANCH"
            exit 1
        fi

        git checkout -b "$NEW_BRANCH" "upstream/$TARGET_BRANCH"
        log_success "Created: $NEW_BRANCH"
        echo ""
    fi

    cherry_pick_loop

    log_info "Switching back to master..."
    git checkout master
    log_success "✓ Back on master (cherry-pick branch: $NEW_BRANCH)"

    echo ""
    echo -e "${BLUE}Next (push to origin):${RESET}"
    if [ "$PR_TARGET_REPO" != "kserve/kserve" ]; then
        echo -e "  ${GREEN}$0 --pr-repo $PR_TARGET_REPO --push${RESET}"
    else
        echo -e "  ${GREEN}$0 --push${RESET}"
    fi
}

trap batch_cleanup EXIT
main "$@"
