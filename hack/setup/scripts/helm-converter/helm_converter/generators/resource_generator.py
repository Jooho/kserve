"""
Resource generator for Helm charts
Handles generation of Service, RBAC, Webhook, and other simple resource templates
"""
from typing import Dict, Any
from pathlib import Path
from .utils import add_kustomize_labels, quote_label_value_if_needed
import yaml


class ResourceGenerator:
    """Generator for simple Kubernetes resource templates"""

    def __init__(self, mapping: Dict[str, Any]):
        """Initialize ResourceGenerator

        Args:
            mapping: Chart mapping configuration
        """
        self.mapping = mapping

    def generate_issuer(self, output_dir: Path, manifests: Dict[str, Any]) -> None:
        """Generate cert-manager Issuer template

        Args:
            output_dir: Output directory for template
            manifests: Manifest data
        """
        if 'certManager-issuer' not in manifests.get('common', {}):
            return

        issuer_manifest = manifests['common']['certManager-issuer']
        issuer_config = self.mapping['certManager']['issuer']

        # Issuer controlled by certManager.enabled only
        template = f'''{{{{- if .Values.certManager.enabled }}}}
apiVersion: {issuer_manifest['apiVersion']}
kind: {issuer_manifest['kind']}
metadata:
  name: {issuer_manifest['metadata']['name']}
  namespace: {{{{ .Release.Namespace }}}}
spec:
  selfSigned: {{}}
{{{{- end }}}}
'''
        output_file = output_dir / f'issuer_{issuer_config["name"]}.yaml'
        with open(output_file, 'w') as f:
            f.write(template)

    def generate_simple_resource(self, output_dir: Path, resource_name: str,
                                manifest: Dict[str, Any], component_name: str,
                                component_data: Dict[str, Any]) -> None:
        """Generate simple resource templates (Service, Role, RoleBinding, etc.)

        Args:
            output_dir: Output directory for template
            resource_name: Name of the resource type
            manifest: Resource manifest
            component_name: Component name
            component_data: Component configuration and data
        """
        config = component_data['config']
        chart_name = self.mapping['metadata']['name']

        # Main component (kserve/llmisvc) is always installed, localmodel needs enabled check
        is_main_component = component_name in [chart_name, 'kserve', 'llmisvc']
        enabled_path = None if is_main_component else config['enabled']['valuePath']

        # Build template
        lines = []

        # Add conditional wrapper for non-main components
        if not is_main_component and enabled_path:
            lines.append(f'{{{{- if .Values.{enabled_path} }}}}')

        # Add apiVersion, kind, metadata
        lines.append(f'apiVersion: {manifest["apiVersion"]}')
        lines.append(f'kind: {manifest["kind"]}')
        lines.append('metadata:')
        lines.append(f'  name: {manifest["metadata"]["name"]}')

        if 'namespace' in manifest['metadata']:
            lines.append('  namespace: {{ .Release.Namespace }}')

        # Add labels
        if 'labels' in manifest['metadata']:
            lines.append('  labels:')
            lines.append(f'    {{{{- include "{chart_name}.labels" . | nindent 4 }}}}')
            kustomize_labels = add_kustomize_labels(manifest['metadata']['labels'])
            if kustomize_labels:
                lines.append(kustomize_labels)

        # Add annotations
        if 'annotations' in manifest['metadata']:
            lines.append('  annotations:')
            for key, value in manifest['metadata']['annotations'].items():
                lines.append(f'    {key}: {value}')

        # Add spec/data/other fields
        for key in manifest:
            if key not in ['apiVersion', 'kind', 'metadata']:
                lines.append(f'{key}:')
                yaml_content = yaml.dump(manifest[key], default_flow_style=False, sort_keys=False)
                for line in yaml_content.split('\n'):
                    if line:
                        lines.append(f'  {line}')

        # Only add closing if for non-main components
        if not is_main_component:
            lines.append('{{- end }}')

        template = '\n'.join(lines)

        # Generate filename
        kind_lower = manifest['kind'].lower()
        name = manifest['metadata']['name']
        filename = f'{kind_lower}_{name}.yaml'
        output_file = output_dir / filename

        with open(output_file, 'w') as f:
            f.write(template)
