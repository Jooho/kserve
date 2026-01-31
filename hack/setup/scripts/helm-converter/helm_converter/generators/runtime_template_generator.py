"""
Runtime Template Generator Module

Generates Helm templates for ClusterServingRuntimes.
"""

from pathlib import Path
from typing import Dict, Any
import yaml
import copy

from .utils import CustomDumper, quote_numeric_strings_in_labels, escape_go_templates_in_resource


class RuntimeTemplateGenerator:
    """Generates templates for ClusterServingRuntimes"""

    def __init__(self, mapping: Dict[str, Any]):
        self.mapping = mapping

    def generate_runtime_templates(self, templates_dir: Path, manifests: Dict[str, Any]):
        """Generate templates for all ClusterServingRuntimes

        Args:
            templates_dir: Path to templates directory
            manifests: Dictionary containing all manifests
        """
        if not manifests.get('runtimes'):
            return

        runtimes_dir = templates_dir / 'runtimes'
        runtimes_dir.mkdir(exist_ok=True)

        for runtime_data in manifests['runtimes']:
            self._generate_runtime_template(runtimes_dir, runtime_data)

    def _generate_runtime_template(self, output_dir: Path, runtime_data: Dict[str, Any]):
        """Generate a single ClusterServingRuntime template

        Uses the full kustomize build result and only substitutes fields defined in mapper:
        - image: replaced with values reference
        - resources: replaced with values reference
        All other fields (env, volumeMounts, volumes, hostIPC, command, etc.) are preserved as-is
        """
        config = runtime_data['config']
        manifest = copy.deepcopy(runtime_data['manifest'])

        runtime_name = config['name']
        enabled_path = config['enabledPath']

        # Escape Go template expressions in the entire manifest (e.g., {{.Name}}, {{.Labels.foo}})
        manifest = escape_go_templates_in_resource(manifest)

        # Modify manifest to use values for configured fields
        if 'image' in config:
            # Replace image with placeholder (will be replaced after yaml.dump)
            # This avoids yaml.dump() adding quotes around Helm template expressions
            img_repo_path = config['image']['repository']['valuePath']
            img_tag_path = config['image']['tag']['valuePath']
            manifest['spec']['containers'][0]['image'] = f"__IMAGE_PLACEHOLDER_{img_repo_path}_{img_tag_path}__"

        if 'resources' in config:
            # Replace resources with Helm toYaml template
            # We'll use a placeholder that we replace after yaml.dump()
            resources_path = config['resources']['valuePath']
            manifest['spec']['containers'][0]['resources'] = f"__RESOURCES_PLACEHOLDER_{resources_path}__"

        # Add Helm labels to metadata
        if 'metadata' not in manifest:
            manifest['metadata'] = {}
        manifest['metadata']['labels'] = "__HELM_LABELS_PLACEHOLDER__"

        # Dump the full manifest to YAML
        manifest_yaml = yaml.dump(manifest, Dumper=CustomDumper, default_flow_style=False, sort_keys=False, width=float('inf'))

        # Quote numeric strings in labels (e.g., "1.0" should stay as string, not become number)
        manifest_yaml = quote_numeric_strings_in_labels(manifest_yaml)

        # Replace placeholders with Helm templates
        manifest_yaml = manifest_yaml.replace(
            'labels: __HELM_LABELS_PLACEHOLDER__',
            f'labels:\n    {{{{- include "{self.mapping["metadata"]["name"]}.labels" . | nindent 4 }}}}'
        )

        if 'image' in config:
            img_repo_path = config['image']['repository']['valuePath']
            img_tag_path = config['image']['tag']['valuePath']
            manifest_yaml = manifest_yaml.replace(
                f'image: __IMAGE_PLACEHOLDER_{img_repo_path}_{img_tag_path}__',
                f'image: {{{{ .Values.{img_repo_path} }}}}:{{{{ .Values.{img_tag_path} }}}}'
            )

        if 'resources' in config:
            resources_path = config['resources']['valuePath']
            manifest_yaml = manifest_yaml.replace(
                f'resources: __RESOURCES_PLACEHOLDER_{resources_path}__',
                f'resources: {{{{- toYaml .Values.{resources_path} | nindent 6 }}}}'
            )

        # Wrap with conditional blocks
        template = f'''{{{{- if .Values.runtimes.enabled }}}}
{{{{- if .Values.{enabled_path} }}}}
{manifest_yaml}{{{{- end }}}}
{{{{- end }}}}
'''

        # Sanitize filename
        filename = runtime_name.replace('kserve-', '') + '.yaml'
        output_file = output_dir / filename

        with open(output_file, 'w') as f:
            f.write(template)
