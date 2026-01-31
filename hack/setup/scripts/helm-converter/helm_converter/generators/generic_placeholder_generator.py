"""
Generic Placeholder Generator Module

Generates Helm templates for simple resource types (ClusterServingRuntime, LLMInferenceServiceConfig, etc.)
using a generic placeholder-based approach.
"""

from pathlib import Path
from typing import Dict, Any
import yaml
import copy

from .utils import CustomDumper, quote_numeric_strings_in_labels, escape_go_templates_in_resource


class GenericPlaceholderGenerator:
    """Generates templates for simple resource types using placeholder substitution"""

    def __init__(self, mapping: Dict[str, Any]):
        self.mapping = mapping

    def generate_templates(self, templates_dir: Path, resource_list: list, subdir_name: str):
        """Generate templates for a list of resources

        Args:
            templates_dir: Path to templates directory
            resource_list: List of resource data dicts [{config, manifest, ...}, ...]
            subdir_name: Subdirectory name (e.g., 'runtimes', 'llmisvcconfigs')
        """
        if not resource_list:
            return

        output_dir = templates_dir / subdir_name
        output_dir.mkdir(exist_ok=True)

        for resource_data in resource_list:
            self._generate_single_template(output_dir, resource_data, subdir_name)

    def _generate_single_template(self, output_dir: Path, resource_data: Dict[str, Any], subdir_name: str):
        """Generate a single resource template

        Uses placeholder substitution to replace specific fields with Helm values references,
        while preserving all other fields as-is.

        Args:
            output_dir: Output directory for the template
            resource_data: Resource data dict with 'config', 'manifest', etc.
            subdir_name: Subdirectory name for context
        """
        config = resource_data['config']
        manifest = copy.deepcopy(resource_data['manifest'])
        copy_as_is = resource_data.get('copyAsIs', False)

        resource_name = config['name']

        # Step 1: Escape Go template expressions
        # All resources may contain Go templates that should be preserved for runtime evaluation
        # We always escape them so Helm doesn't try to process them
        manifest = escape_go_templates_in_resource(manifest)

        # Step 2: Replace configured fields with placeholders
        placeholders = {}

        # Handle image field (repository + tag)
        if 'image' in config:
            img_repo_path = config['image']['repository']['valuePath']
            img_tag_path = config['image']['tag']['valuePath']
            placeholder_key = f'__IMAGE_PLACEHOLDER_{img_repo_path}_{img_tag_path}__'

            # Set placeholder in manifest
            # Note: Assuming spec.containers[0].image for most resources
            if 'spec' in manifest and 'containers' in manifest['spec']:
                manifest['spec']['containers'][0]['image'] = placeholder_key
                placeholders[placeholder_key] = f'{{{{ .Values.{img_repo_path} }}}}:{{{{ .Values.{img_tag_path} }}}}'

        # Handle resources field
        if 'resources' in config:
            resources_path = config['resources']['valuePath']
            placeholder_key = f'__RESOURCES_PLACEHOLDER_{resources_path}__'

            # Set placeholder in manifest
            if 'spec' in manifest and 'containers' in manifest['spec']:
                manifest['spec']['containers'][0]['resources'] = placeholder_key
                placeholders[placeholder_key] = f'{{{{- toYaml .Values.{resources_path} | nindent 6 }}}}'

        # Step 3: Add Helm labels to metadata
        if 'metadata' not in manifest:
            manifest['metadata'] = {}
        manifest['metadata']['labels'] = '__HELM_LABELS_PLACEHOLDER__'

        # Add namespace for copyAsIs resources (LLMInferenceServiceConfig)
        if copy_as_is:
            manifest['metadata']['namespace'] = '__NAMESPACE_PLACEHOLDER__'

        # Step 4: Dump manifest to YAML
        manifest_yaml = yaml.dump(manifest, Dumper=CustomDumper, default_flow_style=False, sort_keys=False, width=float('inf'))

        # Quote numeric strings in labels
        manifest_yaml = quote_numeric_strings_in_labels(manifest_yaml)

        # Step 5: Replace placeholders with Helm templates
        manifest_yaml = manifest_yaml.replace(
            'labels: __HELM_LABELS_PLACEHOLDER__',
            f'labels:\n    {{{{- include "{self.mapping["metadata"]["name"]}.labels" . | nindent 4 }}}}'
        )

        if copy_as_is:
            manifest_yaml = manifest_yaml.replace(
                'namespace: __NAMESPACE_PLACEHOLDER__',
                'namespace: {{ .Release.Namespace }}'
            )

        for placeholder_key, helm_template in placeholders.items():
            if placeholder_key.startswith('__IMAGE_'):
                manifest_yaml = manifest_yaml.replace(f'image: {placeholder_key}', f'image: {helm_template}')
            elif placeholder_key.startswith('__RESOURCES_'):
                manifest_yaml = manifest_yaml.replace(f'resources: {placeholder_key}', f'resources: {helm_template}')

        # Step 6: Wrap with conditional blocks
        template = self._wrap_with_conditionals(manifest_yaml, config, subdir_name)

        # Step 7: Write template file
        filename = self._get_output_filename(resource_name, resource_data)
        output_file = output_dir / filename

        with open(output_file, 'w') as f:
            f.write(template)

    def _wrap_with_conditionals(self, manifest_yaml: str, config: Dict[str, Any], subdir_name: str) -> str:
        """Wrap manifest YAML with Helm conditional blocks

        Args:
            manifest_yaml: YAML string of the manifest
            config: Resource configuration
            subdir_name: Subdirectory name (runtimes or llmisvcconfigs)

        Returns:
            Template string with conditional wrapping
        """
        # For runtimes: dual conditional (global enabled + individual enabled)
        if subdir_name == 'runtimes':
            enabled_path = config.get('enabledPath')
            if enabled_path:
                return f'''{{{{- if .Values.runtimes.enabled }}}}
{{{{- if .Values.{enabled_path} }}}}
{manifest_yaml}{{{{- end }}}}
{{{{- end }}}}
'''
            else:
                return f'''{{{{- if .Values.runtimes.enabled }}}}
{manifest_yaml}{{{{- end }}}}
'''

        # For llmisvcConfigs: single conditional
        elif subdir_name == 'llmisvcconfigs':
            return f'''{{{{- if .Values.llmisvcConfigs.enabled }}}}
{manifest_yaml}{{{{- end }}}}
'''

        # Default: no conditional
        return manifest_yaml

    def _get_output_filename(self, resource_name: str, resource_data: Dict[str, Any]) -> str:
        """Get output filename for resource

        Args:
            resource_name: Resource name
            resource_data: Resource data dict

        Returns:
            Output filename
        """
        copy_as_is = resource_data.get('copyAsIs', False)
        original_filename = resource_data.get('original_filename')

        # Use original filename if copyAsIs is True
        if copy_as_is and original_filename:
            return original_filename

        # Otherwise, sanitize resource name
        filename = resource_name.replace('kserve-config-', '').replace('kserve-', '') + '.yaml'
        return filename
