#!/bin/bash
# Script to quickly run a single test
# Usage: ./run-single-test.sh predictor/test_sklearn.py::test_sklearn_v2
set -e

# Docker registry configuration - MUST be set before sourcing common.sh
# because common.sh sources kserve-images.sh which uses KO_DOCKER_REPO
export KO_DOCKER_REPO="${KO_DOCKER_REPO:-kserve}"

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
source "${SCRIPT_DIR}/../../common.sh"

GH_SCRIPTS_DIR="${REPO_ROOT}/test/scripts/gh-actions"

TEST_PATH="${1}"

if [ -z "$TEST_PATH" ]; then
    echo "Usage: $0 <test_path>"
    echo ""
    echo "Examples:"
    echo "  $0 predictor/test_sklearn.py::test_sklearn_v2"
    echo "  $0 predictor/test_sklearn.py"
    echo "  $0 predictor/"
    exit 1
fi

echo "==================================================================="
echo "KServe Single Test Execution"
echo "==================================================================="
echo "Test: $TEST_PATH"
echo "Docker registry: $KO_DOCKER_REPO"
echo "==================================================================="
echo ""

echo "=== Setup Python environment ==="
cd "${REPO_ROOT}"
${GH_SCRIPTS_DIR}/setup-uv.sh

# Activate venv (setup-uv.sh activation doesn't persist to parent shell)
source "${REPO_ROOT}/.venv/bin/activate"

cd "${REPO_ROOT}/python/kserve"
uv sync --active --group test
echo "  ✓ KServe Python SDK installed"

# Move to test directory
cd "${REPO_ROOT}/test/e2e"

echo ""
echo "=== Run test ==="
pytest --ignore=qpext --log-cli-level=INFO "$TEST_PATH"

echo ""
echo "==================================================================="
echo "Test completed!"
echo "==================================================================="
