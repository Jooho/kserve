#!/usr/bin/env bash
# Generate release notes for KServe releases
# Usage: generate-release-notes.sh <version>

set -eo pipefail

VERSION=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            echo "Usage: $0 <version>"
            echo ""
            echo "Generate release notes for KServe releases"
            echo ""
            echo "Arguments:"
            echo "  version               Release version (e.g., v0.17.0-rc1)"
            echo ""
            echo "Examples:"
            echo "  $0 v0.17.0-rc1"
            exit 0
            ;;
        *)
            if [[ -z "$VERSION" ]]; then
                VERSION="$1"
            else
                echo "Unknown option: $1" >&2
                exit 1
            fi
            shift
            ;;
    esac
done

if [[ -z "$VERSION" ]]; then
    echo "Error: Version is required" >&2
    echo "Usage: $0 <version>" >&2
    echo "Run '$0 --help' for more information" >&2
    exit 1
fi

# Determine previous tag for comparison
determine_prev_tag() {
    local version=$1
    local prev_tag=""

    if [[ "$version" == *"-rc"* ]]; then
        # RC release
        if [[ "$version" == *"-rc0" ]]; then
            # RC0: compare with previous major/minor final release
            local major_minor=$(echo "$version" | grep -oE 'v[0-9]+\.[0-9]+')
            local prev_major=$(echo "$major_minor" | cut -d. -f1)
            local prev_minor=$(echo "$major_minor" | cut -d. -f2 | awk '{print $1-1}')
            local prev_major_minor="${prev_major}.${prev_minor}"

            prev_tag=$(git tag -l "${prev_major_minor}.*" | grep -v 'rc' | sort -V | tail -1)
            echo "  RC0: comparing with previous version $prev_major_minor" >&2
        else
            # RC1+: compare with previous tag (any RC)
            local base_version=$(echo "$version" | sed 's/-rc.*//')
            prev_tag=$(git tag -l "${base_version}*" | sort -V | while read tag; do
                if [[ "$tag" < "$version" ]]; then
                    echo "$tag"
                fi
            done | tail -1)
            echo "  RC1+: comparing with previous RC" >&2
        fi
    else
        # Final release: compare with previous major/minor final release
        local major_minor=$(echo "$version" | grep -oE 'v[0-9]+\.[0-9]+')
        local prev_major=$(echo "$major_minor" | cut -d. -f1)
        local prev_minor=$(echo "$major_minor" | cut -d. -f2 | awk '{print $1-1}')
        local prev_major_minor="${prev_major}.${prev_minor}"

        prev_tag=$(git tag -l "${prev_major_minor}.*" | grep -v 'rc' | sort -V | tail -1)
        echo "  Final: comparing with previous version $prev_major_minor" >&2
    fi

    echo "$prev_tag"
}

# Generate changelog from commits
generate_changelog() {
    local prev_tag=$1
    local version=$2

    if [[ -z "$prev_tag" ]]; then
        echo "- Initial release"
        return
    fi

    # Parse commits and filter out noise
    git log ${prev_tag}..${version} --oneline \
        | grep -v -E '(Revert "|^\[TEST\]|release: cherry-pick batch)' \
        | sed 's/^[a-f0-9]* /- /'
}

# Main

# Validate that the version tag exists
if ! git rev-parse --verify "$VERSION" >/dev/null 2>&1; then
    echo "Error: Tag $VERSION does not exist" >&2
    echo "" >&2
    echo "Please create the tag first:" >&2
    echo "  git tag $VERSION" >&2
    echo "  git push upstream $VERSION" >&2
    echo "" >&2
    echo "Or run create-release.sh to create the release:" >&2
    echo "  ./hack/release/create-release.sh $VERSION" >&2
    exit 1
fi

# Generate release notes
echo "Generating release notes for $VERSION..." >&2

PREV_TAG=$(determine_prev_tag "$VERSION")
echo "Previous tag: ${PREV_TAG:-<none>}" >&2
echo "" >&2

CHANGELOG=$(generate_changelog "$PREV_TAG" "$VERSION")

# Output release notes
cat <<EOF
## Installation

- [Installation Guide](https://kserve.github.io/website/docs/next/getting-started/quickstart-guide)

## What's Changed

${CHANGELOG}

**Full Changelog**: https://github.com/kserve/kserve/compare/${PREV_TAG}...${VERSION}
EOF
