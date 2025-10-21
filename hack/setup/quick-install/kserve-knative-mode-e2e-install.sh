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

# Install KServe Knative Mode dependencies and components for E2E testing
#
# AUTO-GENERATED from: kserve-knative-mode-e2e-install.definition
# DO NOT EDIT MANUALLY
#
# To regenerate:
#   ./scripts/generate-install-script.py kserve-knative-mode-e2e-install.definition
#
# Usage: kserve-knative-mode-e2e-install.sh [--reinstall|--uninstall]

set -o errexit
set -o nounset
set -o pipefail

#================================================
# Helper Functions (from common.sh)
#================================================

# Utility Functions
# ============================================================================

find_repo_root() {
    local current_dir="${1:-$(pwd)}"

    while [[ "$current_dir" != "/" ]]; do
        if [[ -d "${current_dir}/.git" ]]; then
            echo "$current_dir"
            return 0
        fi
        current_dir="$(dirname "$current_dir")"
    done

    echo "Error: Could not find git repository root" >&2
    exit 1
}

ensure_dir() {
    local dir_path="${1}"

    if [[ -d "${dir_path}" ]]; then
        return 0
    fi

    mkdir -p "${dir_path}"
}

detect_os() {
    local os=""
    case "$(uname -s)" in
        Linux*)  os="linux" ;;
        Darwin*) os="darwin" ;;
        *)       echo "Unsupported OS" >&2; exit 1 ;;
    esac
    echo "$os"
}

detect_arch() {
    local arch=""
    case "$(uname -m)" in
        x86_64)  arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *)       echo "Unsupported architecture" >&2; exit 1 ;;
    esac
    echo "$arch"
}

log_info() {
    echo "[INFO] $*"
}

log_error() {
    echo "[ERROR] $*" >&2
}

log_success() {
    echo "[SUCCESS] $*"
}


# ============================================================================
# Infrastructure Installation Helper Functions
# ============================================================================

# Detect the platform (kind/minikube/openshift/kubernetes)
# Returns: kind, minikube, openshift, or kubernetes
detect_platform() {
    # Check for OpenShift
    if kubectl get clusterversion &>/dev/null; then
        echo "openshift"
        return 0
    fi

    # Check for Kind
    local node_hostname
    node_hostname=$(kubectl get nodes -o jsonpath='{.items[0].metadata.labels.kubernetes\.io/hostname}' 2>/dev/null || echo "")
    if [[ "$node_hostname" == *"kind"* ]]; then
        echo "kind"
        return 0
    fi

    # Check for Minikube
    local current_context
    current_context=$(kubectl config current-context 2>/dev/null || echo "")
    if [[ "$current_context" == *"minikube"* ]]; then
        echo "minikube"
        return 0
    fi

    # Default to standard Kubernetes
    echo "kubernetes"
    return 0
}

# Wait for pods to be created (exist)
# Usage: wait_for_pods_created <namespace> <label-selector> [timeout_seconds]
wait_for_pods_created() {
    local namespace="$1"
    local label_selector="$2"
    local timeout="${3:-60}"
    local elapsed=0

    log_info "Waiting for pods with label '$label_selector' in namespace '$namespace' to be created..."

    while true; do
        local pod_count=$(kubectl get pods -n "$namespace" -l "$label_selector" --no-headers 2>/dev/null | wc -l)

        if [ "$pod_count" -gt 0 ]; then
            log_info "Found $pod_count pod(s) with label '$label_selector'"
            return 0
        fi

        if [ $elapsed -ge $timeout ]; then
            log_error "Timeout waiting for pods with label '$label_selector' to be created"
            return 1
        fi

        sleep 2
        elapsed=$((elapsed + 2))
    done
}

# Wait for pods to be ready
# Usage: wait_for_pods_ready <namespace> <label-selector> [timeout]
wait_for_pods_ready() {
    local namespace="$1"
    local label_selector="$2"
    local timeout="${3:-180s}"

    log_info "Waiting for pods with label '$label_selector' in namespace '$namespace' to be ready..."
    kubectl wait --for=condition=Ready pod -l "$label_selector" -n "$namespace" --timeout="$timeout"
}

