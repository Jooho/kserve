"""
Workload generator for Helm charts
Handles generation of Deployment and DaemonSet templates
"""
from typing import Dict, Any, Optional
from pathlib import Path
from .utils import (
    add_kustomize_labels,
    quote_label_value_if_needed,
    yaml_to_string,
    replace_cert_manager_namespace
)


class TemplateRenderer:
    """Generic Helm template renderer for workload fields.

    This class provides generic template rendering for common workload fields
    like nodeSelector, affinity, tolerations, etc. based on field metadata
    from the mapper configuration.
    """

    # Template patterns for different field types
    TEMPLATE_TYPES = {
        'configurable': """      {{{{- with .Values.{valuePath} }}}}
      {fieldName}:
        {{{{- toYaml . | nindent {indent} }}}}
      {{{{- end }}}}
""",
        'static': """      {fieldName}:
{value}
"""
    }

    def render_field(
            self,
            field_name: str,
            field_config: Dict[str, Any],
            default_indent: int = 6,
            static_value: Optional[Any] = None
    ) -> str:
        """Render template for a field based on its configuration.

        Args:
            field_name: Name of the field (e.g., 'nodeSelector', 'affinity')
            field_config: Field configuration from mapper (must have 'valuePath')
            default_indent: Default indentation level for the field content
            static_value: Static value to use if no valuePath (for backward compatibility)

        Returns:
            Rendered Helm template string for the field
        """
        # Determine template type based on configuration
        template_meta = field_config.get('template', {})
        template_type = template_meta.get('type', 'configurable')

        # For fields with valuePath, use configurable template (Helm values)
        if 'valuePath' in field_config:
            template_type = 'configurable'
            template_pattern = self.TEMPLATE_TYPES.get(template_type, '')

            # Calculate indent for content (field content is indented more than field name)
            content_indent = template_meta.get('indent', default_indent + 2)

            return template_pattern.format(
                valuePath=field_config['valuePath'],
                fieldName=field_name,
                indent=content_indent
            )

        # For static values (fallback for backward compatibility)
        elif static_value is not None:
            value_str = yaml_to_string(static_value, indent=default_indent + 2)
            return f"      {field_name}:\n{value_str}"

        return ""


