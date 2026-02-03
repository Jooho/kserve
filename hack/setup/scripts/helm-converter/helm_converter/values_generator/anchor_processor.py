"""
Anchor Processor Module

Applies YAML anchors for centralized version management in values.yaml.
"""

import re
from typing import List


def apply_version_anchors(
    yaml_content: str,
    version: str,
    version_anchor_fields: List[str]
) -> str:
    """Apply version management with empty strings for useVersionAnchor fields.

    NEW APPROACH: Use template-level fallback instead of YAML anchors.
    - kserve.version: Plain value (no anchor)
    - kserve.createSharedResources: Plain value (no anchor)
    - useVersionAnchor fields: Empty string ""
    - Templates use: {{ .Values.tag | default .Values.kserve.version }}

    Transforms field values:

      Before:
        kserve:
          controller:
            tag: latest
        inferenceServiceConfig:
          agent:
            tag: latest

      After:
        kserve:
          version: v0.16.0
        inferenceServiceConfig:
          agent:
            tag: ""

    Args:
        yaml_content: Original YAML content string
        version: Version string from Chart metadata (e.g., "v0.16.0")
        version_anchor_fields: List of field paths that should use empty strings

    Returns:
        YAML content with version management applied
    """
    # Step 1: Add kserve.version and createSharedResources (NO anchor)
    if 'kserve:\n' in yaml_content:
        # kserve section exists, add version and createSharedResources without anchor
        pattern = r'(^|\n)(kserve:)\n'
        yaml_content = re.sub(
            pattern,
            rf'\1\2\n  version: {version}\n  createSharedResources: true\n',
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

        # Insert kserve.version and createSharedResources before the first non-comment line
        kserve_section = f'kserve:\n  version: {version}\n  createSharedResources: true\n'
        lines.insert(insert_index, kserve_section)
        yaml_content = '\n'.join(lines)

    # Step 2: Set useVersionAnchor fields to empty string
    for field_path in version_anchor_fields:
        yaml_content = apply_empty_string_to_field(yaml_content, field_path, version)

    return yaml_content


def apply_empty_string_to_field(yaml_content: str, field_path: str, version: str) -> str:
    """Apply empty string to a specific field path for template-level fallback.

    Args:
        yaml_content: YAML content string
        field_path: Field path (e.g., "kserve.controller.tag" or "inferenceServiceConfig.agent.tag")
        version: Version string (used for matching)

    Returns:
        YAML content with empty string applied to the field
    """
    # Split path into components
    parts = field_path.split('.')

    if len(parts) < 2:
        return yaml_content

    # Build regex pattern based on field path
    # For "kserve.controller.tag" -> match "controller:\n  ...\n  tag: latest"
    # For "inferenceServiceConfig.agent.tag" -> match "agent:\n  ...\n  tag: latest"

    # Get the parent key and field name (use last two parts)
    parent_key = parts[-2]
    field_name = parts[-1]
    pattern = rf'({parent_key}:.*?\n(?:.*?\n)*?.*?{field_name}: )(?:latest|' + re.escape(version) + r')(\n)'

    # Replace with empty string
    yaml_content = re.sub(
        pattern,
        r'\1""\2',
        yaml_content,
        flags=re.MULTILINE
    )

    return yaml_content
