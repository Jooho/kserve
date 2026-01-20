"""
Values Generator Module

Generates values.yaml file from mapping configuration and default values.
"""

import yaml
from pathlib import Path
from typing import Dict, Any


# Custom YAML representer to handle dict in order
class OrderedDumper(yaml.SafeDumper):
    pass


def dict_representer(dumper, data):
    return dumper.represent_mapping(
        yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG,
        data.items())


# Register the representer
OrderedDumper.add_representer(dict, dict_representer)


class ValuesGenerator:
    """Generates values.yaml file for Helm chart"""

    def __init__(self, mapping: Dict[str, Any], manifests: Dict[str, Any], output_dir: Path):
        self.mapping = mapping
        self.manifests = manifests
        self.output_dir = output_dir

    def generate(self):
        """Generate values.yaml file"""
        values = self._build_values()

        values_file = self.output_dir / 'values.yaml'
        with open(values_file, 'w') as f:
            # Add header comment
            f.write(self._generate_header())
            # Write values using custom dumper
            yaml.dump(values, f, Dumper=OrderedDumper, default_flow_style=False, sort_keys=False, width=120, allow_unicode=True)

    def show_plan(self):
        """Show what values would be generated (dry run)"""
        values = self._build_values()
        print("\n  Sample values structure:")
        self._print_keys(values, indent=4)

    def _build_values(self) -> Dict[str, Any]:
        """Build the complete values dictionary"""
        values = {}

        # Add inferenceServiceConfig values
        if 'inferenceServiceConfig' in self.mapping:
            values['inferenceServiceConfig'] = self._build_inference_service_config_values()

        # Add certManager values
        if 'certManager' in self.mapping:
            if 'enabled' in self.mapping['certManager']:
                values['certManager'] = {
                    'enabled': self.mapping['certManager']['enabled'].get('defaultValue', True)
                }

        # Add component-specific values
        chart_name = self.mapping['metadata']['name']
        if chart_name in self.mapping:
            values[chart_name] = self._build_component_values(chart_name, self.mapping[chart_name])

        # Add llmisvcConfigs values if present (even if not main chart)
        if 'llmisvcConfigs' in self.mapping and chart_name != 'llmisvc':
            # For llmisvc configs component, just add enabled flag
            if 'enabled' in self.mapping['llmisvcConfigs']:
                values['llmisvcConfigs'] = {
                    'enabled': self.mapping['llmisvcConfigs']['enabled'].get('defaultValue', False)
                }
        # Also support old 'llmisvc' key for backward compatibility
        elif 'llmisvc' in self.mapping and chart_name != 'llmisvc':
            if 'enabled' in self.mapping['llmisvc']:
                values['llmisvcConfigs'] = {
                    'enabled': self.mapping['llmisvc']['enabled'].get('defaultValue', False)
                }

        # Add localmodel values if present
        if 'localmodel' in self.mapping:
            values['localmodel'] = self._build_component_values('localmodel', self.mapping['localmodel'])

        # Add runtime values (support both clusterServingRuntimes and runtimes keys)
        if 'clusterServingRuntimes' in self.mapping:
            values['runtimes'] = self._build_runtime_values()
        elif 'runtimes' in self.mapping:
            values['runtimes'] = self._build_runtime_values_from_key('runtimes')

        # Add CRD values
        if 'crds' in self.mapping:
            values['crds'] = self._build_crd_values()

        return values

    def _build_inference_service_config_values(self) -> Dict[str, Any]:
        """Build inferenceServiceConfig section of values"""
        isvc_config = self.mapping['inferenceServiceConfig']
        values = {}

        # Enabled flag
        if 'enabled' in isvc_config:
            values['enabled'] = isvc_config['enabled'].get('defaultValue', True)

        # Config values from ConfigMap dataFields
        if 'configMap' in isvc_config:
            config_map = isvc_config['configMap']

            # Add all data fields directly to inferenceServiceConfig
            for field in config_map.get('dataFields', []):
                key = field['key']
                default_value = field.get('defaultValue', '{}')

                # Parse JSON string to dict for better YAML formatting
                try:
                    import json
                    parsed_value = json.loads(default_value)
                    values[key] = parsed_value
                except:
                    # If not valid JSON, use as-is
                    values[key] = default_value

        return values

    def _build_component_values(self, component_name: str, component_config: Dict[str, Any]) -> Dict[str, Any]:
        """Build values for a component (kserve, llmisvc, or localmodel)"""
        values = {}

        # Main components (kserve, llmisvc) don't need enabled flag - always installed
        # Only localmodel needs enabled flag
        chart_name = self.mapping['metadata']['name']
        is_main_component = component_name in [chart_name, 'kserve', 'llmisvc']

        if not is_main_component and 'enabled' in component_config:
            values['enabled'] = component_config['enabled'].get('defaultValue', True)

        # Controller manager configuration
        if 'controllerManager' in component_config:
            cm_config = component_config['controllerManager']
            values['controllerManager'] = {}

            # Image configuration
            if 'image' in cm_config:
                img_config = cm_config['image']
                values['controllerManager']['image'] = {}

                if 'repository' in img_config:
                    values['controllerManager']['image']['repository'] = img_config['repository'].get('defaultValue', '')

                if 'tag' in img_config:
                    values['controllerManager']['image']['tag'] = img_config['tag'].get('defaultValue', 'latest')

                if 'pullPolicy' in img_config:
                    values['controllerManager']['image']['pullPolicy'] = img_config['pullPolicy'].get('defaultValue', 'Always')

            # Resources configuration
            if 'resources' in cm_config:
                values['controllerManager']['resources'] = cm_config['resources'].get('defaultValue', {})

        return values

    def _build_runtime_values(self) -> Dict[str, Any]:
        """Build runtime values from clusterServingRuntimes key"""
        return self._build_runtime_values_from_key('clusterServingRuntimes')

    def _build_runtime_values_from_key(self, key: str) -> Dict[str, Any]:
        """Build runtime values from specified key (clusterServingRuntimes or runtimes)"""
        runtimes_config = self.mapping[key]
        values = {}

        # Global enabled flag
        if 'enabled' in runtimes_config:
            values['enabled'] = runtimes_config['enabled'].get('defaultValue', True)

        # Individual runtime configurations
        for runtime_config in runtimes_config.get('runtimes', []):
            runtime_key = self._extract_runtime_key(runtime_config['enabledPath'])

            values[runtime_key] = {}
            values[runtime_key]['enabled'] = runtime_config.get('defaultEnabled', True)

            # Image configuration
            if 'image' in runtime_config:
                img_config = runtime_config['image']
                values[runtime_key]['image'] = {}
                values[runtime_key]['image']['repository'] = img_config.get('defaultRepository', '')
                values[runtime_key]['image']['tag'] = img_config.get('defaultTag', 'latest')

            # Resources configuration
            if 'resources' in runtime_config:
                values[runtime_key]['resources'] = runtime_config['resources'].get('defaultValue', {})

        return values

    def _build_crd_values(self) -> Dict[str, Any]:
        """Build CRD values"""
        crds_config = self.mapping['crds']
        values = {}

        if 'install' in crds_config:
            values['install'] = crds_config['install'].get('defaultValue', True)

        if 'full' in crds_config:
            values['full'] = {}
            values['full']['enabled'] = crds_config['full'].get('defaultEnabled', True)

        return values

    def _extract_runtime_key(self, enabled_path: str) -> str:
        """
        Extract runtime key from enabledPath
        e.g., "runtimes.sklearn.enabled" -> "sklearn"
        """
        parts = enabled_path.split('.')
        if len(parts) >= 2:
            return parts[1]
        return parts[0]

    def _generate_header(self) -> str:
        """Generate header comment for values.yaml"""
        chart_name = self.mapping['metadata']['name']
        description = self.mapping['metadata']['description']

        return f"""# Default values for {chart_name}
# This is a YAML-formatted file.
# Declare variables to be passed into your templates.

# {description}

# NOTE: This file was auto-generated from {chart_name} mapping configuration.
# Source of truth: Kustomize manifests in config/

"""

    def _print_keys(self, d: Dict[str, Any], indent: int = 0):
        """Print dictionary keys recursively for dry run output"""
        for key, value in d.items():
            if isinstance(value, dict):
                print(' ' * indent + f'- {key}:')
                self._print_keys(value, indent + 2)
            else:
                print(' ' * indent + f'- {key}: ...')
