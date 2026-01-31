#!/usr/bin/env bash
# Compare old and new Helm chart deployments sequentially on Kind clusters

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../../.." && pwd)"
CHARTS_OLD_DIR="${REPO_ROOT}/charts_ori"
CHARTS_NEW_DIR="${REPO_ROOT}/charts"
OUTPUT_DIR="${SCRIPT_DIR}/comparison_output"

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

log_step() {
    echo -e "${BLUE}[STEP]${NC} $*"
}

# Cleanup function - delete any existing cluster
cleanup_cluster() {
    local cluster_name=$1
    log_info "Cleaning up cluster: ${cluster_name}..."
    kind delete cluster --name "${cluster_name}" 2>/dev/null || true
}

# Create output directory
mkdir -p "${OUTPUT_DIR}"

CERT_MANAGER_SCRIPT="${REPO_ROOT}/hack/setup/infra/manage.cert-manager-helm.sh"

# ============================================================================
# PHASE 1: Process OLD chart
# ============================================================================
log_step "PHASE 1: Testing OLD chart (charts_ori)"
echo ""

# log_info "Creating kind cluster: kserve-test..."
# kind create cluster --name kserve-test --wait 5m

log_info "Installing cert-manager..."
kubectl config use-context kind-kserve-test
bash "${CERT_MANAGER_SCRIPT}"

log_info "Creating kserve namespace..."
kubectl create namespace kserve || true

log_info "Installing OLD charts..."
helm upgrade -i kserve-llmisvc-crd "${CHARTS_OLD_DIR}/kserve-llmisvc-crd" \
    --namespace kserve \
    --create-namespace

log_info "First attempt to install resources (may fail due to webhook)..."
helm upgrade -i kserve-llmisvc-resources "${CHARTS_OLD_DIR}/kserve-llmisvc-resources" \
    --namespace kserve || true

log_info "Waiting for deployments to start..."
sleep 20

log_info "Waiting for deployments to be ready..."
kubectl wait --for=condition=Available --timeout=300s deployment --all -n kserve || true

log_info "Retry installing resources (webhooks should be ready now)..."
helm upgrade -i kserve-llmisvc-resources "${CHARTS_OLD_DIR}/kserve-llmisvc-resources" \
    --namespace kserve

log_info "Final stabilization wait..."
sleep 10

log_info "Exporting resources from OLD chart deployment..."
kubectl get all,configmap,secret,sa,role,rolebinding,clusterrole,clusterrolebinding,certificate,issuer,validatingwebhookconfiguration,mutatingwebhookconfiguration \
    -n kserve \
    -o yaml > "${OUTPUT_DIR}/old-cluster-resources.yaml"

kubectl get crd \
    -o yaml > "${OUTPUT_DIR}/old-cluster-crds.yaml"

log_info "Saving OLD deployment info..."
kubectl get deployment -n kserve -o name > "${OUTPUT_DIR}/old-deployments.txt"
kubectl get service -n kserve -o name > "${OUTPUT_DIR}/old-services.txt"

log_info "Deleting OLD cluster..."
cleanup_cluster kserve-test

echo ""
log_info "Phase 1 complete! OLD chart data saved."
echo ""

# ============================================================================
# PHASE 2: Process NEW chart
# ============================================================================
log_step "PHASE 2: Testing NEW chart (charts)"
echo ""

log_info "Creating kind cluster: kserve-test..."
kind create cluster --name kserve-test --wait 5m

log_info "Installing cert-manager..."
kubectl config use-context kind-kserve-test
bash "${CERT_MANAGER_SCRIPT}"

log_info "Creating kserve namespace..."
kubectl create namespace kserve || true

log_info "Installing NEW charts..."
helm upgrade -i kserve-crd "${CHARTS_NEW_DIR}/kserve-llmisvc-crd" \
    --namespace kserve \
    --create-namespace

log_info "First attempt to install resources (may fail due to webhook)..."
helm upgrade -i kserve-llmisvc-resources "${CHARTS_NEW_DIR}/kserve-llmisvc-resources" \
    --namespace kserve || true

