#!/usr/bin/env bats

# Tests for hack/get_kserve_manifests.sh

SCRIPT_DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")" && pwd)"
SCRIPT="${SCRIPT_DIR}/get_kserve_manifests.sh"

setup() {
    TEST_DST="$(mktemp -d)"
}

teardown() {
    rm -rf "${TEST_DST}"
}

# -------------------------------------------------------
# --help
# -------------------------------------------------------

@test "--help exits 0 and shows usage" {
    run bash "${SCRIPT}" --help
    [ "$status" -eq 0 ]
    [[ "$output" == *"Usage:"* ]]
    [[ "$output" == *"--local-kserve"* ]]
    [[ "$output" == *"--help"* ]]
    [[ "$output" == *"ODH_PLATFORM_TYPE"* ]]
}

# -------------------------------------------------------
# Default mode (remote clone, ODH)
# -------------------------------------------------------

@test "default mode clones kserve and modelcontroller from ODH repos" {
    run bash "${SCRIPT}" "${TEST_DST}"
    [ "$status" -eq 0 ]

    # Verify ODH repos used in actual variable logs
    [[ "$output" == *"opendatahub-io:kserve:"* ]]
    [[ "$output" == *"opendatahub-io:odh-model-controller:"* ]]

    # Verify output structure
    [ -d "${TEST_DST}/kserve" ]
    [ -d "${TEST_DST}/modelcontroller/base" ]
}

@test "default mode uses odh-stable images (not latest)" {
    run bash "${SCRIPT}" "${TEST_DST}"
    [ "$status" -eq 0 ]

    local params="${TEST_DST}/kserve/overlays/odh/params.env"
    [ -f "${params}" ]
    grep -q "kserve-controller=.*odh-stable" "${params}"
    ! grep -q "kserve-controller=.*:latest" "${params}"
}

# -------------------------------------------------------
# --local-kserve
# -------------------------------------------------------

@test "--local-kserve copies from local repo config/" {
    run bash "${SCRIPT}" "${TEST_DST}" --local-kserve
    [ "$status" -eq 0 ]
    [[ "$output" == *"Using local kserve config (--local-kserve)"* ]]
    [ -d "${TEST_DST}/kserve" ]

    # Verify local config files match output (byte-identical)
    local repo_root
    repo_root="$(cd "${SCRIPT_DIR}/../.." && pwd)"
    cmp -s "${repo_root}/config/overlays/odh/params.env" \
           "${TEST_DST}/kserve/overlays/odh/params.env"

    # Verify modelcontroller still clones from remote (not affected)
    [[ "$output" == *"opendatahub-io:odh-model-controller:"* ]]
}

@test "--local-kserve excludes kserve-module from copied config" {
    run bash "${SCRIPT}" "${TEST_DST}" --local-kserve
    [ "$status" -eq 0 ]
    [ ! -d "${TEST_DST}/kserve/kserve-module" ]
}

# -------------------------------------------------------
# --component= override
# -------------------------------------------------------

@test "override modelcontroller ref via --modelcontroller=" {
    run bash "${SCRIPT}" "${TEST_DST}" \
        --modelcontroller=opendatahub-io:odh-model-controller:main:config
    [ "$status" -eq 0 ]

    # Verify override applied in actual variable logs
    [[ "$output" == *"opendatahub-io:odh-model-controller:main:config"* ]]

    # Verify default ref is NOT used for modelcontroller
    [[ "$output" != *"opendatahub-io:odh-model-controller:incubating:config"* ]]

    # Verify kserve still uses default (not affected by override)
    [[ "$output" == *"opendatahub-io:kserve:release-v0.17:config"* ]]

    # Verify actual download
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

@test "RHOAI mode uses red-hat-data-services repos with correct variables" {
    ODH_PLATFORM_TYPE=RHOAI run bash "${SCRIPT}" "${TEST_DST}"
    [ "$status" -eq 0 ]

    # Verify actual variable content in logs (trustworthy)
    [[ "$output" == *"red-hat-data-services:kserve:rhoai-"* ]]
    [[ "$output" == *"red-hat-data-services:odh-model-controller:rhoai-"* ]]

    # Verify ODH repos are NOT used (negative test)
    [[ "$output" != *"opendatahub-io:kserve"* ]]
    [[ "$output" != *"opendatahub-io:odh-model-controller"* ]]

    # Verify platform message (informational, less trustworthy)
    [[ "$output" == *"Cloning manifests for RHOAI"* ]]

    # Verify actual file download results
    [ -d "${TEST_DST}/kserve" ]
    [ -d "${TEST_DST}/modelcontroller" ]
}

