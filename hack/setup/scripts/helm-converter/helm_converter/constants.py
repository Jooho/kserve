"""Constants for helm-converter

Centralized location for hardcoded values to improve maintainability.
"""

# Main component names that are always enabled (no conditional wrapping needed)
MAIN_COMPONENTS = ['kserve', 'llmisvc', 'localmodel', 'localmodelnode']

# KServe core CRDs managed by kserve-crd chart
# These CRDs should be skipped when generating component templates
KSERVE_CORE_CRDS = {
    'clusterservingruntimes.serving.kserve.io',
    'inferencegraphs.serving.kserve.io',
    'inferenceservices.serving.kserve.io',
    'servingruntimes.serving.kserve.io',
    'trainedmodels.serving.kserve.io',
    'inferencepools.serving.kserve.io',
    'clusterstoragecontainers.serving.kserve.io',
    'localmodelnodes.serving.kserve.io',
    'localmodelnodegroups.serving.kserve.io',
    'clustertrainedmodels.serving.kserve.io'
}

# Component-specific CRDs that should go to crds/ directory
# NOTE: localmodel CRDs are managed separately by user (kserve-localmodel-crd chart)
COMPONENT_SPECIFIC_CRDS = {
    'llmisvc': {
        'inferenceobjectives.inference.networking.x-k8s.io',
        'inferencepoolimports.inference.networking.x-k8s.io',
        'inferencepools.inference.networking.k8s.io',
        'inferencepools.inference.networking.x-k8s.io'
    }
}
