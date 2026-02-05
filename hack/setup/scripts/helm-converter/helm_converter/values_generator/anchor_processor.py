"""
Anchor Processor Module

Applies YAML anchors for centralized version management in values.yaml.
"""

import re
from typing import List


def apply_version_anchors(
    yaml_content: str,
    version: str,
    version_anchor_fields: List[str],
    chart_name: str = ''
) -> str:
    """Add kserve.version to values.yaml.

    Adds kserve.version field at the beginning of values.yaml.
    Tags that should use version anchors should have value: "" in their mapping.

    Args:
        yaml_content: Original YAML content string
        version: Version string from Chart metadata (e.g., "v0.16.0")
        version_anchor_fields: DEPRECATED - no longer used
        chart_name: Chart name to determine if createSharedResources is needed

    Returns:
        YAML content with kserve.version added
    """
    # Add kserve.version and optionally createSharedResources
    # Charts without common resources don't need createSharedResources
    charts_without_shared_resources = {'kserve-localmodel-resources', 'kserve-runtime-configs'}
    add_shared_resources = chart_name not in charts_without_shared_resources

    if 'kserve:\n' in yaml_content:
        # kserve section exists, add version and optionally createSharedResources
        pattern = r'(^|\n)(kserve:)\n'
        if add_shared_resources:
            replacement = rf'\1\2\n  version: {version}\n  createSharedResources: true\n'
        else:
            replacement = rf'\1\2\n  version: {version}\n'
        yaml_content = re.sub(
            pattern,
            replacement,
            yaml_content,
            count=1,
            flags=re.MULTILINE
        )
    else:
        # kserve section doesn't exist
        # Add kserve.version at the top level
        lines = yaml_content.split('\n')
        insert_index = 0
        for i, line in enumerate(lines):
            # Skip comment lines and empty lines at the start
            if line.strip() and not line.strip().startswith('#'):
                insert_index = i
                break

        # Insert kserve.version and optionally createSharedResources before the first non-comment line
        if add_shared_resources:
            kserve_section = f'kserve:\n  version: {version}\n  createSharedResources: true\n'
        else:
            kserve_section = f'kserve:\n  version: {version}\n'
        lines.insert(insert_index, kserve_section)
        yaml_content = '\n'.join(lines)

    return yaml_content
