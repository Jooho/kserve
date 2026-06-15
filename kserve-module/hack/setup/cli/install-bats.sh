#!/usr/bin/env bash

# INIT
SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
source "${SCRIPT_DIR}/../../../../hack/setup/common.sh"
# INIT END

BATS_VERSION="${BATS_VERSION:-v1.13.0}"

install() {
    local version="${BATS_VERSION#v}"
    local download_url="https://github.com/bats-core/bats-core/archive/refs/tags/v${version}.tar.gz"

    log_info "Installing bats ${BATS_VERSION}..."

    if [[ -x "${BIN_DIR}/bats" ]]; then
        local current_version=$("${BIN_DIR}/bats" --version 2>/dev/null | awk '{print $2}')
        if [[ -n "$current_version" ]] && version_gte "$current_version" "$version"; then
            log_info "bats ${current_version} is already installed in ${BIN_DIR} (>= ${version})"
            return 0
        fi
        [[ -n "$current_version" ]] && log_info "Upgrading bats from ${current_version} to ${version}..."
    fi

    local temp_dir=$(mktemp -d)

    if command -v curl &>/dev/null; then
        curl -sL "${download_url}" -o "${temp_dir}/bats.tar.gz"
    elif command -v wget &>/dev/null; then
        wget -q "${download_url}" -O "${temp_dir}/bats.tar.gz"
    else
        log_error "Neither curl nor wget is available"
        rm -rf "${temp_dir}"
        exit 1
    fi

    tar -xzf "${temp_dir}/bats.tar.gz" -C "${temp_dir}"

    local bats_dir="${temp_dir}/bats-core-${version}"
    if [[ ! -d "${bats_dir}" ]]; then
        log_error "bats directory not found in archive"
        rm -rf "${temp_dir}"
        exit 1
    fi

    local install_dir="${BIN_DIR}/bats-core"
    rm -rf "${install_dir}"
    cp -r "${bats_dir}" "${install_dir}"
    ln -sf "${install_dir}/bin/bats" "${BIN_DIR}/bats"

    rm -rf "${temp_dir}"

    log_success "Successfully installed bats ${BATS_VERSION} to ${BIN_DIR}/bats"
    "${BIN_DIR}/bats" --version
}

install