log_info "Waiting for deployments to start..."
sleep 20

log_info "Waiting for deployments to be ready..."
kubectl wait --for=condition=Available --timeout=300s deployment --all -n kserve || true

log_info "Retry installing resources (webhooks should be ready now)..."
helm upgrade -i kserve-llmisvc-resources "${CHARTS_NEW_DIR}/kserve-llmisvc-resources" \
    --namespace kserve

log_info "Final stabilization wait..."
sleep 10

log_info "Exporting resources from NEW chart deployment..."
kubectl get all,configmap,secret,sa,role,rolebinding,clusterrole,clusterrolebinding,certificate,issuer,validatingwebhookconfiguration,mutatingwebhookconfiguration \
    -n kserve \
    -o yaml > "${OUTPUT_DIR}/new-cluster-resources.yaml"

kubectl get crd \
    -o yaml > "${OUTPUT_DIR}/new-cluster-crds.yaml"

log_info "Saving NEW deployment info..."
kubectl get deployment -n kserve -o name > "${OUTPUT_DIR}/new-deployments.txt"
kubectl get service -n kserve -o name > "${OUTPUT_DIR}/new-services.txt"

log_info "Deleting NEW cluster..."
cleanup_cluster kserve-test

echo ""
log_info "Phase 2 complete! NEW chart data saved."
echo ""

# ============================================================================
# PHASE 3: Compare results
# ============================================================================
log_step "PHASE 3: Comparing deployments"
echo ""

# Create a comparison summary
cat > "${OUTPUT_DIR}/comparison-summary.txt" << 'EOF'
============================================================
Helm Chart Deployment Comparison
============================================================

Old Chart: charts_ori/kserve-llmisvc-resources
New Chart: charts/kserve-llmisvc-resources

EOF

# Count resources using exported files
log_info "Counting resources..."

echo "Resource Counts:" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "===============" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "" >> "${OUTPUT_DIR}/comparison-summary.txt"

# Count from YAML files using yq or grep
for resource_type in Deployment Service ServiceAccount ConfigMap Secret Role RoleBinding ClusterRole ClusterRoleBinding Certificate Issuer ValidatingWebhookConfiguration MutatingWebhookConfiguration; do
    old_count=$(grep -c "^kind: ${resource_type}$" "${OUTPUT_DIR}/old-cluster-resources.yaml" 2>/dev/null || echo 0)
    new_count=$(grep -c "^kind: ${resource_type}$" "${OUTPUT_DIR}/new-cluster-resources.yaml" 2>/dev/null || echo 0)

    if [ "$old_count" -eq "$new_count" ]; then
        echo "✓ ${resource_type}: ${old_count} (same)" >> "${OUTPUT_DIR}/comparison-summary.txt"
    else
        echo "✗ ${resource_type}: old=${old_count}, new=${new_count} (DIFFERENT)" >> "${OUTPUT_DIR}/comparison-summary.txt"
    fi
done

echo "" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "CRD Counts:" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "===========" >> "${OUTPUT_DIR}/comparison-summary.txt"
old_crd_count=$(grep -E "(inferenceservices|trainedmodels|inferencegraphs|clusterservingruntimes|servingruntimes|llminferenceserviceconfigs)" "${OUTPUT_DIR}/old-cluster-crds.yaml" 2>/dev/null | grep -c "^  name:" || echo 0)
new_crd_count=$(grep -E "(inferenceservices|trainedmodels|inferencegraphs|clusterservingruntimes|servingruntimes|llminferenceserviceconfigs)" "${OUTPUT_DIR}/new-cluster-crds.yaml" 2>/dev/null | grep -c "^  name:" || echo 0)
echo "Old cluster: ${old_crd_count}" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "New cluster: ${new_crd_count}" >> "${OUTPUT_DIR}/comparison-summary.txt"

# List resource names
echo "" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "Deployments:" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "============" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "Old cluster:" >> "${OUTPUT_DIR}/comparison-summary.txt"
cat "${OUTPUT_DIR}/old-deployments.txt" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "New cluster:" >> "${OUTPUT_DIR}/comparison-summary.txt"
cat "${OUTPUT_DIR}/new-deployments.txt" >> "${OUTPUT_DIR}/comparison-summary.txt"

