#!/bin/bash
# Script to run E2E tests locally matching CI environment
set -e

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
source "${SCRIPT_DIR}/../../common.sh"

if [ -d "${DOCKER_IMAGES_PATH}" ]; then
  mkdir -p "${DOCKER_IMAGES_PATH}"  
fi

# Argument handling
MARKER="${1:-predictor}"
PARALLELISM="${2:-6}"

# Environment variable defaults
SKIP_MINIKUBE="${SKIP_MINIKUBE:-false}"
SKIP_BUILD_IMG="${SKIP_BUILD_IMG:-false}"
SKIP_KSERVE_SETUP="${SKIP_KSERVE_SETUP:-false}"
GH_SCRIPTS_DIR="${REPO_ROOT}/test/scripts/gh-actions"

# Dependencies configuration
NETWORK_LAYER="${NETWORK_LAYER:-istio}"
ENABLE_KEDA="${ENABLE_KEDA:-false}"

# Docker registry configuration
export KO_DOCKER_REPO="${KO_DOCKER_REPO:-kserve}"
export TAG="${TAG:-latest}"

# Define marker to build dependencies mapping
# Note: 'base' is always required and 'predictor' is needed for most tests
# Only specify additional requirements beyond base+predictor
#
# Unmapped markers (use default base+predictor):
#   collocation, grpc, llm, llminferenceservice, mms, modelcache, transformer, vllm
declare -A MARKER_EXTRA_DEPS=(
    ["explainer"]="explainer"
    ["graph"]="graph"
    ["kourier"]="graph"
    ["raw"]="explainer"
    ["rawcipn"]="explainer"
    ["autoscaling"]="explainer"
    ["path_based_routing"]="explainer"
)

# Markers that only need base (no predictor)
declare -A BASE_ONLY_MARKERS=(
    ["helm"]=1
)

if [ "SKIP_BUILD_IMG" == "false" ]; then
    # Use git commit SHA for tag
    export TAG=$(git rev-parse HEAD)
fi

echo "==================================================================="
echo "KServe E2E Tests (Local Minikube)"
echo "==================================================================="
echo "Test marker: $MARKER"
echo "Parallelism: $PARALLELISM"
echo "Docker registry: $KO_DOCKER_REPO"
echo "Image tag: $TAG"
echo "Skip Minikube setup: $SKIP_MINIKUBE"
echo "Skip image build: $SKIP_BUILD_IMG"
echo "Skip KServe setup: $SKIP_KSERVE_SETUP"
echo "Deployment mode: $DEPLOYMENT_MODE"
echo "Network layer: $NETWORK_LAYER"
echo "Enable KEDA: $ENABLE_KEDA"
echo "LLMISVC: $LLMISVC"
echo "==================================================================="
echo ""

# Helper function: Get required build types based on marker
get_required_builds() {
    local marker="$1"
    local -n result_array=$2

    # Start with base (always required)
    local -a builds=("base")

    # Check if this is a base-only marker
    if [[ -n "${BASE_ONLY_MARKERS[$marker]:-}" ]]; then
        result_array=("${builds[@]}")
        return
    fi

    # Most tests need predictor
    builds+=("predictor")

    # Add extra dependencies if defined
    local extra="${MARKER_EXTRA_DEPS[$marker]:-}"
    if [[ -n "$extra" ]]; then
        builds+=("$extra")
    fi

    result_array=("${builds[@]}")
}

# Generic build helper function
run_build_script() {
    local label="$1"
    local script="$2"
    shift 2
    local args="$@"

    echo "  [$label] Starting build..."
    if ! "$script" $args; then
        echo "  ❌ $label build failed"
        return 1
    fi
    echo "  [$label] ✓ Complete"
}

# Helper function: Build runtime images with Huggingface handling
build_runtime_images() {
    local runtime_types="$1"
    local runtime_script="${GH_SCRIPTS_DIR}/build-server-runtimes.sh"
    local temp_script=""

    # Huggingface build flag handling
    if [ "${HUGGINGFACE_IMG_BUILD:-false}" != "true" ]; then
        echo "  Creating temp build script (Huggingface will be pulled, not built)..."
        temp_script="${GH_SCRIPTS_DIR}/temp-build-server-runtimes.sh"
        cp "$runtime_script" "$temp_script"
        sed -i '/Building Huggingface CPU image/,/df -hT/c\' "$temp_script"
        runtime_script="$temp_script"
    fi

    run_build_script "Runtime Images: $runtime_types" "$runtime_script" "$runtime_types"
    local result=$?

    [ -n "$temp_script" ] && rm -f "$temp_script"
    return $result
}