class WorkloadGenerator:
    """Generator for Deployment and DaemonSet templates"""

    def __init__(self, mapping: Dict[str, Any]):
        """Initialize WorkloadGenerator

        Args:
            mapping: Chart mapping configuration
        """
        self.mapping = mapping

    def generate_deployment(
            self, output_dir: Path, component_name: str,
            component_data: Dict[str, Any], manifest_key: str) -> None:
        """Generate Deployment template for a component

        Args:
            output_dir: Output directory for template
            component_name: Name of the component
            component_data: Component configuration and manifests
            manifest_key: Key in component_data['manifests'] for the deployment
        """
        self._generate_workload(
            output_dir=output_dir,
            component_name=component_name,
            component_data=component_data,
            workload_type='deployment',
            config_key=manifest_key
        )

    def generate_daemonset(
            self, output_dir: Path, component_name: str,
            component_data: Dict[str, Any], manifest_key: str) -> None:
        """Generate DaemonSet template for a component's nodeAgent

        Args:
            output_dir: Output directory for template
            component_name: Name of the component
            component_data: Component configuration and manifests
            manifest_key: Key in component_data['manifests'] for the daemonset
        """
        self._generate_workload(
            output_dir=output_dir,
            component_name=component_name,
            component_data=component_data,
            workload_type='daemonset',
            config_key=manifest_key
        )

    def _extract_workload_manifest(
            self,
            component_data: Dict[str, Any],
            config_key: str
    ) -> Optional[Dict[str, Any]]:
        """Extract workload manifest from component data

        Args:
            component_data: Component configuration and manifests
            config_key: Key to lookup in manifests (e.g., 'controllerManager', 'nodeAgent')

        Returns:
            Workload manifest dict, or None if not found
        """
        manifest = component_data['manifests'].get(config_key)
        if not manifest:
            return None
        return manifest if isinstance(manifest, dict) else manifest[0]

    def _determine_component_status(
            self,
            component_name: str,
            component_data: Dict[str, Any]
    ) -> tuple[bool, Optional[str]]:
        """Determine if component is main and get enabled path

        Args:
            component_name: Name of the component
            component_data: Component configuration and manifests

        Returns:
            Tuple of (is_main_component, enabled_path)
        """
        chart_name = self.mapping['metadata']['name']
        is_main = component_name in [chart_name, 'kserve', 'llmisvc', 'localmodel', 'localmodelnode']
        enabled_path = None if is_main else component_data['config'].get('enabled', {}).get('valuePath')
        return is_main, enabled_path

    def _generate_selector_labels(self, match_labels: Dict[str, str]) -> str:
        """Generate selector labels template

        Args:
            match_labels: Label dict from spec.selector.matchLabels

        Returns:
            YAML template string for selector labels
        """
        lines = []
        for key, value in match_labels.items():
            lines.append(f'      {key}: {quote_label_value_if_needed(value)}')
        return '\n'.join(lines)

    def _get_output_filename(self, workload_type: str, workload_name: str) -> str:
        """Get output filename for workload

        Args:
            workload_type: 'deployment' or 'daemonset'
            workload_name: Name of the workload resource

        Returns:
            Output filename (e.g., 'deployment.yaml', 'daemonset_foo.yaml')
        """
        if workload_type == 'deployment':
            return 'deployment.yaml'
        else:  # daemonset
            return f'daemonset_{workload_name}.yaml'

    def _generate_pod_metadata(
            self,
            pod_metadata: Dict[str, Any],
            workload_type: str
    ) -> str:
        """Generate pod metadata with correct field ordering

        Args:
            pod_metadata: Pod metadata from spec.template.metadata
            workload_type: 'deployment' or 'daemonset'

        Returns:
            YAML template string for pod metadata
        """
        lines = ['    metadata:']

        # Order depends on workload type
        # Deployment: labels → annotations
        # DaemonSet: annotations → labels
        field_order = ['labels', 'annotations'] if workload_type == 'deployment' else ['annotations', 'labels']

        for field_name in field_order:
            if field_name == 'labels':
                lines.append('      labels:')
                for key, value in pod_metadata['labels'].items():
                    lines.append(f'        {key}: {quote_label_value_if_needed(value)}')
            elif field_name == 'annotations' and 'annotations' in pod_metadata:
                processed_annotations = replace_cert_manager_namespace(pod_metadata['annotations'])
                lines.append('      annotations:')
                for key, value in processed_annotations.items():
                    lines.append(f'        {key}: {value}')

        return '\n'.join(lines)

    def _generate_pod_spec_fields(
            self,
            pod_spec: Dict[str, Any],
            component_config: Dict[str, Any],
            workload_type: str
    ) -> str:
        """Generate pod spec fields (containers and others)

        Args:
            pod_spec: Pod spec from spec.template.spec
            component_config: Component configuration (controllerManager or nodeAgent)
            workload_type: 'deployment' or 'daemonset'

        Returns:
            YAML template string for pod spec fields
        """
        lines = []
        renderer = TemplateRenderer()

        for field_name, field_value in pod_spec.items():
            if field_name == 'containers':
                # Special handling for containers (image/resources substitution)
                lines.append('      containers:')
                for container in pod_spec['containers']:
                    is_main = container['name'] == 'manager'
                    container_spec = self._generate_container_spec(
                        container, is_main, component_config, workload_type
                    )
                    lines.append(container_spec)
            else:
                # Check mapper for configurability
                if field_name in component_config and isinstance(component_config[field_name], dict) and 'valuePath' in component_config[field_name]:
                    # Mapper defined → configurable ({{ .Values.xxx }})
                    value_path = component_config[field_name]['valuePath']

                    # Special handling for affinity/tolerations to output empty values
                    # This ensures verification accuracy (empty {} or [] are semantically equivalent to omission)
                    if field_name in ['affinity', 'tolerations']:
                        lines.append(f'      {field_name}: {{{{- toYaml .Values.{value_path} | nindent 8 }}}}')
                    else:
                        lines.append(renderer.render_field(field_name, component_config[field_name], default_indent=6))
                else:
                    # Not in mapper → static (keep manifest value as-is)
                    if isinstance(field_value, (dict, list)):
                        # Complex value (dict or list)
                        lines.append(f'      {field_name}:')
                        lines.append(yaml_to_string(field_value, indent=8))
                    else:
                        # Scalar value (string, int, bool, etc.)
                        yaml_value = str(field_value).lower() if isinstance(field_value, bool) else field_value
                        lines.append(f'      {field_name}: {yaml_value}')

        return '\n'.join(lines)

    def _generate_workload(
            self,
            output_dir: Path,
            component_name: str,
            component_data: Dict[str, Any],
            workload_type: str,
            config_key: str
    ) -> None:
        """Generate workload (Deployment or DaemonSet) template

        Args:
            output_dir: Output directory for template
            component_name: Name of the component
            component_data: Component configuration and manifests
            workload_type: 'deployment' or 'daemonset'
            config_key: Config key to lookup ('controllerManager' or 'nodeAgent')
        """
        # Extract manifest
        workload = self._extract_workload_manifest(component_data, config_key)
        if not workload:
            return

        component_config = component_data['config'].get(config_key, {})
        is_main, enabled_path = self._determine_component_status(component_name, component_data)

        # Generate template
        template = self._generate_workload_header(workload, is_main, enabled_path)
        template += '\nspec:\n  selector:\n    matchLabels:\n'
        template += self._generate_selector_labels(workload['spec']['selector']['matchLabels'])
        template += '\n  template:\n'
        template += self._generate_pod_metadata(workload['spec']['template']['metadata'], workload_type)
        template += '\n    spec:\n'
        template += self._generate_pod_spec_fields(
            workload['spec']['template']['spec'],
            component_config,
            workload_type
        )

        if not is_main:
            template += '{{- end }}\n'

        # Write file
        filename = self._get_output_filename(workload_type, workload['metadata']['name'])
        output_file = output_dir / filename
        with open(output_file, 'w') as f:
            f.write(template)

    def _generate_workload_header(
            self, workload: Dict[str, Any], is_main_component: bool,
            enabled_path: str = None) -> str:
        """Generate workload (Deployment/DaemonSet) header with metadata and labels

        Args:
            workload: Workload manifest (Deployment or DaemonSet)
            is_main_component: Whether this is a main component (always enabled)
            enabled_path: Path to enabled flag in values (only for non-main components)

        Returns:
            YAML template string with header, metadata, and labels
        """
        chart_name = self.mapping['metadata']['name']
        lines = []

        # Add conditional wrapper for non-main components
        if not is_main_component and enabled_path:
            lines.append(f'{{{{- if .Values.{enabled_path} }}}}')

        # Add apiVersion, kind, metadata
        lines.extend([
            f'apiVersion: {workload["apiVersion"]}',
            f'kind: {workload["kind"]}',
            'metadata:',
            f'  name: {workload["metadata"]["name"]}',
            '  namespace: {{ .Release.Namespace }}',
            '  labels:',
            f'    {{{{- include "{chart_name}.labels" . | nindent 4 }}}}'
        ])

        # Add Kustomize labels
        if 'labels' in workload['metadata']:
            kustomize_labels = add_kustomize_labels(workload['metadata']['labels'])
            if kustomize_labels:
                lines.append(kustomize_labels)

        return '\n'.join(lines)

    def _generate_container_spec(
            self, container: Dict[str, Any], is_main_container: bool,
            component_config: Dict[str, Any],
            workload_type: str = 'deployment') -> str:
        """Generate complete container specification

        Args:
            container: Container spec from manifest
            is_main_container: Whether this is the main 'manager' container
            component_config: Component configuration (controllerManager or nodeAgent)
            workload_type: 'deployment' or 'daemonset' (affects which fields to include)

        Returns:
            Complete container YAML template string
        """
        lines = [f'      - name: {container["name"]}']

        # Image configuration
        if is_main_container and 'image' in component_config:
            img_repo_path = component_config['image']['repository']['valuePath']
            img_tag_path = component_config['image']['tag']['valuePath']
            chart_name = img_tag_path.split('.')[0]
            # Use kserve.version as highest priority, then component.version, then component.controller.tag
            lines.append(f'        image: "{{{{ .Values.{img_repo_path} }}}}:{{{{ .Values.kserve.version | default .Values.{chart_name}.version | default .Values.{img_tag_path} }}}}"')
        else:
            lines.append(f'        image: "{container["image"]}"')

        # Image pull policy
        if 'imagePullPolicy' in container:
            if is_main_container and 'image' in component_config and 'pullPolicy' in component_config['image']:
                policy_path = component_config['image']['pullPolicy']['valuePath']
                lines.append(f'        imagePullPolicy: {{{{ .Values.{policy_path} }}}}')
            else:
                lines.append(f'        imagePullPolicy: {container["imagePullPolicy"]}')

        # Command
        if 'command' in container:
            lines.append('        command:')
            for cmd in container['command']:
                lines.append(f'        - {cmd}')

        # Args (deployment only)
        if workload_type == 'deployment' and 'args' in container:
            lines.append('        args:')
            for arg in container['args']:
                lines.append(f'        - {arg}')

        # Security context
        if 'securityContext' in container:
            lines.append('        securityContext:')
            lines.append(yaml_to_string(container['securityContext'], indent=10))

        # Env
        if 'env' in container:
            lines.append('        env:')
            lines.append(yaml_to_string(container['env'], indent=10))

        # Ports (deployment only)
        if workload_type == 'deployment' and 'ports' in container:
            lines.append('        ports:')
            lines.append(yaml_to_string(container['ports'], indent=10))

        # Probes (deployment only)
        if workload_type == 'deployment':
            for probe_name in ['livenessProbe', 'readinessProbe']:
                if probe_name in container:
                    lines.append(f'        {probe_name}:')
                    lines.append(yaml_to_string(container[probe_name], indent=10))

        # Resources - configurable for main container
        if is_main_container and 'resources' in component_config:
            resources_path = component_config['resources']['valuePath']
            lines.append(f'        resources: {{{{- toYaml .Values.{resources_path} | nindent 10 }}}}')
        elif 'resources' in container:
            lines.append('        resources:')
            lines.append(yaml_to_string(container['resources'], indent=10))

        # Volume mounts
        if 'volumeMounts' in container:
            lines.append('        volumeMounts:')
            lines.append(yaml_to_string(container['volumeMounts'], indent=10))

        return '\n'.join(lines)