echo "" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "Services:" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "=========" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "Old cluster:" >> "${OUTPUT_DIR}/comparison-summary.txt"
cat "${OUTPUT_DIR}/old-services.txt" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "" >> "${OUTPUT_DIR}/comparison-summary.txt"
echo "New cluster:" >> "${OUTPUT_DIR}/comparison-summary.txt"
cat "${OUTPUT_DIR}/new-services.txt" >> "${OUTPUT_DIR}/comparison-summary.txt"

log_info "Creating detailed diff..."

# Use python to create a more readable diff
python3 << PYTHON_SCRIPT > "${OUTPUT_DIR}/detailed-diff.txt"
import yaml
import sys
from pathlib import Path

output_dir = Path("${OUTPUT_DIR}")

def load_resources(file_path):
    """Load and normalize resources"""
    with open(file_path, 'r') as f:
        data = yaml.safe_load_all(f)
        resources = {}
        for doc in data:
            if doc and 'kind' in doc and 'metadata' in doc:
                # Skip managed fields and other runtime data
                if 'metadata' in doc:
                    doc['metadata'].pop('managedFields', None)
                    doc['metadata'].pop('creationTimestamp', None)
                    doc['metadata'].pop('resourceVersion', None)
                    doc['metadata'].pop('uid', None)
                    doc['metadata'].pop('generation', None)
                    doc['metadata'].pop('selfLink', None)
                    # Remove helm annotations that will differ
                    if 'annotations' in doc['metadata']:
                        doc['metadata']['annotations'].pop('meta.helm.sh/release-name', None)
                        doc['metadata']['annotations'].pop('meta.helm.sh/release-namespace', None)

                # Create a key for comparison
                kind = doc['kind']
                name = doc['metadata'].get('name', 'unknown')
                namespace = doc['metadata'].get('namespace', 'cluster-scoped')
                key = f"{kind}/{namespace}/{name}"
                resources[key] = doc
        return resources

print("Loading resources...")
old_resources = load_resources(output_dir / 'old-cluster-resources.yaml')
new_resources = load_resources(output_dir / 'new-cluster-resources.yaml')

print(f"Old cluster: {len(old_resources)} resources")
print(f"New cluster: {len(new_resources)} resources")
print()

# Find differences
only_in_old = set(old_resources.keys()) - set(new_resources.keys())
only_in_new = set(new_resources.keys()) - set(old_resources.keys())
common = set(old_resources.keys()) & set(new_resources.keys())

if only_in_old:
    print("Resources only in OLD cluster:")
    for key in sorted(only_in_old):
        print(f"  - {key}")
    print()

if only_in_new:
    print("Resources only in NEW cluster:")
    for key in sorted(only_in_new):
        print(f"  - {key}")
    print()

print(f"Common resources: {len(common)}")
print()

# Check for meaningful differences in common resources
print("Checking common resources for differences...")
print("=" * 60)
different_resources = []
for key in sorted(common):
    old_res = old_resources[key]
    new_res = new_resources[key]

    # Simple comparison (could be enhanced with deep diff)
    if yaml.dump(old_res, sort_keys=True) != yaml.dump(new_res, sort_keys=True):
        different_resources.append(key)

if different_resources:
    print(f"Found {len(different_resources)} resources with differences:")
    for key in different_resources:
        print(f"  - {key}")
else:
    print("✅ All common resources are identical!")

PYTHON_SCRIPT

log_step "Results summary:"
cat "${OUTPUT_DIR}/comparison-summary.txt"

echo ""
log_info "Full comparison saved to: ${OUTPUT_DIR}/"
log_info "  - comparison-summary.txt: Resource counts and names"
log_info "  - detailed-diff.txt: Detailed differences"
log_info "  - old-cluster-resources.yaml: Full dump of old cluster"
log_info "  - new-cluster-resources.yaml: Full dump of new cluster"

echo ""
log_info "✅ Comparison complete!"
log_info "Done!"