# Helper function: Load images to Minikube
load_images_to_minikube() {
    echo ""
    echo "=== 3. Load images to Minikube ==="

    if [ ! -d "${DOCKER_IMAGES_PATH}" ]; then
        echo "  Warning: Docker images path does not exist: ${DOCKER_IMAGES_PATH}"
        return 0
    fi

    local count=0
    for img in ${DOCKER_IMAGES_PATH}/*; do
        if [ -f "$img" ]; then
            count=$((count + 1))
            echo "  Loading $(basename $img)..."
            minikube image load "$img"
        fi
    done

    if [ $count -eq 0 ]; then
        echo "  Warning: No images found to load"
    else
        echo "  ✓ Loaded $count image(s)"
    fi
}

# Image build function
build_images() {
    echo "=== 2. Build images ==="

    # Get required builds based on marker
    local -a required_builds
    get_required_builds "$MARKER" required_builds

    echo "  Required builds for '$MARKER': ${required_builds[*]}"

    # Build each type
    local idx=0
    for build_type in "${required_builds[@]}"; do
        idx=$((idx + 1))
        echo ""
        echo "  [Build $idx/${#required_builds[@]}] Building $build_type..."

        case "$build_type" in
            "base")
                run_build_script "Base Images" "${GH_SCRIPTS_DIR}/build-images.sh" || exit 1
                ;;
            "predictor")
                build_runtime_images "predictor,transformer" || exit 1
                ;;
            "explainer")
                build_runtime_images "explainer" || exit 1
                ;;
            "graph")
                run_build_script "Graph Test Images" "${GH_SCRIPTS_DIR}/build-graph-tests-images.sh" || exit 1
                ;;
            *)
                echo "  ❌ Unknown build type: $build_type"
                exit 1
                ;;
        esac
    done

    echo ""
    echo "  ✓ All image builds completed successfully!"

    # Load images to Minikube
    load_images_to_minikube
}


if [ "$SKIP_MINIKUBE" == "false" ]; then
    echo "=== 1. Minikube setup ==="
    ${SCRIPT_DIR}/../manage.minikube-for-e2e.sh
else
    echo "Skipping Minikube setup. (SKIP_MINIKUBE=true)"    
fi

if [ "$SKIP_BUILD_IMG" == "false" ]; then
    build_images
else
    echo "=== 2. Skip image build (using latest tag) ==="
fi

if [ "$SKIP_KSERVE_SETUP" == "false" ]; then
    echo ""
    echo "=== 3. Install KServe dependencies ==="
    ${GH_SCRIPTS_DIR}/setup-deps.sh ${DEPLOYMENT_MODE} ${NETWORK_LAYER} ${ENABLE_KEDA} ${LLMISVC}

    echo ""
    echo "=== 4. Update test overlays ==="
    ${GH_SCRIPTS_DIR}/update-test-overlays.sh
    echo "Updated test overlays:"    
    cat ${REPO_ROOT}/config/overlays/test/configmap/inferenceservice.yaml

    echo ""
    echo "=== 5. Install UV ==="
    ${GH_SCRIPTS_DIR}/setup-uv.sh

    echo ""
    echo "=== 6. Install KServe ==="
    ${GH_SCRIPTS_DIR}/setup-kserve.sh
else
    echo "=== 3-6. Skipping KServe setup (SKIP_KSERVE_SETUP=true) ==="
fi

echo ""
echo "=== Check KServe status ==="
kubectl get pods -n kserve
kubectl describe pods -n kserve

echo ""
echo "=== 7. Run E2E tests ==="
${GH_SCRIPTS_DIR}/run-e2e-tests.sh "$MARKER" "$PARALLELISM"

echo ""
echo "==================================================================="
echo "Tests completed!"
echo "==================================================================="
echo ""
echo "=== Check Minikube tunnel logs ==="
if [ -f minikube-tunnel.log ]; then
    echo "Last 10 lines:"
    tail -n 10 minikube-tunnel.log
fi

# echo ""
# echo "=== Check system status ==="
# ./test/scripts/gh-actions/status-check.sh || true

# echo ""
# echo "=== Cleanup ==="
# echo "Stop Minikube tunnel: pkill -f 'minikube tunnel'"
# echo "Delete Minikube: minikube delete"
