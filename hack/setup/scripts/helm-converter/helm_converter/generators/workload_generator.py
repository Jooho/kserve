"""
Workload generator for Helm charts
Handles generation of Deployment and DaemonSet templates
"""
from typing import Dict, Any
from pathlib import Path
from .utils import (
    add_kustomize_labels,
    quote_label_value_if_needed,
    yaml_to_string,
    replace_cert_manager_namespace
)


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
            component_data: Dict[str, Any]) -> None:
        """Generate Deployment template for a component

        Args:
            output_dir: Output directory for template
            component_name: Name of the component
            component_data: Component configuration and manifests
        """
        config = component_data['config']
        manifest = component_data['manifests'].get('controllerManager')

        if not manifest:
            return

        # Get deployment from manifest (could be multi-doc)
        deployment = manifest if isinstance(manifest, dict) else manifest[0]

        cm_config = config.get('controllerManager', {})
        chart_name = self.mapping['metadata']['name']

        # Main component (kserve/llmisvc/localmodel) is always installed
        is_main_component = component_name in [chart_name, 'kserve', 'llmisvc', 'localmodel']
        enabled_path = None if is_main_component else config.get('enabled', {}).get('valuePath')

        # Generate header with metadata and labels
        template = self._generate_workload_header(deployment, is_main_component, enabled_path)
        template += '\nspec:\n  selector:\n    matchLabels:\n'

        # Add selector labels
        for key, value in deployment['spec']['selector']['matchLabels'].items():
            template += f'      {key}: {quote_label_value_if_needed(value)}\n'

        template += '  template:\n'
        template += '    metadata:\n'
        template += '      labels:\n'

        # Add pod labels
        for key, value in deployment['spec']['template']['metadata']['labels'].items():
            template += f'        {key}: {quote_label_value_if_needed(value)}\n'

        # Add annotations if present
        if 'annotations' in deployment['spec']['template']['metadata']:
            # Replace namespace in cert-manager annotations with Helm template
            processed_annotations = replace_cert_manager_namespace(
                deployment['spec']['template']['metadata']['annotations']
            )
            template += '      annotations:\n'
            for key, value in processed_annotations.items():
                template += f'        {key}: {value}\n'

        template += '    spec:\n'

        # Service account
        template += f'      serviceAccountName: {deployment["spec"]["template"]["spec"]["serviceAccountName"]}\n'

        # Security context
        if 'securityContext' in deployment['spec']['template']['spec']:
            template += '      securityContext:\n'
            template += yaml_to_string(deployment['spec']['template']['spec']['securityContext'], indent=8)

        # Containers
        template += '      containers:\n'
        for container in deployment['spec']['template']['spec']['containers']:
            is_main_container = container['name'] == 'manager'
            template += self._generate_container_spec(container, is_main_container, cm_config, 'deployment')
            template += '\n'

        # Termination grace period
        if 'terminationGracePeriodSeconds' in deployment['spec']['template']['spec']:
            template += f'      terminationGracePeriodSeconds: {deployment["spec"]["template"]["spec"]["terminationGracePeriodSeconds"]}\n'

        # Volumes
        if 'volumes' in deployment['spec']['template']['spec']:
            template += '      volumes:\n'
            template += yaml_to_string(deployment['spec']['template']['spec']['volumes'], indent=8)

        # Only add closing if for non-main components
        if not is_main_component:
            template += '{{- end }}\n'

        output_file = output_dir / 'deployment.yaml'
        with open(output_file, 'w') as f:
            f.write(template)

    def generate_daemonset(
            self, output_dir: Path, component_name: str,
            component_data: Dict[str, Any]) -> None:
        """Generate DaemonSet template for a component's nodeAgent

        Args:
            output_dir: Output directory for template
            component_name: Name of the component
            component_data: Component configuration and manifests
        """
        config = component_data['config']
        manifest = component_data['manifests'].get('nodeAgent')

        if not manifest:
            return

        # Get daemonset from manifest
        daemonset = manifest if isinstance(manifest, dict) else manifest[0]

        na_config = config.get('nodeAgent', {})
        chart_name = self.mapping['metadata']['name']

        # Main component (kserve/llmisvc/localmodel) is always installed
        is_main_component = component_name in [chart_name, 'kserve', 'llmisvc', 'localmodel']
        enabled_path = None if is_main_component else config.get('enabled', {}).get('valuePath')

        # Generate header with metadata and labels
        template = self._generate_workload_header(daemonset, is_main_component, enabled_path)
        template += '\nspec:\n  selector:\n    matchLabels:\n'

        # Add selector labels
        for key, value in daemonset['spec']['selector']['matchLabels'].items():
            template += f'      {key}: {quote_label_value_if_needed(value)}\n'

        template += '  template:\n'
        template += '    metadata:\n'

        # Add annotations if present
        if 'annotations' in daemonset['spec']['template']['metadata']:
            # Replace namespace in cert-manager annotations with Helm template
            processed_annotations = replace_cert_manager_namespace(
                daemonset['spec']['template']['metadata']['annotations']
            )
            template += '      annotations:\n'
            for key, value in processed_annotations.items():
                template += f'        {key}: {value}\n'

        template += '      labels:\n'

        # Add pod labels
        for key, value in daemonset['spec']['template']['metadata']['labels'].items():
            template += f'        {key}: {quote_label_value_if_needed(value)}\n'

        template += '    spec:\n'

        # Containers
        template += '      containers:\n'
        for container in daemonset['spec']['template']['spec']['containers']:
            is_main_container = container['name'] == 'manager'
            template += self._generate_container_spec(container, is_main_container, na_config, 'daemonset')
            template += '\n'

        # Node selector (common in DaemonSets)
        if 'nodeSelector' in daemonset['spec']['template']['spec']:
            template += '      nodeSelector:\n'
            template += yaml_to_string(daemonset['spec']['template']['spec']['nodeSelector'], indent=8)

        # Security context (pod-level)
        if 'securityContext' in daemonset['spec']['template']['spec']:
            template += '      securityContext:\n'
            template += yaml_to_string(daemonset['spec']['template']['spec']['securityContext'], indent=8)

        # Service account
        template += f'      serviceAccountName: {daemonset["spec"]["template"]["spec"]["serviceAccountName"]}\n'

        # Termination grace period
        if 'terminationGracePeriodSeconds' in daemonset['spec']['template']['spec']:
            template += f'      terminationGracePeriodSeconds: {daemonset["spec"]["template"]["spec"]["terminationGracePeriodSeconds"]}\n'

        # Volumes
        if 'volumes' in daemonset['spec']['template']['spec']:
            template += '      volumes:\n'
            template += yaml_to_string(daemonset['spec']['template']['spec']['volumes'], indent=8)

        # Only add closing if for non-main components
        if not is_main_component:
            template += '{{- end }}\n'

        # Generate filename from daemonset name
        filename = f'daemonset_{daemonset["metadata"]["name"]}.yaml'
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
