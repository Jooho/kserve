"""
Common Template Generator Module

Generates Helm templates for common/base resources (inferenceServiceConfig and certManager).
"""

from pathlib import Path
from typing import Dict, Any

from .utils import yaml_to_string
from .configmap_generator import ConfigMapGenerator


class CommonTemplateGenerator:
    """Generates templates for common/base resources"""

    def __init__(self, mapping: Dict[str, Any]):
        self.mapping = mapping
        self.configmap_gen = ConfigMapGenerator()

    def generate_common_templates(self, templates_dir: Path, manifests: Dict[str, Any]):
        """Generate templates for common/base resources (inferenceServiceConfig and certManager)

        Args:
            templates_dir: Path to templates directory
            manifests: Dictionary containing all manifests
        """
        if 'common' not in manifests or not manifests['common']:
            return

        common_dir = templates_dir / 'common'
        common_dir.mkdir(exist_ok=True)

        # Generate ConfigMap template if inferenceServiceConfig is enabled
        if 'inferenceservice-config' in manifests['common'] and 'inferenceServiceConfig' in self.mapping:
            self._generate_configmap_template(common_dir)

        # Generate cert-manager Issuer template if certManager is enabled
        if 'certManager-issuer' in manifests['common'] and 'certManager' in self.mapping:
            self._generate_issuer_template(common_dir, manifests['common']['certManager-issuer'])

    def _generate_configmap_template(self, output_dir: Path):
        """Generate inferenceservice-config ConfigMap template

        Handles both old list format and new dict format for dataFields.
        New format supports individual fields with image/tag separation.
        """
        config = self.mapping['inferenceServiceConfig']['configMap']

        # ConfigMap controlled by inferenceServiceConfig.enabled
        template = f'''{{{{- if .Values.inferenceServiceConfig.enabled }}}}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {config['name']}
  namespace: {{{{ .Release.Namespace }}}}
  labels:
    {{{{- include "{self.mapping['metadata']['name']}.labels" . | nindent 4 }}}}
data:
'''

        data_fields = config['dataFields']

        # Support both old list format and new dict format
        if isinstance(data_fields, list):
            # Old format: list of {key, valuePath, defaultValue}
            for field in data_fields:
                key = field['key']
                value_path = field['valuePath']
                template += f'  {key}: |-\n    {{{{- toJson .Values.{value_path} | nindent 4 }}}}\n'
        else:
            # New format: nested dictionary with individual fields
            for field_name, field_config in data_fields.items():
                template += self.configmap_gen.generate_configmap_field(field_name, field_config)

        template += '{{- end }}\n'

        output_file = output_dir / 'inferenceservice-config.yaml'
        with open(output_file, 'w') as f:
            f.write(template)

    def _generate_issuer_template(self, output_dir: Path, issuer_manifest: Dict[str, Any]):
        """Generate cert-manager Issuer template

        Args:
            output_dir: Output directory for the template
            issuer_manifest: Issuer manifest from kustomize build
        """
        issuer_config = self.mapping['certManager']['issuer']

        # Issuer controlled by certManager.enabled only
        template = f'''{{{{- if .Values.certManager.enabled }}}}
apiVersion: {issuer_manifest['apiVersion']}
kind: {issuer_manifest['kind']}
metadata:
  name: {issuer_manifest['metadata']['name']}
  namespace: {{{{ .Release.Namespace }}}}
  labels:
    {{{{- include "{self.mapping['metadata']['name']}.labels" . | nindent 4 }}}}
spec:
'''
        # Add spec fields
        template += yaml_to_string(issuer_manifest['spec'], indent=2)

        template += '{{- end }}\n'

        output_file = output_dir / 'cert-manager-issuer.yaml'
        with open(output_file, 'w') as f:
            f.write(template)
