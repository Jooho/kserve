"""
Utilities Module

Common utilities for values generation including YAML dumping,
header generation, and display functions.
"""

import yaml
from typing import Dict, Any


# Custom YAML representer to handle dict in order
class OrderedDumper(yaml.SafeDumper):
    """YAML dumper that preserves dictionary order"""
    pass


def dict_representer(dumper, data):
    """Represent dict as ordered mapping"""
    return dumper.represent_mapping(
        yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG,
        data.items()
    )


# Register the representer
OrderedDumper.add_representer(dict, dict_representer)


def generate_header(chart_name: str, description: str) -> str:
    """Generate header comment for values.yaml.

    Args:
        chart_name: Name of the Helm chart
        description: Chart description

    Returns:
        Header comment string
    """
    return f"""# Default values for {chart_name}
# This is a YAML-formatted file.
# Declare variables to be passed into your templates.

# {description}

# NOTE: This file was auto-generated from {chart_name} mapping configuration.
# Source of truth: Kustomize manifests in config/

"""


def print_keys(d: Dict[str, Any], indent: int = 0):
    """Print dictionary keys recursively for dry run output.

    Args:
        d: Dictionary to print
        indent: Current indentation level
    """
    for key, value in d.items():
        if isinstance(value, dict):
            print(' ' * indent + f'- {key}:')
            print_keys(value, indent + 2)
        else:
            print(' ' * indent + f'- {key}: ...')
