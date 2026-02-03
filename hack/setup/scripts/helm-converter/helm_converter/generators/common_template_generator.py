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

        # Ensure directory exists before writing files
        try:
            common_dir.mkdir(parents=True, exist_ok=True)
        except OSError as e:
            raise OSError(f"Failed to create common templates directory '{common_dir}': {e}")

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
        # Validate main config structure (required for ConfigMap generation)
        try:
            config = self.mapping['inferenceServiceConfig']['configMap']
        except KeyError as e:
            raise ValueError(
                f"ConfigMap template generation failed: missing required config - {e}\n"
                f"Required path: mapping['inferenceServiceConfig']['configMap']"
            )

        # Get config fields with safe defaults
        name = config.get('name', 'inferenceservice-config')

        # Validate required dataFields
        try:
            data_fields = config['dataFields']
        except KeyError:
            raise ValueError("ConfigMap config missing required field 'dataFields'")

        # Get chart name for labels template
        try:
            chart_name = self.mapping['metadata']['name']
        except KeyError:
            raise ValueError("Mapping missing required 'metadata.name' for chart labels")

        # ConfigMap controlled by inferenceServiceConfig.enabled
        template = f'''{{{{- if .Values.inferenceServiceConfig.enabled | default .Values.kserve.createSharedResources }}}}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {name}
  namespace: {{{{ .Release.Namespace }}}}
  labels:
    {{{{- include "{chart_name}.labels" . | nindent 4 }}}}
data:
'''

        # Support both old list format and new dict format
        if isinstance(data_fields, list):
            # Old format: list of {key, valuePath, defaultValue}
            for field in data_fields:
                key = field.get('key')
                value_path = field.get('valuePath')
                if not key or not value_path:
                    raise ValueError(f"ConfigMap field missing required 'key' or 'valuePath': {field}")
                template += f'  {key}: |-\n    {{{{- toJson .Values.{value_path} | nindent 4 }}}}\n'
        else:
            # New format: nested dictionary with individual fields
            for field_name, field_config in data_fields.items():
                template += self.configmap_gen.generate_configmap_field(field_name, field_config)

        template += '{{- end }}\n'

        # Write template file with error handling
        output_file = output_dir / 'inferenceservice-config.yaml'
        try:
            with open(output_file, 'w') as f:
                f.write(template)
        except IOError as e:
            raise IOError(f"Failed to write ConfigMap template to '{output_file}': {e}")

    def _generate_issuer_template(self, output_dir: Path, issuer_manifest: Dict[str, Any]):
        """Generate cert-manager Issuer template

        Args:
            output_dir: Output directory for the template
            issuer_manifest: Issuer manifest from kustomize build
        """
        # Validate issuer manifest structure (required fields)
        try:
            api_version = issuer_manifest['apiVersion']
            kind = issuer_manifest['kind']
            name = issuer_manifest['metadata']['name']
            spec = issuer_manifest['spec']
        except KeyError as e:
            raise ValueError(
                f"Issuer template generation failed: missing required field - {e}\n"
                f"Issuer manifest must have: apiVersion, kind, metadata.name, spec"
            )

        # Get chart name for labels template
        try:
            chart_name = self.mapping['metadata']['name']
        except KeyError:
            raise ValueError("Mapping missing required 'metadata.name' for chart labels")

        # Issuer controlled by certManager.enabled only
        template = f'''{{{{- if .Values.certManager.enabled | default .Values.kserve.createSharedResources }}}}
apiVersion: {api_version}
kind: {kind}
metadata:
  name: {name}
  namespace: {{{{ .Release.Namespace }}}}
  labels:
    {{{{- include "{chart_name}.labels" . | nindent 4 }}}}
spec:
'''
        # Add spec fields
        template += yaml_to_string(spec, indent=2)

        template += '{{- end }}\n'

        # Write template file with error handling
        output_file = output_dir / 'cert-manager-issuer.yaml'
        try:
            with open(output_file, 'w') as f:
                f.write(template)
        except IOError as e:
            raise IOError(f"Failed to write Issuer template to '{output_file}': {e}")