# Wait for pods to be ready (combines both creation and ready checks)
# Usage: wait_for_pods <namespace> <label-selector> [timeout]
wait_for_pods() {
    local namespace="$1"
    local label_selector="$2"
    local timeout="${3:-180s}"

    # Convert timeout to seconds for pod creation check
    local timeout_seconds="${timeout%s}"
    local timeout_created=60

    # If timeout is longer than 60s, use 60s for creation, rest for ready
    # If timeout is shorter, split it
    if [ "$timeout_seconds" -gt 60 ]; then
        timeout_created=60
    else
        timeout_created=$((timeout_seconds / 3))
    fi

    # First, wait for pods to be created
    wait_for_pods_created "$namespace" "$label_selector" "$timeout_created" || return 1

    # Then, wait for pods to be ready
    wait_for_pods_ready "$namespace" "$label_selector" "$timeout" || return 1

    log_success "Pods with label '$label_selector' in namespace '$namespace' are ready!"
}

# Wait for deployment to be available using kubectl wait
# Usage: wait_for_deployment <namespace> <deployment-name> [timeout]
# Note: This uses kubectl wait --for=condition=Available, which checks deployment status directly
wait_for_deployment() {
    local namespace="$1"
    local deployment_name="$2"
    local timeout="${3:-180s}"

    log_info "Waiting for deployment '$deployment_name' in namespace '$namespace' to be available..."
    kubectl wait --timeout="$timeout" -n "$namespace" deployment/"$deployment_name" --for=condition=Available

    if [ $? -eq 0 ]; then
        log_success "Deployment '$deployment_name' in namespace '$namespace' is available!"
    else
        log_error "Deployment '$deployment_name' in namespace '$namespace' failed to become available within $timeout"
        return 1
    fi
}

# Wait for CRD to be established
# Usage: wait_for_crd <crd-name> [timeout]
wait_for_crd() {
    local crd_name="$1"
    local timeout="${2:-60s}"

    log_info "Waiting for CRD '$crd_name' to be established..."
    kubectl wait --for=condition=Established --timeout="$timeout" crd/"$crd_name"
}

# Wait for multiple CRDs to be established
# Usage: wait_for_crds <timeout> <crd1> <crd2> ...
wait_for_crds() {
    local timeout="$1"
    shift

    for crd in "$@"; do
        wait_for_crd "$crd" "$timeout" || return 1
    done

    log_success "All CRDs are established!"
}

# Create namespace if it does not exist (skip if already exists)
# Usage: create_or_skip_namespace <namespace>
create_or_skip_namespace() {
    local namespace="$1"

    if kubectl get namespace "$namespace" &>/dev/null; then
        log_info "Namespace '$namespace' already exists"
    else
        log_info "Creating namespace '$namespace'..."
        kubectl create namespace "$namespace"
        log_success "Namespace '$namespace' created"
    fi
}

