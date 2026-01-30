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
    """Apply YAML anchors for centralized version management.

    Uses useVersionAnchor flags from mapper to determine which fields should use version anchors.
    Supports both controller tags and inferenceServiceConfig image tags.

    IMPORTANT: kserve.version is the ONLY anchor source for ALL charts.
    - kserve-resources chart: uses existing kserve section
    - kserve-llmisvc-resources chart: adds kserve.version (no llmisvc.version)
    - kserve-localmodel-resources chart: adds kserve.version (no localmodel.version)
    - kserve-runtime-configs chart: adds kserve.version (no runtimes.version)

    Transforms:
      inferenceServiceConfig:
        agent:
          tag: latest
      kserve:
        controller:
          tag: latest

    To:
      inferenceServiceConfig:
        agent:
          tag: *defaultVersion
      kserve:
        version: &defaultVersion v0.16.0
        controller:
          tag: *defaultVersion

    For charts without kserve section (llmisvc/localmodel/runtime):
      llmisvc:
        controller:
          tag: latest

    To:
      kserve:
        version: &defaultVersion v0.16.0
      llmisvc:
        controller:
          tag: *defaultVersion

    Args:
        yaml_content: Original YAML content string
        version: Version string from Chart metadata (e.g., "v0.16.0")
        version_anchor_fields: List of field paths that should use version anchors

    Returns:
        YAML content with version anchors applied
    """
    # kserve.version is the anchor source for ALL charts
    # Even in llmisvc/localmodel/runtime-configs charts, we use kserve.version as the anchor

    # Step 1: Add kserve.version anchor (anchor source)
    # This applies to all charts - kserve.version is always the anchor source
    if 'kserve:\n' in yaml_content:
        # kserve section exists, add version with anchor
        pattern = r'(^|\n)(kserve:)\n'
        yaml_content = re.sub(
            pattern,
            rf'\1\2\n  version: &defaultVersion {version}\n',
            yaml_content,
            count=1,
            flags=re.MULTILINE
        )
    else:
        # kserve section doesn't exist (e.g., in llmisvc/localmodel/runtime charts)
        # Add kserve.version at the top level to define the anchor
        # Insert after the header comments and before the first top-level key
        lines = yaml_content.split('\n')
        insert_index = 0
        for i, line in enumerate(lines):
            # Skip comment lines and empty lines at the start
            if line.strip() and not line.strip().startswith('#'):
                insert_index = i
                break

        # Insert kserve.version before the first non-comment line
        kserve_section = f'kserve:\n  version: &defaultVersion {version}\n'
        lines.insert(insert_index, kserve_section)
        yaml_content = '\n'.join(lines)

    # Apply version anchors to all tracked fields
    for field_path in version_anchor_fields:
        yaml_content = apply_anchor_to_field(yaml_content, field_path, version)

    return yaml_content


def apply_anchor_to_field(yaml_content: str, field_path: str, version: str) -> str:
    """Apply version anchor to a specific field path.

    Args:
        yaml_content: YAML content string
        field_path: Field path (e.g., "kserve.controller.tag" or "inferenceServiceConfig.agent.tag")
        version: Version string

    Returns:
        YAML content with anchor applied to the field
    """
    # Split path into components
    parts = field_path.split('.')

    if len(parts) < 2:
        return yaml_content

    # Build regex pattern based on field path
    # For "kserve.controller.tag" -> match "controller:\n  ...\n  tag: latest"
    # For "inferenceServiceConfig.agent.tag" -> match "agent:\n  ...\n  tag: latest"

    # Get the parent key and field name
    if len(parts) == 2:
        # Simple case: parent.field
        parent_key = parts[0]
        field_name = parts[1]
        pattern = rf'({parent_key}:.*?\n(?:.*?\n)*?.*?{field_name}: )(?:latest|' + re.escape(version) + r')(\n)'
    else:
        # Nested case: a.b.c -> match "b:\n  ...\n  c: latest"
        parent_key = parts[-2]
        field_name = parts[-1]
        pattern = rf'({parent_key}:.*?\n(?:.*?\n)*?.*?{field_name}: )(?:latest|' + re.escape(version) + r')(\n)'

    # Replace with version anchor reference
    yaml_content = re.sub(
        pattern,
        r'\1*defaultVersion\2',
        yaml_content,
        flags=re.MULTILINE
    )

    return yaml_content
