# KServe Installation Script Generator

Modular script generator for creating KServe installation scripts from definition files.

## Architecture

The generator follows a clean separation of concerns with specialized modules:

```
install-script-generator/
├── generator.py              # Main entry point and orchestration (198 lines)
├── pkg/                      # Core modules package
│   ├── definition_parser.py  # Definition file parsing and normalization
│   ├── component_processor.py# Component discovery and processing
│   ├── script_builder.py     # Script content assembly and building
│   ├── file_reader.py        # File reading and section extraction
│   ├── bash_parser.py        # Bash script parsing and function extraction
│   ├── manifest_builder.py   # Kustomize manifest building
│   ├── template_engine.py    # Template processing and placeholder replacement
│   └── logger.py             # Colored logging utilities
├── templates/                # Template files
│   └── generated-script.template
└── tests/                    # Unit tests (86 tests)
    ├── test_bash_parser.py           # 18 tests
    ├── test_component_processor.py   # 14 tests
    ├── test_definition_parser.py     # 10 tests
    ├── test_file_reader.py           # 15 tests
    ├── test_logger.py                # 4 tests
    ├── test_script_builder.py        # 16 tests
    └── test_template_engine.py       # 9 tests
```

### Key Improvements
- **Modular Design**: Each module has a single, well-defined responsibility
- **Reduced Complexity**: Main generator.py reduced from 555 to 198 lines (64% reduction)
- **Testability**: 86 comprehensive unit tests covering all modules
- **Maintainability**: Changes are isolated to specific modules
- **Method-Aware**: Component script discovery respects installation method (helm/kustomize)
- **Flexible Control**: Separate `RELEASE` and `EMBED_MANIFESTS` flags for fine-grained control

## Definition File Options

### RELEASE vs EMBED_MANIFESTS

Two independent flags control different aspects of script generation:

**`RELEASE`** - Controls output filename format:
- `RELEASE: true` → Adds method suffix: `{filename}-{method}.sh` (e.g., `kserve-install-helm.sh`)
- `RELEASE: false` → Base filename only: `{filename}.sh` (e.g., `kserve-install.sh`)
- **Default**: `false`

**`EMBED_MANIFESTS`** - Controls whether to embed KServe manifests:
- `EMBED_MANIFESTS: true` → Embeds KServe CRDs and core manifests in the script
- `EMBED_MANIFESTS: false` → Script uses kustomize/helm at runtime to apply manifests
- **Default**: `false`

**Example combinations**:
```yaml
# Development script - no embedded manifests, simple filename
RELEASE: false
EMBED_MANIFESTS: false
# Output: kserve-install.sh

# Release script - embedded manifests, method-specific filename
RELEASE: true
EMBED_MANIFESTS: true
METHOD: helm
# Output: kserve-install-helm.sh (with embedded manifests)

# Mixed - embedded manifests but simple filename
RELEASE: false
EMBED_MANIFESTS: true
# Output: kserve-install.sh (with embedded manifests)
```

## Usage

```bash
# Generate from single definition file
./generator.py path/to/definition.definition [output-dir]

# Generate from all definitions in directory
./generator.py path/to/definitions/ [output-dir]

# Use default paths (definitions/ → quick-install/)
./generator.py
```

## Running Tests

```bash
python3 -m pytest tests/ -v
```
