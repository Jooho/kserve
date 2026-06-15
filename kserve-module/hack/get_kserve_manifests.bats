#!/usr/bin/env bats

# Tests for hack/get_kserve_manifests.sh
#
# Manifest refs are read from the script's own arrays (source of truth)
# so tests stay in sync when refs change.

SCRIPT_DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")" && pwd)"
SCRIPT="${SCRIPT_DIR}/get_kserve_manifests.sh"

# Extract manifest ref for a component from the script.
# Usage: manifest_ref ODH kserve  →  "opendatahub-io:kserve:release-v0.17:config"
manifest_ref() {
    local platform="$1" key="$2"
    local array_name="${platform}_COMPONENT_MANIFESTS"
    local block
    block=$(sed -n "/^declare -A ${array_name}/,/^)/p" "${SCRIPT}")
    echo "$block" | grep "\\[\"${key}\"\\]" | sed 's/.*="\(.*\)"/\1/'
}

setup() {
    TEST_DST="$(mktemp -d)"
}

teardown() {
    rm -rf "${TEST_DST}"
}

# -------------------------------------------------------
# Default mode (remote clone, ODH)
# -------------------------------------------------------

@test "default mode clones all components from ODH repos" {
    run bash "${SCRIPT}" "${TEST_DST}"
    [ "$status" -eq 0 ]

    for key in kserve modelcontroller wva; do
        local ref
        ref=$(manifest_ref ODH "$key")
        [[ "$output" == *"${ref}"* ]]
    done

    [ -d "${TEST_DST}/kserve" ]
    [ -d "${TEST_DST}/modelcontroller/base" ]
    [ -d "${TEST_DST}/wva" ]
}

# -------------------------------------------------------
# --local-kserve
# -------------------------------------------------------

@test "--local-kserve copies from local repo config/" {
    run bash "${SCRIPT}" "${TEST_DST}" --local-kserve
    [ "$status" -eq 0 ]
    [[ "$output" == *"Using local kserve config (--local-kserve)"* ]]
    [ -d "${TEST_DST}/kserve" ]
    [ ! -d "${TEST_DST}/kserve/kserve-module" ]

    local repo_root
    repo_root="$(cd "${SCRIPT_DIR}/../.." && pwd)"
    cmp -s "${repo_root}/config/overlays/odh/params.env" \
           "${TEST_DST}/kserve/overlays/odh/params.env"
}

# -------------------------------------------------------
# --component= override
# -------------------------------------------------------

@test "override component ref via --modelcontroller=" {
    run bash "${SCRIPT}" "${TEST_DST}" \
        --modelcontroller=opendatahub-io:odh-model-controller:main:config
    [ "$status" -eq 0 ]

    [[ "$output" == *"opendatahub-io:odh-model-controller:main:config"* ]]
    [ -d "${TEST_DST}/modelcontroller" ]
}

@test "invalid component override exits with error" {
    run bash "${SCRIPT}" "${TEST_DST}" \
        --nonexistent=opendatahub-io:kserve:main:config
    [ "$status" -ne 0 ]
    [[ "$output" == *"does not exist"* ]]
}

# -------------------------------------------------------
# RHOAI platform
# -------------------------------------------------------

@test "RHOAI mode uses red-hat-data-services repos" {
    ODH_PLATFORM_TYPE=RHOAI run bash "${SCRIPT}" "${TEST_DST}"
    [ "$status" -eq 0 ]

    for key in kserve modelcontroller wva; do
        local rhoai_ref odh_ref
        rhoai_ref=$(manifest_ref RHOAI "$key")
        odh_ref=$(manifest_ref ODH "$key")
        [[ "$output" == *"${rhoai_ref}"* ]]
        [[ "$output" != *"${odh_ref}"* ]]
    done

    [ -d "${TEST_DST}/kserve" ]
    [ -d "${TEST_DST}/modelcontroller" ]
    [ -d "${TEST_DST}/wva" ]
}
