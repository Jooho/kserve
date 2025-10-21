#!/usr/bin/env python3

# Copyright 2025 The KServe Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""KServe Installation Script Generator - Simplified Version

Generates standalone installation scripts from definition files.
Convention-based: all component scripts have install(), uninstall(), and VARIABLES section.
"""

import sys
import re
import yaml
import subprocess
from pathlib import Path
from typing import Optional, Any


# ============================================================================
# Constants
# ============================================================================

YAML_SEPARATOR = '---\n'
VARIABLES_SECTION_START = '# VARIABLES'
VARIABLES_SECTION_END = '# VARIABLES END'
INCLUDE_SECTION_START = '# INCLUDE_IN_GENERATED_SCRIPT_START'
INCLUDE_SECTION_END = '# INCLUDE_IN_GENERATED_SCRIPT_END'


class Colors:
    BLUE = "\033[94m"
    GREEN = "\033[92m"
    RED = "\033[91m"
    RESET = "\033[0m"


# ============================================================================
# Logging
# ============================================================================

def log_info(msg: str):
    print(f"{Colors.BLUE}[INFO]{Colors.RESET} {msg}")


def log_success(msg: str):
    print(f"{Colors.GREEN}[SUCCESS]{Colors.RESET} {msg}")


def log_error(msg: str):
    print(f"{Colors.RED}[ERROR]{Colors.RESET} {msg}", file=sys.stderr)


# ============================================================================
# File & Repository Utilities
# ============================================================================

def find_repo_root(start_dir: Path) -> Path:
    current = start_dir.resolve()
    while current != current.parent:
        if (current / ".git").exists():
            return current
        current = current.parent
    raise RuntimeError("Could not find git repository root")


def read_definition(definition_file: Path) -> dict[str, Any]:
    """Read definition file (YAML or key=value format)."""
    with open(definition_file) as f:
        content = f.read()

    # Try YAML first
    if ":" in content and ("\n  " in content or "COMPONENTS:" in content):
        try:
            config = yaml.safe_load(content)
            return config if config else {}
        except yaml.YAMLError:
            pass

    # Fall back to key=value
    config = {}
    for line in content.split('\n'):
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            config[key] = value.strip().strip('"').strip("'")
    return config


# ============================================================================
# Kustomize & Manifest Processing
# ============================================================================

def run_kustomize_build(kustomize_dir: Path) -> str:
    try:
        result = subprocess.run(
            ["kustomize", "build", str(kustomize_dir)],
            capture_output=True,
            text=True,
            check=True,
            cwd=kustomize_dir.parent
        )
        return result.stdout
    except subprocess.CalledProcessError as e:
        log_error(f"Failed to run kustomize build on {kustomize_dir}: {e}")
        log_error(f"stderr: {e.stderr}")
        raise
    except FileNotFoundError:
        log_error("kustomize command not found")
        raise


def build_core_manifest_without_crd(kustomize_dir: Path) -> str:
    """Build manifest and filter out CRDs."""
    full_manifest = run_kustomize_build(kustomize_dir)
    documents = full_manifest.split(YAML_SEPARATOR)
    filtered_documents = []

    for doc in documents:
        if not doc.strip():
            continue
        is_crd = any('kind:' in line and 'CustomResourceDefinition' in line
                     for line in doc.split('\n'))
        if not is_crd:
            filtered_documents.append(doc)

    return YAML_SEPARATOR.join(filtered_documents)


# ============================================================================
# Component Script Processing
# ============================================================================

def find_component_script(component: str, infra_dir: Path) -> Optional[Path]:
    """Find component script following naming conventions."""
    search_paths = [
        infra_dir / component / f"manage.{component}.sh",
        infra_dir / f"manage.{component}.sh",
        infra_dir / component / f"manage.{component}-helm.sh",
        infra_dir / f"manage.{component}-helm.sh",
    ]

    for path in search_paths:
        if path.exists():
            return path
    return None


def extract_function(script_file: Path, func_name: str) -> str:
    """Extract bash function from script file."""
    lines = []
    in_function = False
    brace_count = 0

    with open(script_file) as f:
        for line in f:
            if line.startswith(f"{func_name}() {{"):
                in_function = True
                brace_count = 0

            if in_function:
                lines.append(line.rstrip())
                brace_count += line.count("{") - line.count("}")
                if brace_count == 0 and len(lines) > 1:
                    break

    return "\n".join(lines)


def rename_function(func_code: str, old_name: str, new_name: str) -> str:
    """Rename bash function."""
    if not func_code:
        return func_code
    lines = func_code.split('\n')
    if lines and lines[0].startswith(f"{old_name}() {{"):
        lines[0] = f"{new_name}() {{"
    return '\n'.join(lines)


def extract_component_variables(script_file: Path) -> list[str]:
    """Extract VARIABLES section from component script."""
    variables = []
    in_section = False

    with open(script_file) as f:
        for line in f:
            stripped = line.strip()
            if stripped == VARIABLES_SECTION_START:
                in_section = True
            elif stripped == VARIABLES_SECTION_END:
                break
            elif in_section and stripped:
                variables.append(stripped)

    return variables


def extract_include_section(script_file: Path) -> list[str]:
    """Extract INCLUDE_IN_GENERATED_SCRIPT section from component script."""
    include_lines = []
    in_section = False

    with open(script_file) as f:
        for line in f:
            stripped = line.strip()
            if stripped == INCLUDE_SECTION_START:
                in_section = True
            elif stripped == INCLUDE_SECTION_END:
                break
            elif in_section:
                include_lines.append(line.rstrip())

    return include_lines


def deduplicate_variables(variables: list[str]) -> list[str]:
    """Remove duplicate variable declarations."""
    seen = set()
    result = []
    for var in variables:
        match = re.match(r"^([A-Z_]+)=", var)
        if match:
            var_name = match.group(1)
            if var_name not in seen:
                seen.add(var_name)
                result.append(var)
    return result


def extract_common_functions(content: str) -> str:
    """Extract utility functions from common.sh."""
    lines = content.split('\n')
    start_idx = next((i for i, line in enumerate(lines) if '# Utility Functions' in line), None)
    end_idx = next((i for i, line in enumerate(lines) if '# Auto-initialization' in line), None)

    if start_idx is not None:
        return '\n'.join(lines[start_idx:end_idx] if end_idx else lines[start_idx:])
    return content


# ============================================================================
# Main Generation Logic
# ============================================================================

def parse_definition(definition_file: Path) -> dict[str, Any]:
    """Parse and normalize definition file."""
    config = read_definition(definition_file)

    # Parse components
    components_data = config.get("COMPONENTS")
    if not components_data:
        raise ValueError(f"Error in {definition_file}: COMPONENTS not found")
    if not isinstance(components_data, list):
        raise ValueError(f"Error in {definition_file}: COMPONENTS must be a list")

    components = []
    for item in components_data:
        if isinstance(item, str):
            components.append({"name": item.strip(), "env": {}})
        elif isinstance(item, dict):
            components.append({
                "name": item.get("name", "").strip(),
                "env": item.get("env", {})
            })

    # Parse tools
    tools_data = config.get("TOOLS", [])
    if isinstance(tools_data, str):
        tools = [t.strip() for t in tools_data.split(",") if t.strip()]
    elif isinstance(tools_data, list):
        tools = [str(t).strip() for t in tools_data]
    else:
        tools = []

    # Parse global_env
    global_env_data = config.get("GLOBAL_ENV", {})
    if isinstance(global_env_data, str):
        global_env = {}
        for pair in global_env_data.split():
            if "=" in pair:
                k, v = pair.split("=", 1)
                global_env[k] = v
    elif isinstance(global_env_data, dict):
        global_env = {k: str(v) for k, v in global_env_data.items()}
    else:
        global_env = {}

    # Parse release
    release_val = config.get("RELEASE", False)
    release = str(release_val).lower() == "true" if isinstance(release_val, str) else release_val

    return {
        "file_name": config.get("FILE_NAME") or definition_file.stem,
        "description": config.get("DESCRIPTION", "Install infrastructure components"),
        "method": config.get("METHOD", "helm"),
        "release": release,
        "tools": tools,
        "global_env": global_env,
        "components": components
    }


def process_component(comp_config: dict[str, Any], infra_dir: Path) -> dict[str, Any]:
    """Process single component: find script, extract and rename functions."""
    name = comp_config["name"]
    log_info(f"Discovering: {name}")

    script_file = find_component_script(name, infra_dir)
    if not script_file:
        raise RuntimeError(f"Script not found for: {name}")

    # Extract functions, variables, and include section
    install_raw = extract_function(script_file, "install")
    uninstall_raw = extract_function(script_file, "uninstall")
    variables = extract_component_variables(script_file)
    include_section = extract_include_section(script_file)

    if not install_raw:
        raise RuntimeError(f"Function 'install()' not found in: {script_file}")
    if not uninstall_raw:
        raise RuntimeError(f"Function 'uninstall()' not found in: {script_file}")

    # Rename functions
    suffix = name.replace("-", "_")
    install_func = f"install_{suffix}"
    uninstall_func = f"uninstall_{suffix}"
    install_code = rename_function(install_raw, "install", install_func)
    uninstall_code = rename_function(uninstall_raw, "uninstall", uninstall_func)

    log_info(f"  → {install_func}(), {uninstall_func}()")

    return {
        "name": name,
        "install_func": install_func,
        "uninstall_func": uninstall_func,
        "install_code": install_code,
        "uninstall_code": uninstall_code,
        "variables": variables,
        "include_section": include_section,
        "env": comp_config["env"]
    }


def generate_script_content(definition_file: Path, config: dict, components: list[dict], repo_root: Path) -> str:
    """Generate complete script content by filling template."""

    # Generate KServe manifest functions if RELEASE mode
    kserve_manifest_functions = ""
    if config["release"]:
        log_info("Release mode enabled - generating embedded KServe manifests...")
        crd_dir = repo_root / "config/crd"
        core_dir = repo_root / "config/default"
        crd_manifest = run_kustomize_build(crd_dir)
        core_manifest = build_core_manifest_without_crd(core_dir)

        kserve_manifest_functions = f'''# ============================================================================
# KServe Manifest Functions (RELEASE MODE)
# ============================================================================

install_kserve_manifest() {{
    log_info "Installing KServe CRDs..."
    get_kserve_crd_manifest | kubectl apply --server-side -f -

    log_info "Installing KServe core components..."
    get_kserve_core_manifest | kubectl apply --server-side -f -

    log_success "KServe manifests installed successfully!"
}}

uninstall_kserve_manifest() {{
    log_info "Uninstalling KServe core components..."
    get_kserve_core_manifest | kubectl delete -f - || true

    log_info "Uninstalling KServe CRDs..."
    get_kserve_crd_manifest | kubectl delete -f - || true

    log_success "KServe manifests uninstalled successfully!"
}}

get_kserve_crd_manifest() {{
    cat <<'KSERVE_CRD_MANIFEST_EOF'
{crd_manifest}KSERVE_CRD_MANIFEST_EOF
}}

get_kserve_core_manifest() {{
    cat <<'KSERVE_CORE_MANIFEST_EOF'
{core_manifest}KSERVE_CORE_MANIFEST_EOF
}}

'''

    # Read common functions
    common_sh = repo_root / "hack/setup/common.sh"
    with open(common_sh) as f:
        common_functions = extract_common_functions(f.read())

    # Read env files
    def read_env_lines(file_path: Path, require_assignment: bool = False) -> list[str]:
        if not file_path.exists():
            return []
        lines = []
        with open(file_path) as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                if require_assignment and "=" not in line:
                    continue
                lines.append(line)
        return lines

    # Collect all variables
    all_variables = []
    for comp in components:
        all_variables.extend(comp["variables"])
    component_variables = "\n".join(deduplicate_variables(all_variables))

    # Generate component sections (with include sections per component)
    component_functions = ""
    for comp in components:
        # Add include section for this component if it exists
        include_section = ""
        if comp["include_section"]:
            include_section = "\n".join(comp["include_section"]) + "\n\n"

        component_functions += f'''# ----------------------------------------
# Component: {comp["name"]}
# ----------------------------------------

{include_section}{comp["uninstall_code"]}

{comp["install_code"]}

'''

    # Generate DEFINITION_GLOBAL_ENV
    definition_global_env = ""
    if config["global_env"]:
        env_lines = [f'    export {k}="{v}"' for k, v in config["global_env"].items()]
        definition_global_env = "\n".join(env_lines)

    # Generate tool install calls
    tool_install_calls = ""
    if config["tools"]:
        cli_dir = repo_root / "hack/setup/cli"
        lines = []
        for tool in config["tools"]:
            tool_script = cli_dir / f"install-{tool}.sh"
            if tool_script.exists():
                lines.append(f'    echo "Installing {tool}..."')
                lines.append(f'    bash "${{REPO_ROOT}}/hack/setup/cli/install-{tool}.sh"')
            else:
                lines.append(f'    echo "Warning: Tool installation script not found: install-{tool}.sh" >&2')
        tool_install_calls = "\n".join(lines)

    # Generate install calls (with env handling)
    install_calls = []
    for comp in components:
        if comp["env"]:
            env_exports = [f'        export {k}="{v}"' for k, v in comp["env"].items()]
            env_code = "\n".join(env_exports)
            install_calls.append(f'    (\n{env_code}\n        {comp["install_func"]}\n    )')
        else:
            install_calls.append(f'    {comp["install_func"]}')
    install_calls_str = "\n".join(install_calls)

    # Generate uninstall calls (reversed order)
    uninstall_calls = "\n".join([f'        {comp["uninstall_func"]}' for comp in reversed(components)])

    # Read template and replace placeholders
    template_path = Path(__file__).parent / "generated-script.template"
    if not template_path.exists():
        raise RuntimeError(f"Script template not found: {template_path}")

    with open(template_path) as f:
        content = f.read()

    placeholders = {
        "{{TEMPLATE_NAME}}": definition_file.name,
        "{{DESCRIPTION}}": config["description"],
        "{{FILE_NAME}}": config["file_name"],
        "{{RELEASE}}": "true" if config["release"] else "false",
        "{{KSERVE_DEPS_CONTENT}}": "\n".join(read_env_lines(repo_root / "kserve-deps.env")),
        "{{GLOBAL_VARS_CONTENT}}": "\n".join(read_env_lines(repo_root / "hack/setup/global-vars.env", require_assignment=True)),
        "{{COMMON_FUNCTIONS}}": common_functions,
        "{{COMPONENT_VARIABLES}}": component_variables,
        "{{KSERVE_MANIFEST_FUNCTIONS}}": kserve_manifest_functions,
        "{{COMPONENT_FUNCTIONS}}": component_functions,
        "{{DEFINITION_GLOBAL_ENV}}": definition_global_env,
        "{{TOOL_INSTALL_CALLS}}": tool_install_calls,
        "{{UNINSTALL_CALLS}}": uninstall_calls,
        "{{INSTALL_CALLS}}": install_calls_str,
    }

    for placeholder, value in placeholders.items():
        content = content.replace(placeholder, value)

    return content


def generate_script(definition_file: Path, output_dir: Path):
    """Main script generation function."""
    log_info(f"Reading definition: {definition_file}")

    # Parse definition
    config = parse_definition(definition_file)

    log_info(f"Output file name: {config['file_name']}")
    log_info(f"Description: {config['description']}")
    if config["tools"]:
        log_info(f"Tools ({len(config['tools'])}): {', '.join(config['tools'])}")
    log_info(f"Components ({len(config['components'])}): {', '.join([c['name'] for c in config['components']])}")

    # Find directories
    script_dir = Path(__file__).parent
    infra_dir = script_dir.parent / "infra"
    repo_root = find_repo_root(script_dir)

    # Process all components
    components = [process_component(comp, infra_dir) for comp in config["components"]]

    # Generate content
    content = generate_script_content(definition_file, config, components, repo_root)

    # Determine output file name
    if config["release"]:
        output_file = output_dir / f"{config['file_name']}-{config['method']}.sh"
    else:
        output_file = output_dir / f"{config['file_name']}.sh"

    # Write output
    log_info(f"Generating: {output_file}")
    with open(output_file, "w") as out:
        out.write(content)
    output_file.chmod(0o755)

    log_success(f"Generated: {output_file}")
    print()
    print("Usage:")
    print(f"  Install:   {output_file}")
    print(f"  Uninstall: {output_file} --uninstall")
    print()


# ============================================================================
# CLI
# ============================================================================

def parse_arguments() -> tuple[Path, Optional[Path]]:
    script_dir = Path(__file__).parent
    default_input_dir = script_dir.parent / "quick-install/definitions"
    default_output_dir = script_dir.parent / "quick-install"

    if len(sys.argv) > 3:
        print(f"Usage: {sys.argv[0]} [definition-file-or-directory] [output-directory]")
        print()
        print("Defaults:")
        print(f"  Input:  {default_input_dir}")
        print(f"  Output: {default_output_dir}")
        sys.exit(1)

    if len(sys.argv) == 1:
        return default_input_dir, default_output_dir

    input_path = Path(sys.argv[1])
    output_dir = Path(sys.argv[2]) if len(sys.argv) == 3 else None
    return input_path, output_dir


def collect_definition_files(input_path: Path, output_dir: Optional[Path]) -> tuple[list[Path], Path]:
    if not input_path.exists():
        log_error(f"Path not found: {input_path}")
        sys.exit(1)

    definition_files = []

    if input_path.is_file():
        definition_files.append(input_path)
        output_dir = output_dir or input_path.parent
    elif input_path.is_dir():
        definition_files.extend(input_path.glob("*.definition"))
        for subdir in input_path.iterdir():
            if subdir.is_dir():
                definition_files.extend(subdir.glob("*.definition"))
        definition_files = sorted(set(definition_files))
        output_dir = output_dir or input_path
    else:
        log_error(f"Invalid path: {input_path}")
        sys.exit(1)

    if not definition_files:
        log_error(f"No .definition files found in: {input_path}")
        sys.exit(1)

    if not output_dir.exists():
        log_error(f"Output directory not found: {output_dir}")
        sys.exit(1)

    return definition_files, output_dir


def main():
    input_path, output_dir = parse_arguments()
    definition_files, output_dir = collect_definition_files(input_path, output_dir)

    failed = 0
    for definition_file in definition_files:
        try:
            generate_script(definition_file, output_dir)
        except Exception as e:
            log_error(f"Generation failed for {definition_file}: {e}")
            import traceback
            traceback.print_exc()
            failed += 1

    print("=" * 70)
    print("Generation Summary")
    print("=" * 70)
    print(f"Total files:  {len(definition_files)}")
    print(f"✅ Success:   {len(definition_files) - failed}")
    print(f"❌ Failed:    {failed}")
    print()

    if failed > 0:
        sys.exit(1)


if __name__ == "__main__":
    main()
