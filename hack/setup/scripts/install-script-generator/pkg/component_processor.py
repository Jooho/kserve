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

"""Component discovery and processing utilities."""

from pathlib import Path
from typing import Any, Optional

from . import bash_parser
from . import file_reader


# Section markers for extraction
VARIABLES_SECTION_START = '# VARIABLES'
VARIABLES_SECTION_END = '# VARIABLES END'
INCLUDE_SECTION_START = '# INCLUDE_IN_GENERATED_SCRIPT_START'
INCLUDE_SECTION_END = '# INCLUDE_IN_GENERATED_SCRIPT_END'


def find_component_script(component: str, infra_dir: Path, method: Optional[str] = None) -> Optional[Path]:
    """Find component script following naming conventions.

    Search paths in order (when method is specified):
    1. infra_dir/{component}/manage.{component}-{method}.sh
    2. infra_dir/manage.{component}-{method}.sh
    3. infra_dir/{component}/manage.{component}.sh
    4. infra_dir/manage.{component}.sh

    When method is not specified, searches for base name only:
    1. infra_dir/{component}/manage.{component}.sh
    2. infra_dir/manage.{component}.sh

    Args:
        component: Component name
        infra_dir: Infrastructure directory path
        method: Installation method (e.g., "helm", "kustomize"), optional

    Returns:
        Path to component script or None if not found
    """
    search_paths = []

    # If method is specified, prioritize method-specific scripts
    if method:
        search_paths.extend([
            infra_dir / component / f"manage.{component}-{method}.sh",
            infra_dir / f"manage.{component}-{method}.sh",
        ])

    # Then search for base scripts
    search_paths.extend([
        infra_dir / component / f"manage.{component}.sh",
        infra_dir / f"manage.{component}.sh",
    ])

    for path in search_paths:
        if path.exists():
            return path
    return None


def process_component(comp_config: dict[str, Any], infra_dir: Path, method: Optional[str] = None) -> dict[str, Any]:
    """Process single component: find script, extract and rename functions.

    Args:
        comp_config: Component configuration dict with keys:
                     - name: Component name
                     - env: Environment variables for this component
        infra_dir: Infrastructure directory path
        method: Installation method (e.g., "helm", "kustomize"), optional

    Returns:
        Processed component data dict with keys:
        - name: Component name
        - install_func: Renamed install function name
        - uninstall_func: Renamed uninstall function name
        - install_code: Install function code
        - uninstall_code: Uninstall function code
        - variables: List of variable declarations
        - include_section: Code to include in generated script
        - env: Environment variables

    Raises:
        RuntimeError: If script or required functions not found
    """
    name = comp_config["name"]

    script_file = find_component_script(name, infra_dir, method)
    if not script_file:
        raise RuntimeError(f"Script not found for: {name} (method: {method})")

    # Extract functions
    install_raw = bash_parser.extract_bash_function(script_file, "install")
    uninstall_raw = bash_parser.extract_bash_function(script_file, "uninstall")

    if not install_raw:
        raise RuntimeError(f"Function 'install()' not found in: {script_file}")
    if not uninstall_raw:
        raise RuntimeError(f"Function 'uninstall()' not found in: {script_file}")

    # Extract variables and include section
    variables = file_reader.extract_marked_section(
        script_file,
        VARIABLES_SECTION_START,
        VARIABLES_SECTION_END,
        preserve_indent=False,
        skip_empty=True
    )

    include_section = file_reader.extract_marked_section(
        script_file,
        INCLUDE_SECTION_START,
        INCLUDE_SECTION_END,
        preserve_indent=True,
        skip_empty=False
    )

    # Rename functions
    suffix = name.replace("-", "_")
    install_func = f"install_{suffix}"
    uninstall_func = f"uninstall_{suffix}"
    install_code = bash_parser.rename_bash_function(install_raw, "install", install_func)
    uninstall_code = bash_parser.rename_bash_function(uninstall_raw, "uninstall", uninstall_func)

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
