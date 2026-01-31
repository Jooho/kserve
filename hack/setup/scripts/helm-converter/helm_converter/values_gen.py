"""
Values Generator Module

Generates values.yaml file from mapping configuration and default values.
"""

import yaml
from pathlib import Path
from typing import Dict, Any

from .values_generator.utils import OrderedDumper, generate_header, print_keys
from .values_generator.configmap_builder import ConfigMapBuilder
from .values_generator.component_builder import ComponentBuilder
from .values_generator.runtime_builder import RuntimeBuilder
from .values_generator.anchor_processor import apply_version_anchors


class ValuesGenerator:
    """Generates values.yaml file for Helm chart"""

    def __init__(self, mapping: Dict[str, Any], manifests: Dict[str, Any], output_dir: Path):
        self.mapping = mapping
        self.manifests = manifests
        self.output_dir = output_dir

        # Initialize builders
        self.configmap_builder = ConfigMapBuilder()
        self.component_builder = ComponentBuilder(mapping)
        self.runtime_builder = RuntimeBuilder(mapping)

    def generate(self):
        """Generate values.yaml file with version anchors"""
        values = self._build_values()
        values_file = self.output_dir / 'values.yaml'

        # Get version from Chart metadata for version anchor
        version = self.mapping['metadata'].get('appVersion', 'latest')

        # Collect all version anchor fields from builders
        version_anchor_fields = []
        version_anchor_fields.extend(self.configmap_builder.version_anchor_fields)
        version_anchor_fields.extend(self.component_builder.version_anchor_fields)
        version_anchor_fields.extend(self.runtime_builder.version_anchor_fields)

        with open(values_file, 'w') as f:
            # Add header comment
            chart_name = self.mapping['metadata']['name']
            description = self.mapping['metadata']['description']
            f.write(generate_header(chart_name, description))

            # Generate YAML content
            yaml_content = yaml.dump(values, Dumper=OrderedDumper,
                                     default_flow_style=False,
                                     sort_keys=False,
                                     width=120,
                                     allow_unicode=True)

            # Apply version anchors for centralized version management
            yaml_content = apply_version_anchors(yaml_content, version, version_anchor_fields)

            # Write final content
            f.write(yaml_content)

    def show_plan(self):
        """Show what values would be generated (dry run)"""
        values = self._build_values()
        print("\n  Sample values structure:")
        print_keys(values, indent=4)

    def _build_values(self) -> Dict[str, Any]:
        """Build the complete values dictionary"""
        values = {}

        # IMPORTANT: Add component-specific values FIRST (especially kserve)
        # This ensures kserve.version anchor is defined before any references to it
        chart_name = self.mapping['metadata']['name']
        if chart_name in self.mapping:
            values[chart_name] = self.component_builder.build_component_values(
                chart_name,
                self.mapping[chart_name],
                self.manifests
            )

        # Add inferenceServiceConfig values (may reference kserve.version anchor)
        if 'inferenceServiceConfig' in self.mapping:
            values['inferenceServiceConfig'] = self.configmap_builder.build_inference_service_config_values(
                self.mapping,
                self.manifests
            )

        # Add certManager values
        if 'certManager' in self.mapping:
            if 'enabled' in self.mapping['certManager']:
                # cert-manager is typically enabled by default (most installations need it)
                values['certManager'] = {
                    'enabled': True
                }

        # Add llmisvcConfigs values if present (even if not main chart)
        if 'llmisvcConfigs' in self.mapping and chart_name != 'llmisvc':
            # For llmisvc configs component, just add enabled flag
            # LLM configs are optional, default to False
            if 'enabled' in self.mapping['llmisvcConfigs']:
                values['llmisvcConfigs'] = {
                    'enabled': False
                }
        # Also support old 'llmisvc' key for backward compatibility
        elif 'llmisvc' in self.mapping and chart_name != 'llmisvc':
            if 'enabled' in self.mapping['llmisvc']:
                values['llmisvcConfigs'] = {
                    'enabled': False
                }

        # Add localmodel values if present
        if 'localmodel' in self.mapping:
            values['localmodel'] = self.component_builder.build_component_values(
                'localmodel',
                self.mapping['localmodel'],
                self.manifests
            )

        # Add localmodelnode values to localmodel section (they belong together)
        if 'localmodelnode' in self.mapping:
            if 'localmodel' not in values:
                values['localmodel'] = {}
            localmodelnode_values = self.component_builder.build_component_values(
                'localmodelnode',
                self.mapping['localmodelnode'],
                self.manifests
            )
            # Merge localmodelnode values into localmodel section
            values['localmodel'].update(localmodelnode_values)

        # Add runtime values (support both clusterServingRuntimes and runtimes keys)
        if 'clusterServingRuntimes' in self.mapping:
            values['runtimes'] = self.runtime_builder.build_runtime_values(
                'clusterServingRuntimes',
                self.manifests
            )
        elif 'runtimes' in self.mapping:
            values['runtimes'] = self.runtime_builder.build_runtime_values(
                'runtimes',
                self.manifests
            )

        return values
