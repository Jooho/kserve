"""
ConfigMap generator for Helm charts
Handles generation of ConfigMap data fields with various types
"""
from typing import Dict, Any


class ConfigMapGenerator:
    """Generator for ConfigMap templates"""

    def generate_configmap_field(self, field_name: str, field_config: dict) -> str:
        """Generate a single ConfigMap data field template

        Handles different types:
        1. JSON fields with valuePath+defaultValue (credentials, ingress, etc.)
        2. Explainers with image/defaultImageVersion (special case)
        3. Structured fields with image/tag separation (agent, logger, etc.)
        4. LocalModel with defaultJobImage/defaultJobImageTag separation
        5. Simple structured fields (deploy, security, etc.)

        Args:
            field_name: Name of the ConfigMap data field
            field_config: Field configuration from mapper

        Returns:
            Formatted ConfigMap field template
        """
        # Check if this field has a valuePath (JSON format)
        if 'valuePath' in field_config and 'defaultValue' in field_config:
            # JSON format - use toJson
            return f'  {field_name}: |-\n    {{{{- toJson .Values.{field_config["valuePath"]} | nindent 4 }}}}\n'

        # Special case: explainers uses defaultImageVersion instead of tag
        if field_name == 'explainers':
            return self._generate_explainers_field(field_config)

        # Check if this is localModel (has defaultJobImage)
        if field_name == 'localModel':
            return self._generate_localmodel_field(field_name, field_config)

        # Check if this field has image/tag structure
        has_image = 'image' in field_config and 'tag' in field_config
        has_individual_fields = any(
            isinstance(v, dict) and 'valuePath' in v
            for k, v in field_config.items()
            if k not in ['image', 'tag']
        )

        if has_image:
            # Image-based fields (agent, logger, storageInitializer, batcher, router)
            return self._generate_image_based_field(field_name, field_config)

        if has_individual_fields:
            # Generate JSON with individual fields (metricsAggregator, autoscaler, security, service, etc.)
            return self._generate_individual_fields(field_name, field_config)

        # Simple structured fields (deploy, credentials, ingress, etc.) - use toJson
        return self._generate_simple_structured_field(field_name, field_config)

    def _build_json_field_line(self, parent_path: str, field_key: str, field_type: str) -> str:
        """Build a single JSON field line with proper type handling

        Args:
            parent_path: Parent values path (e.g., 'inferenceServiceConfig.agent')
            field_key: Field key name (e.g., 'memoryRequest')
            field_type: Field type ('boolean', 'number', 'array', 'string')

        Returns:
            JSON field line with proper quotes/toJson based on type
        """
        if field_type in ['boolean', 'number']:
            # Boolean/Number - no quotes
            return f'        "{field_key}": {{{{ .Values.{parent_path}.{field_key} }}}}'
        elif field_type == 'array':
            # Array - use toJson
            return f'        "{field_key}": {{{{- toJson .Values.{parent_path}.{field_key} }}}}'
        else:
            # String (default) - with quotes
            return f'        "{field_key}": "{{{{ .Values.{parent_path}.{field_key} }}}}"'

    def _generate_explainers_field(self, field_config: dict) -> str:
        """Generate ConfigMap field for explainers with defaultImageVersion

        Explainers uses defaultImageVersion instead of tag in ConfigMap.
        Structure: {"art": {"image": "...", "defaultImageVersion": "..."}}
        Uses simplified Helm dict/range with inline dict creation.

        Args:
            field_config: Field configuration from mapper

        Returns:
            Formatted explainers field template
        """
        lines = ['  explainers: |-']
        lines.append('    {{- $explainers := dict }}')
        lines.append('    {{- range $name, $config := .Values.inferenceServiceConfig.explainers }}')
        lines.append('      {{- $_ := set $explainers $name (dict "image" $config.image "defaultImageVersion" $config.tag) }}')
        lines.append('    {{- end }}')
        lines.append('    {{- toJson $explainers | nindent 4 }}')
        return '\n'.join(lines) + '\n'

    def _generate_json_field_with_type_support(self, field_name: str, field_config: dict,
                                                image_field_name: str = None) -> str:
        """Generate ConfigMap JSON field with type-aware field rendering

        Unified function for generating JSON fields with proper type handling.
        Supports optional image field combination.

        Args:
            field_name: ConfigMap data field name (e.g., 'agent', 'metricsAggregator')
            field_config: Field configuration from mapper
            image_field_name: If provided, combines image:tag into this field name
                            (e.g., 'image' for agent/logger, 'defaultJobImage' for localModel)

        Returns:
            Formatted ConfigMap field with JSON structure
        """
        parent_path = f'inferenceServiceConfig.{field_name}'
        lines = [f'  {field_name}: |-', '    {']
        json_lines = []

        # Add image field if specified
        if image_field_name:
            img_template = f'{{{{ .Values.{parent_path}.image }}}}:{{{{ .Values.{parent_path}.tag }}}}'
            if image_field_name == 'defaultJobImage':
                # LocalModel uses defaultJobImageTag
                img_template = f'{{{{ .Values.{parent_path}.defaultJobImage }}}}:{{{{ .Values.{parent_path}.defaultJobImageTag }}}}'
            json_lines.append(f'        "{image_field_name}" : "{img_template}"')

        # Add other fields from config
        for key, value in field_config.items():
            # Skip image-related keys
            if key in ['image', 'tag', 'defaultJobImage', 'defaultJobImageTag']:
                continue

            if not isinstance(value, dict) or 'valuePath' not in value:
                continue

            # Extract field name and type
            field_key = value['valuePath'].split('.')[-1]
            field_type = value.get('type', 'string')

            # Build JSON line with proper type handling
            json_lines.append(self._build_json_field_line(parent_path, field_key, field_type))

        # Join with commas
        for i, line in enumerate(json_lines):
            lines.append(line + (',' if i < len(json_lines) - 1 else ''))

        lines.extend(['    }', ''])
        return '\n'.join(lines)

    def _generate_image_based_field(self, field_name: str, field_config: dict) -> str:
        """Generate ConfigMap field for image-based components (agent, logger, etc.)

        Args:
            field_name: ConfigMap data field name
            field_config: Field configuration from mapper

        Returns:
            Formatted ConfigMap field
        """
        return self._generate_json_field_with_type_support(field_name, field_config, image_field_name='image')

    def _generate_individual_fields(self, field_name: str, field_config: dict) -> str:
        """Generate ConfigMap field for components with individual fields (metricsAggregator, autoscaler, etc.)

        Args:
            field_name: ConfigMap data field name
            field_config: Field configuration from mapper

        Returns:
            Formatted ConfigMap field
        """
        return self._generate_json_field_with_type_support(field_name, field_config)

    def _generate_simple_structured_field(self, field_name: str, field_config: dict) -> str:
        """Generate ConfigMap field for simple structured fields (deploy, security, etc.)

        Just uses toJson on the entire object.

        Args:
            field_name: ConfigMap data field name
            field_config: Field configuration from mapper

        Returns:
            Formatted ConfigMap field
        """
        return f'  {field_name}: |-\n    {{{{- toJson .Values.inferenceServiceConfig.{field_name} | nindent 4 }}}}\n'

    def _generate_localmodel_field(self, field_name: str, field_config: dict) -> str:
        """Generate ConfigMap field for localModel with defaultJobImage/defaultJobImageTag

        Args:
            field_name: ConfigMap data field name
            field_config: Field configuration from mapper

        Returns:
            Formatted ConfigMap field
        """
        return self._generate_json_field_with_type_support(field_name, field_config, image_field_name='defaultJobImage')
