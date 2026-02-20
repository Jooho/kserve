SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

source "${SCRIPT_DIR}/../common.sh"

# Track all modified folders
declare -a MODIFIED_FOLDERS=()

# Cleanup function - restore all modified kustomization files
cleanup() {
    for folder in "${MODIFIED_FOLDERS[@]}"; do
        if [ -f "${folder}/kustomization.yaml.bak" ]; then
            mv "${folder}/kustomization.yaml.bak" "${folder}/kustomization.yaml"
        fi
    done
}

# Comment out CRD references in kustomization.yaml
comment_crd(){
    local kustomization_folder="${1}"
    # Only create backup if it doesn't exist (idempotent for failed runs)
    if [ ! -f "${kustomization_folder}/kustomization.yaml.bak" ]; then
        cp "${kustomization_folder}/kustomization.yaml" "${kustomization_folder}/kustomization.yaml.bak"
    fi
    sed -i 's| *- \.\./crd$|# - ../crd|' "${kustomization_folder}/kustomization.yaml"
    sed -i 's| *- \.\./crd/full/localmodel$|# - ../crd/full/localmodel|' "${kustomization_folder}/kustomization.yaml"
    sed -i 's| *- \.\./crd/full/llmisvc$|# - ../crd/full/llmisvc|' "${kustomization_folder}/kustomization.yaml"
    sed -i 's| *- path: cainjection_conversion_webhook\.yaml$|# - path: cainjection_conversion_webhook.yaml|' "${kustomization_folder}/kustomization.yaml"
    MODIFIED_FOLDERS+=("${kustomization_folder}")
}

# Set trap once at the beginning
trap cleanup EXIT ERR INT TERM

# KServe and Common
comment_crd "${REPO_ROOT}/config/default"
kustomize build ${REPO_ROOT}/config/components/kserve > ${REPO_ROOT}/charts/kserve-resources/files/kserve/resources.yaml
kustomize build ${REPO_ROOT}/config/certmanager > ${REPO_ROOT}/charts/kserve-resources/files/common/certmanager.yaml
kustomize build ${REPO_ROOT}/config/configmap > ${REPO_ROOT}/charts/kserve-resources/files/common/configmap.yaml

# LLMISVC Configs and Runtimes
kustomize build ${REPO_ROOT}/config/llmisvcconfig > ${REPO_ROOT}/charts/kserve-runtime-configs/files/llmisvcconfigs/resources.yaml
kustomize build ${REPO_ROOT}/config/runtimes > ${REPO_ROOT}/charts/kserve-runtime-configs/files/runtimes/resources.yaml

# LLMISVC and Common
comment_crd "${REPO_ROOT}/config/llmisvc"
kustomize build ${REPO_ROOT}/config/llmisvc > ${REPO_ROOT}/charts/kserve-llmisvc-resources/files/llmisvc/resources.yaml
kustomize build ${REPO_ROOT}/config/certmanager > ${REPO_ROOT}/charts/kserve-llmisvc-resources/files/common/certmanager.yaml
kustomize build ${REPO_ROOT}/config/configmap > ${REPO_ROOT}/charts/kserve-llmisvc-resources/files/common/configmap.yaml

# LocalModel and Common
comment_crd "${REPO_ROOT}/config/localmodels"
kustomize build ${REPO_ROOT}/config/localmodels > ${REPO_ROOT}/charts/kserve-localmodel-resources/files/resources.yaml
