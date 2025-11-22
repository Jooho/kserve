# Predictive Server

Unified model serving runtime for KServe that supports multiple ML frameworks (scikit-learn, XGBoost, LightGBM) in a single Docker image.

## Overview

Predictive Server is a **wrapper runtime** that unifies three existing KServe model servers into a single deployable unit:

- **Scikit-learn** (`sklearnserver`): Support for `.joblib`, `.pkl`, and `.pickle` model files
- **XGBoost** (`xgbserver`): Support for `.bst`, `.json`, and `.ubj` model files
- **LightGBM** (`lgbserver`): Support for `.bst` model files

Instead of maintaining separate Docker images and deployments for each framework, Predictive Server provides a single runtime that delegates to the appropriate framework server based on the `--framework` argument. This approach:

- **Eliminates code duplication**: Reuses existing, well-tested server implementations
- **Simplifies deployment**: One Docker image instead of three
- **Maintains compatibility**: Uses the same underlying servers as individual runtimes
- **Reduces maintenance**: Changes to individual servers automatically propagate

## Features

- **Multi-framework support**: Single runtime for sklearn, XGBoost, and LightGBM
- **KServe integration**: Full compatibility with KServe inference protocol
- **Multi-model serving**: Support for dynamic model loading via ModelRepository
- **Thread control**: Configurable thread count for XGBoost and LightGBM
- **Probability predictions**: Support for `predict_proba` in scikit-learn (via `PREDICT_PROBA` environment variable)

## Dependencies

Predictive Server depends on the following KServe components:

- **kserve** (>=0.16.0): Core KServe inference protocol and model serving framework
- **kserve-storage** (>=0.16.0): Storage abstraction for model loading
- **sklearnserver** (>=0.16.0): Scikit-learn model server
- **xgbserver** (>=0.16.0): XGBoost model server
- **lgbserver** (>=0.16.0): LightGBM model server

## Installation

```bash
# Install dependencies using uv
cd python/predictiveserver
uv sync

# Or install in development mode
pip install -e .
```

## Usage

### Basic Usage

Start the server with a specific framework:

```bash
# Scikit-learn model
python -m predictiveserver --model_name sklearn-model --model_dir /path/to/sklearn/model --framework sklearn

# XGBoost model
python -m predictiveserver --model_name xgb-model --model_dir /path/to/xgboost/model --framework xgboost

# LightGBM model
python -m predictiveserver --model_name lgb-model --model_dir /path/to/lightgbm/model --framework lightgbm
```

### Command-line Arguments

- `--model_name`: Name of the model (required)
- `--model_dir`: Directory containing the model file (required)
- `--framework`: ML framework to use - `sklearn`, `xgboost`, or `lightgbm` (default: `sklearn`)
- `--nthread`: Number of threads for XGBoost/LightGBM (default: `1`)

### Environment Variables

- `PREDICT_PROBA`: Set to `"true"` to use `predict_proba()` for scikit-learn models (default: `"false"`)

### Multi-model Serving

Predictive Server supports multi-model serving through the ModelRepository:

```bash
# Start with an empty model repository
python -m predictiveserver --model_name example --model_dir /mnt/models --framework sklearn
```

Models can then be loaded dynamically using KServe's TrainedModel API.

## KServe Deployment

### Using ClusterServingRuntime

Deploy a model using the Predictive Server runtime. The framework is automatically detected from `modelFormat.name`:

```yaml
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: sklearn-iris
spec:
  predictor:
    model:
      modelFormat:
        name: sklearn
      storageUri: gs://kfserving-examples/models/sklearn/1.0/model
```

```yaml
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: xgboost-iris
spec:
  predictor:
    model:
      modelFormat:
        name: xgboost
      storageUri: gs://kfserving-examples/models/xgboost/1.0/model
```

```yaml
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: lightgbm-iris
spec:
  predictor:
    model:
      modelFormat:
        name: lightgbm
      storageUri: gs://kfserving-examples/models/lightgbm/1.0/model
```

**Note**: The KServe controller automatically adds a `serving.kserve.io/model-framework` annotation based on `modelFormat.name`. This annotation is then passed to the container via the `--framework` argument, telling Predictive Server which underlying framework server to use. You don't need to add any labels or annotations manually.

## Model File Requirements

### Scikit-learn
- Single model file with extension: `.joblib`, `.pkl`, or `.pickle`
- Saved using `joblib.dump()` or `pickle.dump()`

### XGBoost
- Single model file with extension: `.bst`, `.json`, or `.ubj`
- Saved using `booster.save_model()`

### LightGBM
- Single model file with extension: `.bst`
- Saved using `booster.save_model()`

**Note**: Only one model file is allowed per model directory.

## Architecture

```
predictiveserver/
├── __init__.py              # Package initialization
├── __main__.py              # Entry point with CLI argument parsing
├── model.py                 # PredictiveServerModel - unified model wrapper
└── model_repository.py      # PredictiveServerModelRepository - multi-model support
```

### Design Pattern

The Predictive Server uses a **facade/wrapper pattern** where:

1. `PredictiveServerModel` acts as a unified interface
2. Delegates to existing framework-specific servers (`sklearnserver`, `xgbserver`, `lgbserver`)
3. Framework selection happens at initialization based on `--framework` argument
4. All framework models implement the same KServe `Model` interface
5. Avoids code duplication by reusing existing, well-tested server implementations

## Development

### Running Tests

```bash
# Run all tests
pytest

# Run with coverage
pytest --cov=predictiveserver --cov-report=term-missing
```

### Code Formatting

```bash
# Format code with black
black predictiveserver/

# Type checking with mypy
mypy predictiveserver/
```

## Docker

Build the Docker image from the repository root:

```bash
# From kserve repository root
cd python
docker build -t predictiveserver:latest -f predictiveserver.Dockerfile .
```

Or use the Makefile:

```bash
# From kserve repository root
make docker-build-predictive
make docker-push-predictive
```

Run the container:

```bash
docker run -p 8080:8080 \
  -v /path/to/models:/mnt/models \
  predictiveserver:latest \
  --model_name my-model \
  --model_dir /mnt/models \
  --framework sklearn
```

## Comparison with Individual Runtimes

| Feature | Individual Runtimes | Predictive Server |
|---------|-------------------|-----------|
| Docker Images | 3 separate images | 1 unified image |
| Deployment Complexity | Higher (manage 3 runtimes) | Lower (single runtime) |
| Image Size | Smaller per image | Slightly larger (all deps) |
| Framework Switching | Requires redeployment | Simple arg change |
| Maintenance | 3 codebases to maintain | 1 codebase |

## License

Apache License 2.0

## Contributing

Contributions are welcome! Please ensure:

1. All tests pass
2. Code is formatted with `black`
3. Type hints are provided
4. Documentation is updated
