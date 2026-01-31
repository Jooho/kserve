"""
LLMIsvc Config Generator Module

Generates Helm templates for LLMInferenceServiceConfigs.
"""

from pathlib import Path
from typing import Dict, Any

from .utils import yaml_to_string


class LLMIsvcConfigGenerator:
    """Generates templates for LLMInferenceServiceConfigs"""

    def __init__(self, mapping: Dict[str, Any]):
        self.mapping = mapping

    def generate_llmisvc_configs_templates(self, templates_dir: Path, manifests: Dict[str, Any]):
        """Generate templates for all LLMInferenceServiceConfigs

        Args:
            templates_dir: Path to templates directory
            manifests: Dictionary containing all manifests
        """
        if not manifests.get('llmisvcConfigs'):
            return

        configs_dir = templates_dir / 'llmisvcconfigs'
        configs_dir.mkdir(exist_ok=True)

        for config_data in manifests['llmisvcConfigs']:
            self._generate_llmisvc_config_template(configs_dir, config_data)

    def _generate_llmisvc_config_template(self, output_dir: Path, config_data: Dict[str, Any]):
        """Generate a single LLMInferenceServiceConfig template

        These resources contain Go templates that should NOT be escaped
        """
        config = config_data['config']
        manifest = config_data['manifest']
        copy_as_is = config_data.get('copyAsIs', True)
        original_yaml = config_data.get('original_yaml')
        original_filename = config_data.get('original_filename')

        config_name = config['name']

        # For copyAsIs resources with original YAML, use it directly with minimal processing
        if copy_as_is and original_yaml:
            # Parse the original YAML to extract just the spec section
            # We'll recreate the template with our conditional and labels
            template = f'''{{{{- if .Values.llmisvcConfigs.enabled }}}}
apiVersion: {manifest['apiVersion']}
kind: {manifest['kind']}
metadata:
  name: {manifest['metadata']['name']}
  namespace: {{{{ .Release.Namespace }}}}
  labels:
    {{{{- include "{self.mapping['metadata']['name']}.labels" . | nindent 4 }}}}
'''
            # Extract spec section from original YAML
            # Find the top-level "spec:" line and include it and everything after it
            lines = original_yaml.split('\n')
            spec_started = False
            for i, line in enumerate(lines):
                # Look for top-level spec: (no leading spaces)
                if not spec_started and line == 'spec:':
                    spec_started = True
                    template += 'spec:\n'
                elif spec_started:
                    # Escape Go template expressions so Helm doesn't try to process them
                    # Use placeholder technique to avoid double-replacement
                    escaped_line = line.replace('{{', '__HELM_OPEN__').replace('}}', '__HELM_CLOSE__')
                    escaped_line = escaped_line.replace('__HELM_OPEN__', '{{ "{{" }}').replace('__HELM_CLOSE__', '{{ "}}" }}')
                    template += escaped_line + '\n'

            template += '{{- end }}\n'

        else:
            # Fallback to normal processing
            template = f'''{{{{- if .Values.llmisvcConfigs.enabled }}}}
apiVersion: {manifest['apiVersion']}
kind: {manifest['kind']}
metadata:
  name: {manifest['metadata']['name']}
  labels:
    {{{{- include "{self.mapping['metadata']['name']}.labels" . | nindent 4 }}}}
'''
            if 'spec' in manifest:
                template += 'spec:\n'
                template += yaml_to_string(manifest['spec'], indent=2)

            template += '{{- end }}\n'

        # Use original filename if copyAsIs is True, otherwise sanitize
        if copy_as_is and original_filename:
            filename = original_filename
        else:
            # Sanitize filename
            filename = config_name.replace('kserve-config-', '').replace('kserve-', '') + '.yaml'

        output_file = output_dir / filename

        with open(output_file, 'w') as f:
            f.write(template)