# Check if required CLI tools exist
# Usage: check_cli_exist <tool1> [tool2] [tool3] ...
check_cli_exist() {
    local missing=()
    for cmd in "$@"; do
        if ! command_exists "$cmd"; then
            missing+=("$cmd")
        fi
    done

    if [ ${#missing[@]} -gt 0 ]; then
        log_error "Required CLI tool(s) not found: ${missing[*]}"
        log_error "Please install missing tool(s) first."
        exit 1
    fi
}

command_exists() {
    command -v "$1" &>/dev/null
}

# ============================================================================

#================================================
# Determine repository root using find_repo_root
#================================================

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
REPO_ROOT="$(find_repo_root "${SCRIPT_DIR}")"
export REPO_ROOT
export BIN_DIR="${REPO_ROOT}/bin"
export PATH="${BIN_DIR}:${PATH}"

UNINSTALL="${UNINSTALL:-false}"
REINSTALL="${REINSTALL:-false}"

if [[ "$*" == *"--uninstall"* ]]; then
    UNINSTALL=true
elif [[ "$*" == *"--reinstall"* ]]; then
    REINSTALL=true
fi

export REINSTALL
export UNINSTALL

# RELEASE mode (from definition file)
RELEASE="false"
export RELEASE

#================================================
# Version Dependencies (from kserve-deps.env)
#================================================

GOLANGCI_LINT_VERSION=v1.64.8
CONTROLLER_TOOLS_VERSION=v0.16.2
ENVTEST_VERSION=latest
YQ_VERSION=v4.28.1
HELM_VERSION=v3.16.3
KUSTOMIZE_VERSION=v5.5.0
HELM_DOCS_VERSION=v1.12.0
BLACK_FMT_VERSION=24.3
FLAKE8_LINT_VERSION=7.1
POETRY_VERSION=1.8.3
UV_VERSION=0.7.8
CERT_MANAGER_VERSION=v1.17.0
ENVOY_GATEWAY_VERSION=v1.2.2
ENVOY_AI_GATEWAY_VERSION=v0.3.0
KSERVE_VERSION=v0.16.0-rc1
ISTIO_VERSION=1.24.2
KNATIVE_SERVING_VERSION=v0.44.0
KEDA_VERSION=2.16.1
OPENTELEMETRY_OPERATOR_VERSION=0.113.0
LWS_VERSION=v0.6.2
GATEWAY_API_VERSION=v1.2.1
GIE_VERSION=v0.3.0

#================================================
# Global Variables (from global-vars.env)
#================================================
# These provide default namespace values that can be overridden
# by environment variables or GLOBAL_ENV settings below

KEDA_NAMESPACE="${KEDA_NAMESPACE:-keda}"
KSERVE_NAMESPACE="${KSERVE_NAMESPACE:-kserve}"
OTEL_NAMESPACE="${OTEL_NAMESPACE:-opentelemetry-operator}"
OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-knative-operator}"
SERVING_NAMESPACE="${SERVING_NAMESPACE:-knative-serving}"
ISTIO_NAMESPACE="${ISTIO_NAMESPACE:-istio-system}"
GATEWAY_NAMESPACE="${GATEWAY_NAMESPACE:-kserve}"

#================================================
# Component-Specific Variables
#================================================

ADDON_RELEASE_NAME="keda-otel-scaler"
OTEL_RELEASE_NAME="my-opentelemetry-operator"
KSERVE_CRD_DIR="${REPO_ROOT}/config/crd"
KSERVE_CONFIG_DIR="${REPO_ROOT}/config/default"
DEPLOYMENT_MODE="${DEPLOYMENT_MODE:-Knative}"
LLMISVC="${LLMISVC:-false}"
RELEASE="${RELEASE:-false}"

#================================================
# Component Functions
#================================================

# ----------------------------------------
# Component: cert-manager
# ----------------------------------------

uninstall_cert_manager() {
    log_info "Uninstalling cert-manager..."
    helm uninstall cert-manager -n cert-manager 2>/dev/null || true
    kubectl delete all --all -n cert-manager --force --grace-period=0 2>/dev/null || true
    kubectl delete namespace cert-manager --wait=true --timeout=60s --force --grace-period=0 2>/dev/null || true
    log_success "cert-manager uninstalled"
}

install_cert_manager() {
    if helm list -n cert-manager 2>/dev/null | grep -q "cert-manager"; then
        if [ "$REINSTALL" = false ]; then
            log_info "cert-manager is already installed. Use --reinstall to reinstall."
            return 0
        else
            log_info "Reinstalling cert-manager..."
            uninstall
        fi
    fi

    log_info "Adding cert-manager Helm repository..."
    helm repo add jetstack https://charts.jetstack.io --force-update

    log_info "Installing cert-manager ${CERT_MANAGER_VERSION}..."
    helm install \
        cert-manager jetstack/cert-manager \
        --namespace cert-manager \
        --create-namespace \
        --version "${CERT_MANAGER_VERSION}" \
        --set crds.enabled=true \
        --wait

    log_success "Successfully installed cert-manager ${CERT_MANAGER_VERSION} via Helm"

    wait_for_pods "cert-manager" "app in (cert-manager,webhook)" "180s"

    log_success "cert-manager is ready!"
}

# ----------------------------------------
# Component: istio
# ----------------------------------------

uninstall_istio() {
    log_info "Uninstalling Istio..."
    helm uninstall istio-ingressgateway -n "${ISTIO_NAMESPACE}" 2>/dev/null || true
    helm uninstall istiod -n "${ISTIO_NAMESPACE}" 2>/dev/null || true
    helm uninstall istio-base -n "${ISTIO_NAMESPACE}" 2>/dev/null || true
    kubectl delete all --all -n "${ISTIO_NAMESPACE}" --force --grace-period=0 2>/dev/null || true
    kubectl delete namespace "${ISTIO_NAMESPACE}" --wait=true --timeout=60s --force --grace-period=0 2>/dev/null || true
    log_success "Istio uninstalled"
}

install_istio() {
    if helm list -n "${ISTIO_NAMESPACE}" 2>/dev/null | grep -q "istio-base"; then
        if [ "$REINSTALL" = false ]; then
            log_info "Istio is already installed. Use --reinstall to reinstall."
            return 0
        else
            log_info "Reinstalling Istio..."
            uninstall
        fi
    fi

    log_info "Adding Istio Helm repository..."
    helm repo add istio https://istio-release.storage.googleapis.com/charts --force-update

    log_info "Installing istio-base ${ISTIO_VERSION}..."
    helm install istio-base istio/base \
        --namespace "${ISTIO_NAMESPACE}" \
        --create-namespace \
        --version "${ISTIO_VERSION}" \
        --set defaultRevision=default \
        --wait \
        ${ISTIO_BASE_EXTRA_ARGS:-}

    log_info "Installing istiod ${ISTIO_VERSION}..."
    helm install istiod istio/istiod \
        --namespace "${ISTIO_NAMESPACE}" \
        --version "${ISTIO_VERSION}" \
        --set proxy.autoInject=disabled \
        --set-string pilot.podAnnotations."cluster-autoscaler\.kubernetes\.io/safe-to-evict"=true \
        --wait \
        ${ISTIOD_EXTRA_ARGS:-}

    log_info "Installing istio-ingressgateway ${ISTIO_VERSION}..."
    helm install istio-ingressgateway istio/gateway \
        --namespace "${ISTIO_NAMESPACE}" \
        --version "${ISTIO_VERSION}" \
        --set-string podAnnotations."cluster-autoscaler\.kubernetes\.io/safe-to-evict"=true \
        ${ISTIO_GATEWAY_EXTRA_ARGS:-}

    log_success "Successfully installed Istio ${ISTIO_VERSION} via Helm"

    wait_for_pods "${ISTIO_NAMESPACE}" "app=istiod" "600s"
    wait_for_pods "${ISTIO_NAMESPACE}" "app=istio-ingressgateway" "600s"

    log_success "Istio is ready!"
}

# ----------------------------------------
# Component: istio-ingress-class
# ----------------------------------------

uninstall_istio_ingress_class() {
    log_info "Deleting Istio IngressClass 'istio'..."
    kubectl delete ingressclass "istio" --ignore-not-found=true --force --grace-period=0 2>/dev/null || true
    log_success "Istio IngressClass 'istio' deleted"
}

install_istio_ingress_class() {
    if kubectl get ingressclass "istio" &>/dev/null; then
        if [ "$REINSTALL" = false ]; then
            log_info "Istio IngressClass 'istio' already exists. Use --reinstall to recreate."
            exit 0
        else
            log_info "Recreating Istio IngressClass 'istio'..."
            uninstall
        fi
    fi

    log_info "Creating Istio IngressClass 'istio'..."
    cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: IngressClass
metadata:
  name: istio
spec:
  controller: istio.io/ingress-controller
EOF

    log_success "Istio IngressClass 'istio' created successfully!"
}

# ----------------------------------------
# Component: keda
# ----------------------------------------

uninstall_keda() {
    log_info "Uninstalling KEDA..."

    helm uninstall keda-otel-scaler -n "${KEDA_NAMESPACE}" 2>/dev/null || true
    helm uninstall keda -n "${KEDA_NAMESPACE}" 2>/dev/null || true
    kubectl delete all --all -n "${KEDA_NAMESPACE}" --force --grace-period=0 2>/dev/null || true
    kubectl delete namespace "${KEDA_NAMESPACE}" --wait=true --timeout=60s --force --grace-period=0 2>/dev/null || true

    log_success "KEDA uninstalled"
}

install_keda() {
    if helm list -n "${KEDA_NAMESPACE}" 2>/dev/null | grep -q "keda"; then
        if [ "$REINSTALL" = false ]; then
            log_info "KEDA is already installed. Use --reinstall to reinstall."
            return 0
        else
            log_info "Reinstalling KEDA..."
            uninstall
        fi
    fi

    log_info "Adding KEDA Helm repository..."
    helm repo add kedacore https://kedacore.github.io/charts --force-update

    log_info "Installing KEDA ${KEDA_VERSION}..."
    helm install keda kedacore/keda \
        --namespace "${KEDA_NAMESPACE}" \
        --create-namespace \
        --version "${KEDA_VERSION}" \
        --wait \
        ${KEDA_EXTRA_ARGS:-}

    log_success "Successfully installed KEDA ${KEDA_VERSION} via Helm"

    wait_for_pods "${KEDA_NAMESPACE}" "app.kubernetes.io/name=keda-operator" "300s"

    log_success "KEDA is ready!"
}

# ----------------------------------------
# Component: keda-otel-addon
# ----------------------------------------

uninstall_keda_otel_addon() {
    log_info "Uninstalling KEDA OTel add-on..."
    helm uninstall "${ADDON_RELEASE_NAME}" -n "${KEDA_NAMESPACE}" 2>/dev/null || true
    log_success "KEDA OTel add-on uninstalled"
}

install_keda_otel_addon() {
    if ! kubectl get namespace "${KEDA_NAMESPACE}" &>/dev/null; then
        log_error "KEDA namespace '${KEDA_NAMESPACE}' does not exist. Please install KEDA first."
        exit 1
    fi

    if helm list -n "${KEDA_NAMESPACE}" 2>/dev/null | grep -q "${ADDON_RELEASE_NAME}"; then
        if [ "$REINSTALL" = false ]; then
            log_info "KEDA OTel add-on is already installed. Use --reinstall to reinstall."
            return 0
        else
            log_info "Reinstalling KEDA OTel add-on..."
            uninstall
        fi
    fi

    log_info "Installing KEDA OTel add-on ${KEDA_OTEL_ADDON_VERSION} from kedify/otel-add-on..."
    helm upgrade -i "${ADDON_RELEASE_NAME}" \
        oci://ghcr.io/kedify/charts/otel-add-on \
        --namespace "${KEDA_NAMESPACE}" \
        --version="${KEDA_OTEL_ADDON_VERSION}" \
        --wait \
        ${KEDA_OTEL_ADDON_EXTRA_ARGS:-}

    log_success "Successfully installed KEDA OTel add-on ${KEDA_OTEL_ADDON_VERSION} via Helm"

    wait_for_pods "${KEDA_NAMESPACE}" "app.kubernetes.io/instance=${ADDON_RELEASE_NAME}" "300s"

    log_success "KEDA OTel add-on is ready!"
}

# ----------------------------------------
# Component: opentelemetry
# ----------------------------------------

uninstall_opentelemetry() {
    log_info "Uninstalling OpenTelemetry Operator..."
    helm uninstall "${OTEL_RELEASE_NAME}" -n "${OTEL_NAMESPACE}" 2>/dev/null || true
    kubectl delete all --all -n "${OTEL_NAMESPACE}" --force --grace-period=0 2>/dev/null || true
    kubectl delete namespace "${OTEL_NAMESPACE}" --wait=true --timeout=60s --force --grace-period=0 2>/dev/null || true
    log_success "OpenTelemetry Operator uninstalled"
}

install_opentelemetry() {
    if helm list -n "${OTEL_NAMESPACE}" 2>/dev/null | grep -q "${OTEL_RELEASE_NAME}"; then
        if [ "$REINSTALL" = false ]; then
            log_info "OpenTelemetry Operator is already installed. Use --reinstall to reinstall."
            return 0
        else
            log_info "Reinstalling OpenTelemetry Operator..."
            uninstall
        fi
    fi

    log_info "Adding OpenTelemetry Helm repository..."
    helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts --force-update

    log_info "Installing OpenTelemetry Operator..."
    helm install "${OTEL_RELEASE_NAME}" open-telemetry/opentelemetry-operator \
        --namespace "${OTEL_NAMESPACE}" \
        --create-namespace \
        --wait \
        ${OTEL_OPERATOR_EXTRA_ARGS:-}

    log_success "Successfully installed OpenTelemetry Operator via Helm"

    wait_for_pods "${OTEL_NAMESPACE}" "app.kubernetes.io/instance=${OTEL_RELEASE_NAME}" "300s"

    log_success "OpenTelemetry Operator is ready!"
}

# ----------------------------------------
# Component: kserve-kustomize
# ----------------------------------------

# Set CRD/Config directories based on LLMISVC
if [ "${LLMISVC}" = "true" ]; then
    KSERVE_CRD_DIR="${REPO_ROOT}/config/crd/llmisvc"
    KSERVE_CONFIG_DIR="${REPO_ROOT}/config/overlays/llmisvc"
fi

uninstall_kserve_kustomize() {
    log_info "Uninstalling KServe..."

    # RELEASE mode: use embedded manifests
    if [ "$RELEASE" = "true" ]; then
        if type uninstall_kserve_manifest &>/dev/null; then
            uninstall_kserve_manifest
        else
            log_error "RELEASE mode enabled but uninstall_kserve_manifest function not found"
            log_error "This script should be called from a generated installation script"
            exit 1
        fi
    else
        # Development mode: use kustomize
        # Uninstall resources first
        kubectl kustomize "${KSERVE_CONFIG_DIR}" | kubectl delete -f - --force --grace-period=0 2>/dev/null || true

        # Then uninstall CRDs
        kubectl kustomize "${KSERVE_CRD_DIR}" | kubectl delete -f - --force --grace-period=0 2>/dev/null || true
    fi

    kubectl delete all --all -n "${KSERVE_NAMESPACE}" --force --grace-period=0 2>/dev/null || true
    kubectl delete namespace "${KSERVE_NAMESPACE}" --wait=true --timeout=60s --force --grace-period=0 2>/dev/null || true
    log_success "KServe uninstalled"
}

install_kserve_kustomize() {
    if kubectl get deployment kserve-controller-manager -n "${KSERVE_NAMESPACE}" &>/dev/null; then
        if [ "$REINSTALL" = false ]; then
            log_info "KServe is already installed. Use --reinstall to reinstall."
            return 0
        else
            log_info "Reinstalling KServe..."
            uninstall
        fi
    fi

    # RELEASE mode: use embedded manifests from generated script
    if [ "$RELEASE" = "true" ]; then
        log_info "Installing KServe using embedded manifests (RELEASE mode)..."

        # Call manifest functions (these should be available in generated script)
        if type install_kserve_manifest &>/dev/null; then
            install_kserve_manifest
        else
            log_error "RELEASE mode enabled but install_kserve_manifest function not found"
            log_error "This script should be called from a generated installation script"
            exit 1
        fi
    else
        # Development mode: use local kustomize build
        log_info "Installing KServe via Kustomize..."
        log_info "📍 Using local config from ${KSERVE_CRD_DIR} and ${KSERVE_CONFIG_DIR}"

        # Install CRDs first
        log_info "Installing KServe CRDs..."
        kustomize build "${KSERVE_CRD_DIR}" | kubectl apply --server-side -f -

        # Wait for CRDs to be established
        wait_for_crds "60s" \
            "inferenceservices.serving.kserve.io" \
            "servingruntimes.serving.kserve.io" \
            "clusterservingruntimes.serving.kserve.io" \
            "llminferenceservices.serving.kserve.io" \
            "llminferenceserviceconfigs.serving.kserve.io"

        # Install resources
        log_info "Installing KServe resources..."
        kustomize build "${KSERVE_CONFIG_DIR}" | kubectl apply --server-side -f -
    fi

    # Update deployment mode in ConfigMap if not default
    if [ "${DEPLOYMENT_MODE}" != "Knative" ]; then
        log_info "Configuring deployment mode: ${DEPLOYMENT_MODE}"
        kubectl patch configmap inferenceservice-config -n "${KSERVE_NAMESPACE}" \
            --type='merge' \
            -p "{\"data\":{\"deploy\":\"{\\\"defaultDeploymentMode\\\":\\\"${DEPLOYMENT_MODE}\\\"}\" }}"
    fi

    log_success "Successfully installed KServe"

    # Wait for all controller managers to be ready
    log_info "Waiting for KServe controllers to be ready..."
    wait_for_pods "${KSERVE_NAMESPACE}" "control-plane=kserve-controller-manager" "300s"
    wait_for_pods "${KSERVE_NAMESPACE}" "app.kubernetes.io/name=kserve-localmodel-controller-manager" "300s"
    wait_for_pods "${KSERVE_NAMESPACE}" "app.kubernetes.io/name=llmisvc-controller-manager" "300s"

    log_success "KServe is ready!"
}



#================================================
# Main Installation Logic
#================================================

main() {
    if [ "$UNINSTALL" = true ]; then
        echo "=========================================="
        echo "Uninstalling components..."
        echo "=========================================="
        uninstall_kserve_kustomize
        uninstall_opentelemetry
        uninstall_keda_otel_addon
        uninstall_keda
        uninstall_istio_ingress_class
        uninstall_istio
        uninstall_cert_manager
        echo "=========================================="
        echo "✅ Uninstallation completed!"
        echo "=========================================="
        exit 0
    fi

    echo "=========================================="
    echo "Install KServe Knative Mode dependencies and components for E2E testing"
    echo "=========================================="



    echo "Installing helm..."
    bash "${REPO_ROOT}/hack/setup/cli/install-helm.sh"
    echo "Installing kustomize..."
    bash "${REPO_ROOT}/hack/setup/cli/install-kustomize.sh"
    echo "Installing yq..."
    bash "${REPO_ROOT}/hack/setup/cli/install-yq.sh"

    install_cert_manager
    install_istio
    install_istio_ingress_class
    install_keda
    install_keda_otel_addon
    install_opentelemetry
    install_kserve_kustomize

    echo "=========================================="
    echo "✅ Installation completed successfully!"
    echo "=========================================="
}



main "$@"
