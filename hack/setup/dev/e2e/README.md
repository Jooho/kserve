# KServe Local E2E Test Setup

This directory contains scripts for running KServe E2E tests locally in a Minikube environment, matching the CI/CD pipeline configuration.

## Quick Start

### Using Make (Recommended)

```bash
# Run predictor tests (default)
make test-e2e-local

# Run specific test marker
make test-e2e-local MARKER=graph

# Run with custom parallelism
make test-e2e-local MARKER=graph PARALLELISM=4
```

### Using Script Directly

```bash
# Run predictor tests (default)
./hack/setup/dev/e2e/run-local-e2e.sh

# Run specific test marker
./hack/setup/dev/e2e/run-local-e2e.sh graph

# Run with custom parallelism
./hack/setup/dev/e2e/run-local-e2e.sh predictor 4
```

## Prerequisites

- Minikube installed and configured
- Docker installed
- kubectl installed
- uv (Python package manager)
- Sufficient disk space (~20GB recommended)
- Go toolchain (matching project requirements)

## Using Make Target

### Basic Syntax

```bash
make test-e2e-local [MARKER=<marker>] [PARALLELISM=<num>]
```

### Examples

```bash
# Default (predictor with parallelism 6)
make test-e2e-local

# Specific marker
make test-e2e-local MARKER=graph

# Custom parallelism
make test-e2e-local MARKER=graph PARALLELISM=4

# With environment variables
SKIP_BUILD_IMG=true make test-e2e-local MARKER=predictor
```

## Script: `run-local-e2e.sh`

### Syntax

```bash
./hack/setup/dev/e2e/run-local-e2e.sh [MARKER] [PARALLELISM]
```

**Parameters:**

- `MARKER`: Test marker to run (default: `predictor`)
- `PARALLELISM`: Number of parallel test workers (default: `6`)

### Environment Variables

#### Build Control

```bash
# Docker registry (REQUIRED, default: kserve)
export KO_DOCKER_REPO=kserve

# Custom image tag (default: git commit SHA or 'latest')
export TAG=my-custom-tag

# Skip Minikube setup (use existing cluster)
export SKIP_MINIKUBE=true

# Skip image building (use existing images with 'latest' tag)
export SKIP_BUILD_IMG=true

# Skip KServe installation (use existing installation)
export SKIP_KSERVE_SETUP=true

# Build Huggingface images (default: pull from registry)
export HUGGINGFACE_IMG_BUILD=true
```

#### Deployment Configuration

```bash
# Network layer: istio (default), kourier, istio-ingress, envoy-gatewayapi, istio-gatewayapi
export NETWORK_LAYER=istio

# Enable KEDA autoscaling (default: false)
export ENABLE_KEDA=true

# Deployment mode (default: serverless)
export DEPLOYMENT_MODE=serverless

# LLM InferenceService mode
export LLMISVC=true
```

## Script: `run-single-test.sh`

For quick iteration during development, use `run-single-test.sh` to run a single test or test file without running the full test suite.

### Syntax

```bash
./hack/setup/dev/e2e/run-single-test.sh <test_path>
```

**Parameters:**

- `test_path`: Path to test file or specific test (relative to `test/e2e/`)

### Examples

```bash
# Run a specific test
./hack/setup/dev/e2e/run-single-test.sh predictor/test_sklearn.py::test_sklearn_v2

# Run all tests in a file
./hack/setup/dev/e2e/run-single-test.sh predictor/test_sklearn.py

# Run all tests in a directory
./hack/setup/dev/e2e/run-single-test.sh predictor/
```

### What It Does

The script performs the following steps:

1. Installs UV and creates virtual environment (if not exists)
2. Installs KServe Python SDK and test dependencies
3. Activates virtual environment
4. Runs the specified test with pytest

### Notes

- This script assumes KServe is already deployed in your Minikube cluster
- Use this for quick test iterations after making code changes
- Does NOT build images or setup infrastructure
- For full E2E test runs, use `run-local-e2e.sh` instead

## Test Markers

### Available Markers

The script supports all pytest markers defined in the test suite:

| Marker | Description | Images Built |
|--------|-------------|--------------|
| `predictor` | Predictor tests | base + predictor |
| `transformer` | Transformer tests | base + predictor |
| `explainer` | Explainer tests | base + predictor + explainer |
| `graph` | Graph/ensemble tests | base + predictor + graph |
| `raw` | Raw deployment tests | base + predictor + explainer |
| `llm` | LLM tests | base + predictor |
| `vllm` | vLLM tests | base + predictor |
| `helm` | Helm installation tests | base only |
| `grpc` | gRPC tests | base + predictor |
| `mms` | Multi-model serving | base + predictor |
| `modelcache` | Model cache tests | base + predictor |
| `autoscaling` | Autoscaling tests | base + predictor + explainer |
| `kourier` | Kourier networking | base + predictor + graph |
| `path_based_routing` | Path-based routing | base + predictor + explainer |
| `rawcipn` | Cluster IP None tests | base + predictor + explainer |

### Image Build Types

- **base**: Core KServe images (controller, agent, storage-initializer, router, localmodel)
- **predictor**: Runtime servers (sklearn, xgb, lgb, pmml, paddle, custom-model, transformers)
- **explainer**: Explainer images (ART explainer)
- **graph**: Graph test images (success_200_isvc, error_404_isvc)

## Usage Examples

### Basic Usage

```bash
# Run predictor tests with default settings
make test-e2e-local

# Run graph tests with 4 parallel workers
make test-e2e-local MARKER=graph PARALLELISM=4

# Run explainer tests with single worker
make test-e2e-local MARKER=explainer PARALLELISM=1
```

