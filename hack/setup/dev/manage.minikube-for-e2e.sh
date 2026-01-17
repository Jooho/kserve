#!/bin/bash

# Copyright 2025 The KServe Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Setup local Minikube cluster for KServe E2E testing
# Usage: manage.minikube-for-e2e.sh [--reinstall|--uninstall]
#   or:  REINSTALL=true manage.minikube-for-e2e.sh
#   or:  UNINSTALL=true manage.minikube-for-e2e.sh

# INIT
SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

source "${SCRIPT_DIR}/../common.sh"

REINSTALL="${REINSTALL:-false}"
UNINSTALL="${UNINSTALL:-false}"

if [[ "$*" == *"--uninstall"* ]]; then
    UNINSTALL=true
elif [[ "$*" == *"--reinstall"* ]]; then
    REINSTALL=true
fi
# INIT END

# Minikube settings (matching .github/workflows/e2e-test.yml)
MINIKUBE_CLUSTER_NAME="${MINIKUBE_CLUSTER_NAME:-minikube}"
MINIKUBE_K8S_VERSION="${MINIKUBE_K8S_VERSION:-v1.30.7}"
MINIKUBE_DRIVER="${MINIKUBE_DRIVER:-docker}"
MINIKUBE_CPUS="${MINIKUBE_CPUS:-max}"
MINIKUBE_MEMORY="${MINIKUBE_MEMORY:-max}"
MINIKUBE_NODES="${MINIKUBE_NODES:-1}"

# Install minikube if not present (uses MINIKUBE_VERSION from kserve-deps.env)
if ! command_exists minikube; then
    log_warning "minikube not found, installing version ${MINIKUBE_VERSION}..."
    "${SCRIPT_DIR}/../cli/install-minikube.sh"
fi

check_cli_exist minikube docker kubectl

uninstall() {
    log_info "Destroying Minikube cluster..."
    minikube delete --profile "${MINIKUBE_CLUSTER_NAME}" 2>/dev/null || true

    # Stop tunnel if running
    pkill -f "minikube tunnel" 2>/dev/null || true

    log_success "Minikube cluster destroyed"
}

install() {
    # Check if kubectl is connected to a cluster
    if kubectl cluster-info &>/dev/null; then
        local current_platform=$(detect_platform)

        if [ "$current_platform" != "minikube" ]; then
            log_warning "Found existing '$current_platform' cluster"
            log_warning "E2E tests require significant resources (CPU/Memory)"
            log_warning ""
            log_warning "Recommendations:"
            log_warning "  1. Delete existing cluster: "
            case "$current_platform" in
                kind)
                    log_warning "     kind delete cluster"
                    ;;
                openshift)
                    log_warning "     (OpenShift cluster - manual cleanup required)"
                    ;;
                *)
                    log_warning "     (Manual cluster cleanup required)"
                    ;;
            esac
            log_warning "  2. Then run this script again"
            log_warning ""
            log_error "Aborting to avoid resource conflicts"
            log_error "Use FORCE=true to override this check (not recommended)"

            if [ "${FORCE:-false}" != "true" ]; then
                exit 1
            fi

            log_warning "FORCE=true detected, proceeding anyway..."
        fi
    fi

    # Check if Minikube cluster already exists
    if minikube status --profile "${MINIKUBE_CLUSTER_NAME}" &>/dev/null; then
        if [ "$REINSTALL" = false ]; then
            log_info "Minikube cluster '${MINIKUBE_CLUSTER_NAME}' already exists. Use --reinstall to recreate."
            return 0
        else
            log_info "Recreating Minikube cluster..."
            uninstall
        fi
    fi

    # Create Minikube cluster
    log_info "Creating Minikube cluster '${MINIKUBE_CLUSTER_NAME}'..."

    minikube start \
        --profile="${MINIKUBE_CLUSTER_NAME}" \
        --kubernetes-version="${MINIKUBE_K8S_VERSION}" \
        --driver="${MINIKUBE_DRIVER}" \
        --cpus="${MINIKUBE_CPUS}" \
        --memory="${MINIKUBE_MEMORY}" \
        --nodes="${MINIKUBE_NODES}" \
        --wait-timeout=6m0s \
        --wait=all

    log_success "Minikube cluster created"

    # Start tunnel in background
    log_info "Starting Minikube tunnel (background)..."
    nohup minikube tunnel --profile="${MINIKUBE_CLUSTER_NAME}" > minikube-tunnel.log 2>&1 &
    TUNNEL_PID=$!
    log_info "Tunnel PID: $TUNNEL_PID (logs: minikube-tunnel.log)"

    # Wait for tunnel to start
    sleep 5

    # Verify cluster status
    log_info "Verifying cluster status..."
    kubectl get pods -n kube-system

    log_success "Minikube cluster '${MINIKUBE_CLUSTER_NAME}' is ready for E2E testing"
    echo ""
    log_info "Next steps:"
    echo -e "  ${GREEN}cd ${REPO_ROOT}${RESET}"
    echo -e "  ${GREEN}./run-local-e2e.sh predictor 6${RESET}                      # Run E2E tests"
    echo -e "  ${GREEN}./run-single-test.sh predictor/test_sklearn.py${RESET}       # Run single test"
    echo ""
    log_info "To stop tunnel:"
    echo -e "  ${GREEN}pkill -f 'minikube tunnel'${RESET}"
}

if [ "$UNINSTALL" = true ]; then
    uninstall
    exit 0
fi

install
