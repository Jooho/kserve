#!/usr/bin/env bash
#
# kserve-module E2E Cluster Setup
#
# Installs dependencies and deploys kserve-module on an existing cluster.
# Reuses hack/setup/infra scripts for dependency installation.
#
# Usage:
#   ./kserve-module/test/scripts/setup-cluster.sh [--platform xks|ocp-sim|ocp]
#
# Installs: Gateway API CRDs, cert-manager, Istio, LWS, then deploys kserve-module.

set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
PLATFORM="${PLATFORM:-xks}"
KSERVE_NAMESPACE="${KSERVE_NAMESPACE:-opendatahub}"
KSERVE_MODULE_IMG="${KSERVE_MODULE_IMG:-}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
INFRA_DIR="${PROJECT_ROOT}/hack/setup/infra"

# Source common utilities (also loads kserve-deps.env and global-vars.env)
source "${PROJECT_ROOT}/hack/setup/common.sh"

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --platform)   PLATFORM="$2"; shift 2 ;;
      --image)      KSERVE_MODULE_IMG="$2"; shift 2 ;;
      -h|--help)    usage; exit 0 ;;
      *)            echo "Unknown option: $1"; usage; exit 1 ;;
    esac
  done

  case "$PLATFORM" in
    xks|ocp-sim|ocp) ;;
    *) log_error "Invalid platform: $PLATFORM (must be xks, ocp-sim, or ocp)"; exit 1 ;;
  esac
}

usage() {
  cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Options:
  --platform xks|ocp-sim|ocp   Target platform (default: xks)
  --image IMAGE                 Controller image (e.g. quay.io/org/kserve-module:tag)
  -h, --help                    Show this help
EOF
}

# ---------------------------------------------------------------------------
# setup_cert_manager_pki (kserve-module specific)
# ---------------------------------------------------------------------------
setup_cert_manager_pki() {
  log_info "Setting up cert-manager PKI for kserve-module..."
  kubectl apply -k "${PROJECT_ROOT}/config/overlays/odh-test/cert-manager"
  kubectl wait --for=condition=Ready clusterissuer/opendatahub-selfsigned-issuer --timeout=60s
  kubectl wait --for=condition=Ready certificate/opendatahub-ca -n cert-manager --timeout=120s
  kubectl wait --for=condition=Ready clusterissuer/opendatahub-ca-issuer --timeout=60s
  log_success "PKI chain created"
}

# ---------------------------------------------------------------------------
# deploy_kserve_module
# ---------------------------------------------------------------------------
deploy_kserve_module() {
  log_info "Deploying kserve-module..."
  create_or_skip_namespace "${KSERVE_NAMESPACE}"

  local config_dir="${PROJECT_ROOT}/kserve-module/config"

  local output
  output=$(kustomize build "$config_dir" | sed "s|namespace: kserve|namespace: ${KSERVE_NAMESPACE}|g")

  if [[ -n "${KSERVE_MODULE_IMG}" ]]; then
    log_info "Using custom image: ${KSERVE_MODULE_IMG}"
    output=$(echo "$output" | sed "s|image: .*kserve-module-controller.*|image: ${KSERVE_MODULE_IMG}|g")
  fi

  echo "$output" | kubectl apply --server-side=true --force-conflicts -f -

  log_info "Waiting for controller rollout..."
  kubectl rollout status deployment/kserve-module-controller-manager \
    -n "${KSERVE_NAMESPACE}" --timeout=300s
  log_success "kserve-module deployed"

  log_info "Creating sample Kserve CR..."
  kustomize build "${PROJECT_ROOT}/kserve-module/config/overlays/test" | \
    kubectl apply --server-side=true -f -
  log_success "Kserve CR created"
}

# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------
main() {
  parse_args "$@"

  echo ""
  echo "=========================================="
  echo "kserve-module E2E Setup"
  echo "=========================================="
  echo "  Platform:  ${PLATFORM}"
  echo "  Namespace: ${KSERVE_NAMESPACE}"
  [[ -n "${KSERVE_MODULE_IMG}" ]] && echo "  Image:     ${KSERVE_MODULE_IMG}"
  echo "=========================================="
  echo ""

  check_cli_exist kubectl kustomize

  case "$PLATFORM" in
    xks)
      "${INFRA_DIR}/gateway-api/manage.gateway-api-crd.sh"
      "${INFRA_DIR}/manage.cert-manager-helm.sh"
      setup_cert_manager_pki
      "${INFRA_DIR}/manage.istio-helm.sh"
      "${INFRA_DIR}/manage.lws-operator.sh"
      deploy_kserve_module
      ;;
    ocp-sim)
      "${INFRA_DIR}/gateway-api/manage.gateway-api-crd.sh"
      "${INFRA_DIR}/manage.cert-manager-helm.sh"
      setup_cert_manager_pki
      "${INFRA_DIR}/manage.istio-helm.sh"
      "${INFRA_DIR}/manage.lws-operator.sh"
      deploy_kserve_module
      ;;
    ocp)
      deploy_kserve_module
      ;;
  esac

  echo ""
  log_success "Setup complete!"
  echo ""
  echo "  kubectl get pods -n ${KSERVE_NAMESPACE}"
  echo "  kubectl get crd kserves.components.platform.opendatahub.io"
  echo ""
}

main "$@"
