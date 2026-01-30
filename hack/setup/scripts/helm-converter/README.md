# Kustomize to Helm Converter

A tool to convert KServe's Kustomize manifests to Helm charts.

## 📋 Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Generated Charts](#generated-charts)
- [Version and Image Tag Management](#version-and-image-tag-management)
- [Mapping Files](#mapping-files)
- [Update Workflow](#update-workflow)
- [Advanced Usage](#advanced-usage)
- [How It Works](#how-it-works)
- [Limitations](#limitations)
- [Development](#development)
- [Contributing](#contributing)
- [Troubleshooting](#troubleshooting)
- [Notes](#notes)
- [References](#references)
- [License](#license)

## Overview

### Why this tool?

KServe is built with Kubebuilder and managed with Kustomize manifests. However, users often prefer Helm. This tool:

1. ✅ Keeps Kustomize manifests as the source of truth
2. ✅ Exposes only explicitly defined fields as Helm values via mapping files
3. ✅ Generates identical manifests with default values as Kustomize
4. ✅ Provides sustainable and maintainable conversion
5. ✅ Preserves Go templates in resources using copyAsIs mechanism
6. ✅ Includes automated verification against Kustomize output

### Core Principles

- **Source of Truth**: Kustomize manifests are always the source
- **Explicit Mapping**: Only fields defined in mapping files are exposed as values
- **Identity Guarantee**: Default values produce identical results as Kustomize
- **Safety**: Never overwrites existing charts (requires --force)

## Quick Start

### Generate and Verify Charts

```bash
# Generate and verify all charts at once using Makefile (recommended)
make generate-helm-charts

# Or run steps separately:

# Step 1: Convert Kustomize manifests to Helm charts
make convert-helm-charts

# Step 2: Verify generated charts against Kustomize manifests
make verify-helm-charts

# Or generate individually:

# Generate KServe chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml \
  --output charts/kserve-resources

# Generate LLMISVC chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-llmisvc.yaml \
  --output charts/kserve-llmisvc-resources

# Generate Runtime Configs chart (ClusterServingRuntimes/LLMIsvcConfigs)
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve-runtime-configs.yaml \
  --output charts/kserve-runtime-configs
```

### Overwrite Existing Chart

```bash
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml \
  --output charts/kserve-resources \
  --force
```

## Generated Charts

Three Helm charts are generated:

### 1. kserve-resources chart

Core KServe controller with optional LocalModel support.

```
charts/kserve-resources/
├── Chart.yaml                    # Chart metadata (version, appVersion)
├── values.yaml                   # Default values
└── templates/
    ├── _helpers.tpl              # Helper templates
    ├── NOTES.txt                 # Post-installation notes
    ├── common/
    │   ├── inferenceservice-config.yaml  # ConfigMap template
    │   └── cert-manager-issuer.yaml      # Self-signed Issuer (optional)
    ├── kserve/
    │   ├── deployment.yaml       # KServe controller (templated)
    │   ├── service*.yaml         # Services (static)
    │   ├── *role*.yaml           # RBAC resources (static)
    │   └── *webhook*.yaml        # Webhook configs (static)
    └── localmodel/               # Optional (disabled by default)
        └── deployment.yaml       # LocalModel controller
```

Key configuration options:

- `certManager.enabled`: Install cert-manager self-signed Issuer
- `localmodel.enabled`: Install LocalModel controller (default: false)
- `kserve.controller.image`: Controller image (default: "kserve/kserve-controller")
- `kserve.controller.tag`: Controller image tag (default: "latest")
- `kserve.controller.resources`: Resource limits/requests
```

### 2. kserve-llmisvc-resources chart

Large Language Model Inference Service controller with optional LocalModel support.

```
charts/kserve-llmisvc-resources/
├── Chart.yaml                    # Chart metadata (version, appVersion)
├── values.yaml                   # Default values
└── templates/
    ├── _helpers.tpl              # Helper templates
    ├── NOTES.txt                 # Post-installation notes
    ├── common/                   # Optional (can be disabled)
    │   ├── inferenceservice-config.yaml  # ConfigMap template
    │   └── cert-manager-issuer.yaml      # Self-signed Issuer (optional)
    ├── llmisvc/
    │   ├── deployment.yaml       # LLMISVC controller (templated)
    │   └── ...                   # Other LLMISVC resources
    └── localmodel/               # Optional (disabled by default)
        └── deployment.yaml       # LocalModel controller
```

Key configuration options:

- `certManager.enabled`: Install cert-manager self-signed Issuer
- `localmodel.enabled`: Install LocalModel controller (default: false)
- `llmisvc.controller.image`: Controller image (default: "kserve/llmisvc-controller")
- `llmisvc.controller.tag`: Controller image tag (default: "latest")
- `llmisvc.controller.resources`: Resource limits/requests

```

### 3. kserve-runtime-configs chart

ClusterServingRuntimes and LLMInferenceServiceConfigs for KServe.

```
charts/kserve-runtime-configs/
├── Chart.yaml                    # Chart metadata (version, appVersion)
├── values.yaml                   # Default values
└── templates/
    ├── _helpers.tpl              # Helper templates
    ├── NOTES.txt                 # Post-installation notes
    ├── runtimes/                 # ClusterServingRuntimes (optional)
    │   ├── sklearnserver.yaml    # SKLearn runtime
    │   ├── xgbserver.yaml        # XGBoost runtime
    │   ├── tensorflow-serving.yaml
    │   ├── torchserve.yaml
    │   ├── tritonserver.yaml
    │   ├── lgbserver.yaml
    │   ├── paddleserver.yaml
    │   ├── pmmlserver.yaml
    │   ├── predictiveserver.yaml
    │   ├── mlserver.yaml
    │   ├── huggingfaceserver.yaml
    │   └── huggingfaceserver-multinode.yaml  # 12 runtimes total
    └── llmisvcconfigs/           # LLMInferenceServiceConfigs (optional)
        ├── llm-scheduler.yaml    # LLM scheduler config
        ├── vllm.yaml             # vLLM config
        ├── huggingfaceserver.yaml
        ├── huggingfaceserver-multinode.yaml
        ├── nim.yaml
        ├── sglang.yaml
        ├── tei.yaml
        └── tgi.yaml              # 8 LLM configs total
```

Key configuration options:

- `runtimes.enabled`: Install ClusterServingRuntimes (default: true)
- `runtimes.sklearn.enabled`: Install SKLearn runtime (default: true)
- `runtimes.tensorflow.enabled`: Install TensorFlow runtime (default: true)
- `llmisvcConfigs.enabled`: Install LLMInferenceServiceConfigs (default: false)

**Special handling for LLMInferenceServiceConfigs:**

LLMInferenceServiceConfig resources contain Go template expressions (e.g., `{{ ChildName ... }}`) that are preserved in the generated Helm templates using the `copyAsIs` mechanism. These Go templates are escaped as `{{ "{{" }}` and `{{ "}}" }}` so Helm passes them through unchanged. The resources are read directly from YAML files to preserve their original formatting, including literal block scalars (`|-`).

**Note:** CRDs are managed separately via Makefile and not included in charts.
```


## Version and Image Tag Management

### Updating Chart Version

**IMPORTANT:** Chart versions should match `KSERVE_VERSION` in `kserve-deps.env` to maintain consistency across the project.

Chart version and appVersion are defined in the mapping file metadata:

```yaml
# helm-mapping-kserve.yaml
metadata:
  name: kserve
  description: "KServe - Standard Model Inference Platform on Kubernetes"
  version: "0.1.0"      # Helm chart version (should match KSERVE_VERSION)
  appVersion: "latest"  # Application version (should match KSERVE_VERSION)
```

To update the chart version for a release:

1. **Update `kserve-deps.env` first** (single source of truth):

   ```bash
   # kserve-deps.env
   KSERVE_VERSION=v0.16.0
   ```

2. **Update mapping files** to match:

   ```bash
   # Edit hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml:
   #   version: "0.16.0"
   #   appVersion: "v0.16.0"

   # Edit hack/setup/scripts/helm-converter/mappers/helm-mapping-llmisvc.yaml:
   #   version: "0.16.0"
   #   appVersion: "v0.16.0"

   # Edit hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve-runtime-configs.yaml:
   #   version: "0.16.0"
   #   appVersion: "v0.16.0"
   ```

3. **Regenerate all charts**:

   ```bash
   # Regenerate KServe chart
   python3 hack/setup/scripts/helm-converter/convert.py \
     --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml \
     --output charts/kserve-resources \
     --force

   # Regenerate LLMISVC chart
   python3 hack/setup/scripts/helm-converter/convert.py \
     --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-llmisvc.yaml \
     --output charts/kserve-llmisvc-resources \
     --force

   # Regenerate Runtime Configs chart
   python3 hack/setup/scripts/helm-converter/convert.py \
     --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve-runtime-configs.yaml \
     --output charts/kserve-runtime-configs \
     --force
   ```

**Version Consistency:**

- `kserve-deps.env`: `KSERVE_VERSION=v0.16.0` (source of truth)
- `helm-mapping-*.yaml`: `version: "0.16.0"` and `appVersion: "v0.16.0"` (must match)
- `Chart.yaml`: Generated from mapping files (automatically matches)

### Updating Controller Image Tag

Default image tags are defined in the mapping file:

```yaml
# helm-mapping-kserve.yaml
kserve:
  controllerManager:
    image:
      tag:
        valuePath: kserve.controllerManager.image.tag
        defaultValue: "latest"  # Default tag in values.yaml
```

Users can override image tags at installation time:

```bash
# Install with specific image tag
helm install kserve charts/kserve-resources \
  --set kserve.controllerManager.image.tag=v0.14.0 \
  -n kserve --create-namespace

# Install with custom registry and tag
helm install kserve charts/kserve-resources \
  --set kserve.controllerManager.image.repository=myregistry.io/kserve-controller \
  --set kserve.controllerManager.image.tag=v0.14.0 \
  -n kserve --create-namespace
```

## Mapping Files

### Understanding Mapping Files

Mapping files define which manifest fields should be exposed as Helm values. They act as a bridge between Kustomize manifests and Helm charts.

### Mapping File Structure

```yaml
# Example: helm-mapping-kserve.yaml
metadata:
  name: kserve
  description: "KServe - Standard Model Inference Platform on Kubernetes"
  version: "0.16.0"      # Helm chart version (must match KSERVE_VERSION)
  appVersion: "v0.16.0"  # Application version (must match KSERVE_VERSION)

# KServe controller configuration
kserve:
  enabled:
    valuePath: kserve.enabled
    description: "Enable KServe core inference service controller"

  manifestPath: config/components/kserve

  controllerManager:
    kind: Deployment
    name: kserve-controller-manager
    namespace: kserve
    manifestPath: config/manager/manager.yaml

    image:
      repository:
        path: spec.template.spec.containers[0].image+(:,0)  # Extract repository
        valuePath: kserve.controller.image
      tag:
        path: spec.template.spec.containers[0].image+(:,1)  # Extract tag
        valuePath: kserve.controller.tag
      pullPolicy:
        path: spec.template.spec.containers[0].imagePullPolicy
        valuePath: kserve.controller.imagePullPolicy

    resources:
      path: spec.template.spec.containers[0].resources
      valuePath: kserve.controller.resources
```

**Key Concepts:**

- **`path`**: JSONPath-like expression to extract values from Kustomize manifests
  - Supports dot notation: `spec.template.spec.containers[0].image`
  - Supports array indexing: `containers[0]`
  - Supports split operations: `image+(:,0)` splits by `:` and takes index 0 (repository)
  - Uses `rsplit(':', 1)` for images to handle registry:port correctly
- **`valuePath`**: Destination path in `values.yaml`
- **Source of Truth**: Values are extracted from Kustomize build results, not hardcoded defaults
- **Split Operation**: `+(:,0)` and `+(:,1)` split image strings to separate repository and tag

### Available Mapping Files

The converter uses the following mapping files:

- **[helm-mapping-common.yaml](mappers/helm-mapping-common.yaml)**: Shared configurations (ConfigMap, Issuer)
- **[helm-mapping-kserve.yaml](mappers/helm-mapping-kserve.yaml)**: KServe controller and resources
- **[helm-mapping-llmisvc.yaml](mappers/helm-mapping-llmisvc.yaml)**: LLMISVC controller and resources
- **[helm-mapping-kserve-runtime-configs.yaml](mappers/helm-mapping-kserve-runtime-configs.yaml)**: ClusterServingRuntimes and LLM configs

### Modifying Mapping Files

To expose new fields as Helm values:

1. Edit the appropriate mapping file
2. Add the field with `valuePath` and `defaultValue`
3. Re-run the converter
4. Verify the new field appears in `values.yaml`

```bash
# After modifying mapping file
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml \
  --output charts/kserve-resources \
  --force

# Check new values
cat charts/kserve-resources/values.yaml
```

## Update Workflow

### When Kustomize Manifests Change

```bash
# 1. Modify Kustomize manifest (e.g., config/manager/manager.yaml)
vim config/manager/manager.yaml

# 2. Re-run converter and verification using Makefile (recommended)
make generate-helm-charts

# 3. Verify changes
git diff charts/kserve-resources/
```

### When Exposing New Helm Values

```bash
# 1. Modify mapping file to expose new fields
vim hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml

# 2. Re-run converter
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml \
  --output charts/kserve-resources \
  --force

# 3. Verify new fields in values.yaml
cat charts/kserve-resources/values.yaml

# 4. Verify charts still match Kustomize output
make verify-helm-charts
```

## Advanced Usage

### Installing with Custom Values

Create a custom values file:

```yaml
# custom-values.yaml
kserve:
  controllerManager:
    image:
      repository: my-registry/kserve-controller
      tag: v0.14.0
    resources:
      limits:
        cpu: 200m
        memory: 500Mi
      requests:
        cpu: 100m
        memory: 300Mi
```

Install with custom values:

```bash
helm install kserve charts/kserve-resources \
  -f custom-values.yaml \
  -n kserve --create-namespace
```

### Helm Upgrade and Rollback

```bash
# Upgrade after changing values
helm upgrade kserve charts/kserve-resources \
  -f updated-values.yaml \
  -n kserve

# Check upgrade history
helm history kserve -n kserve

# Rollback to previous version
helm rollback kserve -n kserve

# Rollback to specific revision
helm rollback kserve 2 -n kserve
```


## How It Works

### 1. Manifest Reading
- Uses `kustomize build` to get complete manifests for components
- For ClusterServingRuntimes and LLMInferenceServiceConfigs: reads YAML files directly to preserve formatting
- Parses YAML into separate resources
- Preserves original YAML text for `copyAsIs` resources (like LLMInferenceServiceConfigs)

### 2. Template Generation
- **Deployment**: Templates image, tag, resources from mapping file
- **ConfigMap**: Templates data fields from mapping file
- **ClusterServingRuntimes**: Templates image fields based on mapping
- **LLMInferenceServiceConfigs**: Uses `copyAsIs` mechanism to preserve Go templates
  - Original YAML is read directly to maintain literal block scalars (`|-`)
  - Go template expressions (e.g., `{{ ChildName ... }}`) are escaped as `{{ "{{" }}` and `{{ "}}" }}`
  - Namespace is templated as `{{ .Release.Namespace }}`
- **Other resources**: Static with namespace replaced to `{{ .Release.Namespace }}`
- **Webhooks**: Also replaces service namespace in clientConfig

### 3. Conditional Wrapping
- Main component (kserve/llmisvc): No conditional (always installed)
- LocalModel: Wrapped in `{{- if .Values.localmodel.enabled }}`
- Runtimes: Wrapped in `{{- if .Values.runtimes.enabled }}` and individual enables
- LLMInferenceServiceConfigs: Wrapped in `{{- if .Values.llmisvcConfigs.enabled }}`

### 4. Go Template Preservation
For resources that contain Go templates (LLMInferenceServiceConfigs), the converter:
1. Reads original YAML files directly (not through kustomize build)
2. Extracts the spec section while preserving formatting
3. Escapes Go template delimiters using placeholder replacement:
   - `{{` → `__HELM_OPEN__` → `{{ "{{" }}`
   - `}}` → `__HELM_CLOSE__` → `{{ "}}" }}`
4. This allows Helm to render the template while passing Go templates through unchanged

## Limitations

Current limitations:

1. **Kustomize Features**: Supports basic resources. Complex kustomize patches/overlays may need manual handling
2. **Namespace Creation**: Use `--create-namespace` flag or create namespace beforehand

## Development

### Running Tests

The converter includes two types of testing:

**1. Unit Tests (pytest)**

```bash
# Install dependencies (only PyYAML is required)
pip install -r requirements.txt

# Run all tests
cd hack/setup/scripts/helm-converter
pytest

# Run tests with verbose output
pytest -v

# Run specific test file
pytest tests/test_manifest_reader.py -v

# Run specific test
pytest tests/test_values_generator.py::TestValuesGenerator::test_build_common_values -v
```

Test coverage includes:
- `test_manifest_reader.py`: YAML extends mechanism, deep merge, kustomize build integration
- `test_values_generator.py`: Values generation for common, components, certManager, localmodel
- `test_chart_generator.py`: Chart.yaml, helpers, NOTES.txt, namespace filtering, Issuer template, ConfigMap template

**2. Integration Tests (compare_manifests.py)**

```bash
# Verify generated charts match Kustomize output
make verify-helm-charts

# Or run directly
python3 hack/setup/scripts/helm-converter/compare_manifests.py
```

Integration test coverage:
- KServe standalone and with LocalModel
- LLMISVC standalone and with LocalModel
- ClusterServingRuntimes (12 runtimes)
- LLMInferenceServiceConfigs (8 configs with Go templates)
- Common resources (ConfigMap, Issuer)

### Converter Code Structure

```
hack/setup/scripts/helm-converter/
├── convert.py              # Main entry point
├── compare_manifests.py    # Integration testing tool
├── helm_converter/         # Package directory
│   ├── manifest_reader.py  # Read manifests with kustomize build
│   ├── chart_generator.py  # Generate templates
│   └── values_generator.py # Generate values.yaml
└── tests/                  # Unit test suite
    ├── test_manifest_reader.py
    ├── test_chart_generator.py
    └── test_values_generator.py
```

### Adding New Features

To add new features:

1. **helm_converter/manifest_reader.py**: Add logic to read new manifest types
   - For resources with Go templates, use the `copyAsIs` pattern
   - Read original YAML files directly to preserve formatting
2. **helm_converter/chart_generator.py**: Add logic to generate templates
   - For `copyAsIs` resources, escape Go templates using placeholder replacement
3. **helm_converter/values_generator.py**: Add logic to generate values
4. **mappers/*.yaml**: Update mapping file to define new value paths
   - Add `copyAsIs: true` flag for resources with Go templates
5. **tests/**: Add unit tests to verify the new functionality
6. **compare_manifests.py**: Add integration test scenario to validate against Kustomize output

## Contributing

### Testing Changes

Always test your changes before committing:

```bash
# 1. Run unit tests
cd hack/setup/scripts/helm-converter
pytest -v

# 2. Test with dry run
python3 convert.py \
  --mapping mappers/helm-mapping-kserve.yaml \
  --output /tmp/test-chart \
  --dry-run

# 3. Generate all charts
make convert-helm-charts

# 4. Verify against Kustomize output
make verify-helm-charts

# 5. Validate with Helm
helm lint charts_test/kserve-resources
helm template test charts_test/kserve-resources
```

### Pull Request Checklist

Before submitting a PR:

- [ ] Run all unit tests with `pytest -v`
- [ ] Test chart generation with `make convert-helm-charts`
- [ ] Run integration tests with `make verify-helm-charts`
- [ ] Validate generated charts with `helm lint`
- [ ] Update README.md if adding new features
- [ ] Update mapping files if changing templates
- [ ] Ensure version consistency with `kserve-deps.env`
- [ ] Test copyAsIs mechanism if adding resources with Go templates

## Troubleshooting

### ImportError

```bash
# Install dependencies
pip install -r requirements.txt
```

### Kustomize Not Found

```bash
# Install kustomize
# On macOS
brew install kustomize

# On Linux
hack/setup/cli/install-kustomize.sh
export PATH=/path/to/kserve_root/bin:$PATH
```

### Mapping File Syntax Error

Validate YAML syntax:

```bash
python3 -c "import yaml; yaml.safe_load(open('hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml'))"
```

### Template Rendering Error

Debug with Helm:

```bash
helm template kserve charts/kserve-resources --debug

# Test with custom values
helm template kserve charts/kserve-resources -f custom-values.yaml --debug
```

### Dry Run Fails

View detailed error:

```bash
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml \
  --output /tmp/test \
  --dry-run 2>&1 | less
```

## Notes

### CRDs Management

CRDs are managed separately via Makefile and are **not included** in the Helm charts. Always install CRDs before installing any Helm charts:

```bash
# Install CRDs first (required)
helm install kserve charts/kserve-crd
# Then install Helm charts
helm install kserve charts/kserve-resources -n kserve --create-namespace
```

### LLMInferenceServiceConfig Resources

LLMInferenceServiceConfig resources are now fully supported using the `copyAsIs` mechanism. These resources contain Go template expressions (like `{{ ChildName ... }}`) that are preserved during Helm conversion by escaping them as `{{ "{{" }}` and `{{ "}}" }}`. This allows Helm to render the templates while passing the Go template expressions through unchanged.

When rendered by Helm, these resources will contain literal `{{` and `}}` characters, which are then processed by the KServe controller at runtime.

### Chart Dependencies

The three charts have the following relationship:

- **kserve-resources**: Core controller and common resources
- **kserve-llmisvc-resources**: LLMISVC controller (optional, can disable common resources if kserve-resources is installed)
- **kserve-runtime-configs**: Runtime configurations (optional, can be installed with either chart)

## References

- [Helm Documentation](https://helm.sh/docs/)
- [Kustomize Documentation](https://kustomize.io/)
- [KServe Documentation](https://kserve.github.io/website/)
- [Kubebuilder Documentation](https://book.kubebuilder.io/)

## License

This tool is part of the KServe project and follows the Apache 2.0 license.