### Quick Iteration Workflow

```bash
# First run: Full setup
make test-e2e-local

# Subsequent runs: Skip setup, rebuild images, re-run tests
SKIP_MINIKUBE=true SKIP_KSERVE_SETUP=true make test-e2e-local

# Only re-run tests (no rebuild)
SKIP_MINIKUBE=true SKIP_BUILD_IMG=true SKIP_KSERVE_SETUP=true make test-e2e-local
```

### Custom Configuration

```bash
# Test with Kourier networking
NETWORK_LAYER=kourier make test-e2e-local MARKER=kourier

# Test raw deployment with KEDA
DEPLOYMENT_MODE=raw ENABLE_KEDA=true make test-e2e-local MARKER=raw

# Test with custom image tag
TAG=v0.13.0 SKIP_BUILD_IMG=true make test-e2e-local
```

### Development Workflow

```bash
# 1. Make code changes to KServe controller
vim pkg/controller/...

# 2. Rebuild and re-test (skip Minikube setup)
SKIP_MINIKUBE=true make test-e2e-local

# 3. Make changes to runtime server
vim python/kserve/kserve/...

# 4. Rebuild predictor images and test
SKIP_MINIKUBE=true make test-e2e-local MARKER=predictor

# 5. Quick iteration - test only (no rebuild)
SKIP_MINIKUBE=true SKIP_BUILD_IMG=true SKIP_KSERVE_SETUP=true make test-e2e-local
```

## Script Workflow

The script executes the following steps:

1. **Minikube Setup** (unless `SKIP_MINIKUBE=true`)
   - Creates/configures Minikube cluster
   - Starts Minikube tunnel

2. **Image Building** (unless `SKIP_BUILD_IMG=true`)
   - Builds required images based on test marker
   - Loads images into Minikube

3. **KServe Installation** (unless `SKIP_KSERVE_SETUP=true`)
   - Installs dependencies (Istio/Kourier, Knative, Cert-Manager, etc.)
   - Updates test overlays
   - Installs UV and Python dependencies
   - Deploys KServe

4. **Test Execution**
   - Runs pytest with specified marker and parallelism
   - Collects test results

5. **Status Check**
   - Displays KServe pod status
   - Shows Minikube tunnel logs

## Troubleshooting

### Common Issues

#### Disk Space

```bash
# Check available space
df -h

# Clean up Docker images
docker system prune -a

# Clean up Minikube
minikube delete
```

#### Build Failures

```bash
# Check build logs
tail -f /path/to/build/logs

# Rebuild with verbose output
export DEBUG=true
./run-local-e2e.sh predictor
```

#### Test Failures

```bash
# Run with reduced parallelism
./run-local-e2e.sh predictor 1

# Check KServe pod logs
kubectl logs -n kserve -l control-plane=kserve-controller-manager

# Check test pod logs
kubectl get pods -A
kubectl logs <pod-name> -n <namespace>
```

#### Minikube Issues

```bash
# Restart Minikube
minikube stop
minikube start

# Reset Minikube completely
minikube delete
./run-local-e2e.sh predictor
```

### Debug Mode

```bash
# Enable debug output
set -x
./run-local-e2e.sh predictor
```

## Advanced Configuration

### Custom Image Repository

```bash
# Use custom Docker registry
KO_DOCKER_REPO=my-registry.io/kserve make test-e2e-local

# Or with script directly
export KO_DOCKER_REPO=my-registry.io/kserve
./hack/setup/dev/e2e/run-local-e2e.sh predictor

# Use local registry (default for local testing)
KO_DOCKER_REPO=kserve make test-e2e-local
```

### Selective Image Building

The script automatically determines which images to build based on the test marker. To override:

```bash
# Build only base images (not recommended)
# Modify MARKER_EXTRA_DEPS in the script

# Build all images
export MARKER=all  # Not implemented - build multiple markers separately
```

## CI/CD Alignment

This script closely matches the GitHub Actions E2E test workflow (`.github/workflows/e2e-test.yml`):

- Same build scripts
- Same image tags
- Same dependency installation
- Same test execution

Differences:
- Local: Sequential image builds
- CI: Parallel image builds across jobs
- Local: Single Minikube node
- CI: Single or multi-node based on test

## Performance Tips

1. **Use SKIP flags** for faster iterations during development
2. **Reduce parallelism** if experiencing resource constraints
3. **Pre-build images** and use `SKIP_BUILD_IMG=true` for test-only runs
4. **Allocate sufficient resources** to Minikube (CPU, memory)

```bash
# Configure Minikube resources
minikube config set cpus 4
minikube config set memory 8192
```

## Related Scripts

- `manage.minikube-for-e2e.sh`: Minikube cluster management
- `../../common.sh`: Common utilities and environment variables

## Contributing

When adding new test markers:

1. Add marker to pytest test files with `@pytest.mark.marker_name`
2. If the marker requires special images beyond base+predictor:
   - Update `MARKER_EXTRA_DEPS` in `run-local-e2e.sh`
3. If the marker only needs base images:
   - Add to `BASE_ONLY_MARKERS` in `run-local-e2e.sh`
4. Otherwise, no changes needed (defaults to base+predictor)
5. Test locally:
   ```bash
   make test-e2e-local MARKER=your_new_marker
   ```

## Support

For issues or questions:
- Check [KServe documentation](https://kserve.github.io/website/)
- File issues at [KServe GitHub](https://github.com/kserve/kserve/issues)
- Review CI logs for comparison
