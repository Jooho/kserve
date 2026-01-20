# Kustomize to Helm Converter

A tool to convert KServe's Kustomize manifests to Helm charts.

## 📋 Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Architecture](#architecture)
- [Usage](#usage)
- [Generated Charts](#generated-charts)
- [Installation Scenarios](#installation-scenarios)
- [Version and Image Tag Management](#version-and-image-tag-management)
- [Mapping Files](#mapping-files)
- [Update Workflow](#update-workflow)
- [Troubleshooting](#troubleshooting)
- [Development](#development)

## Overview

### Why this tool?

KServe is built with Kubebuilder and managed with Kustomize manifests. However, users often prefer Helm. This tool:

1. ✅ Keeps Kustomize manifests as the source of truth
2. ✅ Exposes only explicitly defined fields as Helm values via mapping files
3. ✅ Generates identical manifests with default values as Kustomize
4. ✅ Provides sustainable and maintainable conversion

### Core Principles

- **Source of Truth**: Kustomize manifests are always the source
- **Explicit Mapping**: Only fields defined in mapping files are exposed as values
- **Identity Guarantee**: Default values produce identical results as Kustomize
- **Safety**: Never overwrites existing charts (requires --force)

## Quick Start

### Prerequisites

```bash
# Python 3.7+ required
python3 --version

# Install dependencies
pip install -r hack/setup/scripts/helm-converter/requirements.txt

# Install kustomize
# See: https://kubectl.docs.kubernetes.io/installation/kustomize/
```

### Test with Dry Run

Dry run allows you to preview what will be generated without creating files.

**Benefits:**

- Validates mapping file syntax
- Shows which templates will be generated
- Helps verify configuration before committing changes
- Useful for CI/CD pipelines to validate PRs

```bash
# Preview KServe chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml \
  --output charts/kserve-resources \
  --dry-run

# Preview LLMISVC chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-llmisvc.yaml \
  --output charts/kserve-llmisvc-resources \
  --dry-run

# Preview Runtime Configs chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve-runtime-configs.yaml \
  --output charts/kserve-runtime-configs \
  --dry-run
```

### Generate Charts

```bash
# Generate all charts at once using Makefile (recommended)
make generate-helm-charts

# Or generate individually:

# Generate KServe chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml \
  --output charts/kserve-resources

# Generate LLMISVC chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-llmisvc.yaml \
  --output charts/kserve-llmisvc-resources

# Generate Runtime Configs chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve-runtime-configs.yaml \
  --output charts/kserve-runtime-configs
```

### Verify Generated Charts

```bash
# Check chart structure
tree charts/kserve-resources

# Test template rendering
helm template kserve charts/kserve-resources

# Run lint check
helm lint charts/kserve-resources

# Render with custom namespace
helm template kserve charts/kserve-resources -n my-namespace

# Render with custom values
helm template kserve charts/kserve-resources -f custom-values.yaml

# Install (dry-run)
helm install kserve charts/kserve-resources --dry-run --debug -n my-namespace
```

## Architecture

The converter uses `kustomize build` to generate complete Kubernetes resources, then:
- Replaces all namespaces with `{{ .Release.Namespace }}`
- Templates configurable fields (images, resources) based on mapping files
- Wraps optional components in Helm conditionals
- Keeps other resources static for reliability

```
helm-converter/
├── convert.py              # Main entry point
├── requirements.txt        # Python dependencies
├── README.md              # This file
├── helm_converter/        # Package directory
│   ├── __init__.py
│   ├── manifest_reader.py # Reads Kustomize manifests via kustomize build
│   ├── chart_generator.py # Generates Helm templates
│   └── values_generator.py# Generates values.yaml
├── mappers/               # Helm mapping configurations
│   ├── helm-mapping-common.yaml
│   ├── helm-mapping-kserve.yaml
│   ├── helm-mapping-llmisvc.yaml
│   └── helm-mapping-kserve-runtime-configs.yaml
└── tests/                 # Test suite
    ├── __init__.py
    ├── test_manifest_reader.py
    ├── test_chart_generator.py
    └── test_values_generator.py
```

## Usage

### Basic Usage

```bash
# Generate KServe chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml \
  --output charts/kserve-resources

# Generate LLMISVC chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-llmisvc.yaml \
  --output charts/kserve-llmisvc-resources

# Generate Runtime Configs chart
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve-runtime-configs.yaml \
  --output charts/kserve-runtime-configs
```

### Dry Run (Preview without creating files)

Use `--dry-run` to preview what will be generated without actually creating files:

- Validates mapping file syntax
- Shows which templates will be generated
- Helps verify configuration before committing changes
- Useful for CI/CD pipelines to validate PRs

```bash
python3 hack/setup/scripts/helm-converter/convert.py \
  --mapping hack/setup/scripts/helm-converter/mappers/helm-mapping-kserve.yaml \
  --output charts/kserve-resources \
  --dry-run
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

- `common.enabled`: Install common resources (ConfigMap)
- `certManager.enabled`: Install cert-manager self-signed Issuer
- `localmodel.enabled`: Install LocalModel controller (default: false)
- `kserve.controllerManager.image.tag`: Controller image tag (default: "latest")
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

- `common.enabled`: Install common resources (default: true, set to false if kserve chart is installed)
- `certManager.enabled`: Install cert-manager self-signed Issuer
- `localmodel.enabled`: Install LocalModel controller (default: false)
- `llmisvc.controllerManager.image.tag`: Controller image tag (default: "latest")
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
    │   └── ...                   # 13 runtimes total
    └── llmisvcconfigs/           # LLMInferenceServiceConfigs (optional)
        ├── vllm.yaml             # vLLM config
        ├── huggingfaceserver.yaml
        └── ...                   # Other LLM configs
```

Key configuration options:

- `runtimes.enabled`: Install ClusterServingRuntimes (default: true)
- `runtimes.sklearn.enabled`: Install SKLearn runtime (default: true)
- `runtimes.tensorflow.enabled`: Install TensorFlow runtime (default: true)
- `llmisvcConfigs.enabled`: Install LLMInferenceServiceConfigs (default: true)
- `llmisvcConfigs.vllm.enabled`: Install vLLM config (default: true)

**Note:** CRDs are managed separately via Makefile and not included in charts.
```

## Helm Conditionals

The converter generates the following conditional structure:

```yaml
# Main component (always installed)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kserve-controller-manager
  namespace: {{ .Release.Namespace }}
...

# ConfigMap (always installed when common.enabled)
{{- if .Values.common.enabled }}
apiVersion: v1
kind: ConfigMap
...
{{- end }}

# Optional LocalModel controller
{{- if .Values.localmodel.enabled }}
apiVersion: apps/v1
kind: Deployment
...
{{- end }}

# Optional ClusterServingRuntimes
{{- if .Values.runtimes.enabled }}
{{- if .Values.runtimes.sklearn.enabled }}
apiVersion: serving.kserve.io/v1alpha1
kind: ClusterServingRuntime
...
{{- end }}
{{- end }}
```

## Generated Chart Testing

```bash
# Verify template rendering
helm template kserve charts/kserve-resources

# Render with custom namespace
helm template kserve charts/kserve-resources -n my-namespace

# Render with custom values
helm template kserve charts/kserve-resources -f custom-values.yaml

# Install (dry-run)
helm install kserve charts/kserve-resources --dry-run --debug -n my-namespace

# Install
helm install kserve charts/kserve-resources -n my-namespace --create-namespace
```

## Installation Scenarios

**IMPORTANT:** Always install CRDs first using the Makefile before installing any Helm charts.

```bash
# Install CRDs first (required)
make install
```

### Scenario 1: KServe Only (Basic Installation)

Install KServe controller with ClusterServingRuntimes.

```bash
# 1. Install CRDs
make install

# 2. Install KServe controller
helm install kserve charts/kserve-resources -n kserve --create-namespace

# 3. Install ClusterServingRuntimes
helm install kserve-runtime-configs charts/kserve-runtime-configs -n kserve
```

### Scenario 2: KServe + LocalModel

Install KServe with LocalModel controller for local model caching.

**Option A: Using --set flag**

```bash
# 1. Install CRDs
make install

# 2. Install KServe with LocalModel enabled
helm install kserve charts/kserve-resources \
  --set localmodel.enabled=true \
  -n kserve --create-namespace

# 3. Install runtimes
helm install kserve-runtime-configs charts/kserve-runtime-configs -n kserve
```

**Option B: Using values file**

```yaml
# kserve-values.yaml
localmodel:
  enabled: true
```

```bash
# 1. Install CRDs
make install

# 2. Install KServe with LocalModel
helm install kserve charts/kserve-resources \
  -f kserve-values.yaml \
  -n kserve --create-namespace

# 3. Install runtimes
helm install kserve-runtime-configs charts/kserve-runtime-configs -n kserve
```

### Scenario 3: KServe + LLMISVC

Install both KServe and LLMISVC controllers together.

**Option A: Using --set flags**

```bash
# 1. Install CRDs
make install

# 2. Install KServe (with common resources)
helm install kserve charts/kserve-resources -n kserve --create-namespace

# 3. Install LLMISVC (disable common resources to avoid conflict)
helm install llmisvc charts/kserve-llmisvc-resources \
  --set common.enabled=false \
  -n kserve

# 4. Install runtimes with both ClusterServingRuntimes and LLMInferenceServiceConfigs
helm install kserve-runtime-configs charts/kserve-runtime-configs -n kserve
```

**Option B: Using values files**

```yaml
# kserve-values.yaml
common:
  enabled: true
certManager:
  enabled: true
```

```yaml
# llmisvc-values.yaml
common:
  enabled: false  # Already installed by KServe chart
certManager:
  enabled: false  # Already installed by KServe chart
```

```bash
# 1. Install CRDs
make install

# 2. Install KServe
helm install kserve charts/kserve-resources \
  -f kserve-values.yaml \
  -n kserve --create-namespace

# 3. Install LLMISVC
helm install llmisvc charts/kserve-llmisvc-resources \
  -f llmisvc-values.yaml \
  -n kserve

# 4. Install runtimes
helm install kserve-runtime-configs charts/kserve-runtime-configs -n kserve
```

### Scenario 4: Only ClusterServingRuntimes (No LLM Configs)

Install only traditional ML runtimes without LLM configs.

```bash
# 1. Install CRDs
make install

# 2. Install KServe
helm install kserve charts/kserve-resources -n kserve --create-namespace

# 3. Install only ClusterServingRuntimes (disable LLM configs)
helm install kserve-runtime-configs charts/kserve-runtime-configs \
  --set llmisvcConfigs.enabled=false \
  -n kserve
```

### Scenario 5: Selective Runtimes Installation

Install only specific runtimes you need.

```yaml
# minimal-runtimes.yaml
runtimes:
  enabled: true
  sklearn:
    enabled: true
  xgboost:
    enabled: true
  tensorflow:
    enabled: false
  pytorch:
    enabled: false
  # ... disable others

llmisvcConfigs:
  enabled: false
```

```bash
# 1. Install CRDs
make install

# 2. Install KServe
helm install kserve charts/kserve-resources -n kserve --create-namespace

# 3. Install selected runtimes only
helm install kserve-runtime-configs charts/kserve-runtime-configs \
  -f minimal-runtimes.yaml \
  -n kserve
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

common:
  enabled:
    valuePath: common.enabled
    defaultValue: true

  inferenceservice-config:
    manifestPath: config/configmap/inferenceservice.yaml
    dataFields:
      - key: deploy
        valuePath: common.config.deploy
        defaultValue: |
          {
            "defaultDeploymentMode": "Serverless"
          }

kserve:
  controllerManager:
    manifestPath: config/manager/manager.yaml
    image:
      repository:
        valuePath: kserve.controllerManager.image.repository
        defaultValue: "kserve/kserve-controller"
      tag:
        valuePath: kserve.controllerManager.image.tag
        defaultValue: "latest"
```

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

# 2. Re-run converter using Makefile
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

### Deploy to Multiple Environments

```bash
# Dev environment
helm install kserve-dev charts/kserve-resources \
  -f values-dev.yaml \
  -n dev --create-namespace

# Staging environment
helm install kserve-staging charts/kserve-resources \
  -f values-staging.yaml \
  -n staging --create-namespace

# Production environment
helm install kserve-prod charts/kserve-resources \
  -f values-prod.yaml \
  -n production --create-namespace
```

## How It Works

### 1. Manifest Reading
- Uses `kustomize build config/components/kserve` to get complete manifests
- Parses YAML into separate resources
- Also reads individual files for templating (e.g., manager deployment)

### 2. Template Generation
- **Deployment**: Templates image, tag, resources from mapping file
- **ConfigMap**: Templates data fields from mapping file
- **Other resources**: Static with namespace replaced to `{{ .Release.Namespace }}`
- **Webhooks**: Also replaces service namespace in clientConfig

### 3. Conditional Wrapping
- Main component (kserve/llmisvc): No conditional (always installed)
- LocalModel: Wrapped in `{{- if .Values.localmodel.enabled }}`
- Runtimes: Wrapped in `{{- if .Values.runtimes.enabled }}` and individual enables

## Limitations

Current limitations:

1. **Kustomize Features**: Supports basic resources. Complex kustomize patches/overlays may need manual handling
2. **CRD Management**: CRDs are managed separately via Makefile and not included in Helm charts
3. **LLMInferenceServiceConfig**: These resources contain Go templates that conflict with Helm and are intentionally skipped
4. **Namespace Creation**: Use `--create-namespace` flag or create namespace beforehand

## Development

### Running Tests

The converter includes a comprehensive test suite using pytest.

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

### Converter Code Structure

```
hack/setup/scripts/helm-converter/
├── convert.py              # Main entry point
├── helm_converter/         # Package directory
│   ├── manifest_reader.py  # Read manifests with kustomize build
│   ├── chart_generator.py  # Generate templates
│   └── values_generator.py # Generate values.yaml
└── tests/                  # Test suite
    ├── test_manifest_reader.py
    ├── test_chart_generator.py
    └── test_values_generator.py
```

### Adding New Features

To add new features:

1. **helm_converter/manifest_reader.py**: Add logic to read new manifest types
2. **helm_converter/chart_generator.py**: Add logic to generate templates
3. **helm_converter/values_generator.py**: Add logic to generate values
4. **mappers/*.yaml**: Update mapping file to define new value paths
5. **tests/**: Add tests to verify the new functionality

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

# 3. Generate test chart
python3 convert.py \
  --mapping mappers/helm-mapping-kserve.yaml \
  --output /tmp/test-chart

# 4. Validate with Helm
helm lint /tmp/test-chart
helm template test /tmp/test-chart
```

### Pull Request Checklist

Before submitting a PR:

- [ ] Run all tests with `pytest -v`
- [ ] Test chart generation with `make generate-helm-charts`
- [ ] Validate generated charts with `helm lint`
- [ ] Update README.md if adding new features
- [ ] Update mapping files if changing templates
- [ ] Ensure version consistency with `kserve-deps.env`

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
curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash
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
make install

# Then install Helm charts
helm install kserve charts/kserve-resources -n kserve --create-namespace
```

### LLMInferenceServiceConfig Resources

LLMInferenceServiceConfig resources contain Go templates that conflict with Helm templating syntax and are intentionally skipped by the converter. These resources should be managed separately via `kubectl` or other tools.

### Chart Dependencies

The three charts have the following relationship:

- **kserve-resources**: Core controller and common resources
- **kserve-llmisvc-resources**: LLMISVC controller (optional, can disable common resources if kserve-resources is installed)
- **kserve-runtime-configs**: Runtime configurations (optional, can be installed with either chart)

When installing multiple charts, ensure common resources are only installed once by setting `common.enabled=false` in subsequent charts.

## References

- [Helm Documentation](https://helm.sh/docs/)
- [Kustomize Documentation](https://kustomize.io/)
- [KServe Documentation](https://kserve.github.io/website/)
- [Kubebuilder Documentation](https://book.kubebuilder.io/)

## License

This tool is part of the KServe project and follows the Apache 2.0 license.
