"""
Component Builder Module

Builds component values (kserve, llmisvc, localmodel) including
controller manager and node agent configurations.
"""

from typing import Dict, Any, Optional
from .path_extractor import extract_from_manifest


class ComponentBuilder:
    """Builds component values (controller + nodeAgent)"""

    def __init__(self, mapping: Dict[str, Any]):
        self.mapping = mapping
        self.version_anchor_fields = []

    def build_component_values(
        self,
        component_name: str,
        component_config: Dict[str, Any],
        manifests: Dict[str, Any]
    ) -> Dict[str, Any]:
        """Build values for a component (kserve, llmisvc, or localmodel).

        Uses actual values from kustomize build result, not defaultValue from mapping.
        This ensures that helm template output matches kustomize build output.

        Args:
            component_name: Component name (e.g., 'kserve', 'llmisvc', 'localmodel')
            component_config: Component configuration from mapping
            manifests: Kubernetes manifests

        Returns:
            Component values dictionary
        """
        values = {}

        # Main components (kserve, llmisvc) don't need enabled flag - always installed
        # Only localmodel needs enabled flag
        chart_name = self.mapping['metadata']['name']
        is_main_component = component_name in [chart_name, 'kserve', 'llmisvc']

        if not is_main_component and 'enabled' in component_config:
            # localmodel is optional, default to False
            values['enabled'] = False

        # Add version field if specified in mapping (for anchor reference)
        if 'version' in component_config:
            version_config = component_config['version']
            if version_config.get('useVersionAnchor', False):
                # Track version field for anchor reference
                # Don't add actual version value here - it will be added as anchor by anchor_processor
                # But track the path for anchor_processor
                anchor_path = f"{component_name}.version"
                self.version_anchor_fields.append(anchor_path)

        # Get the component manifest
        component_manifest = None
        if component_name in manifests.get('components', {}):
            component_data = manifests['components'][component_name]
            if 'manifests' in component_data and 'controllerManager' in component_data['manifests']:
                component_manifest = component_data['manifests']['controllerManager']

        # Controller manager configuration
        if 'controllerManager' in component_config:
            cm_values = self._build_controller_manager_values(
                component_name,
                component_config['controllerManager'],
                component_manifest
            )
            values.update(cm_values)

        # Node Agent configuration (DaemonSet)
        if 'nodeAgent' in component_config:
            # Get the nodeAgent manifest
            nodeagent_manifest = None
            if component_name in manifests.get('components', {}):
                component_data = manifests['components'][component_name]
                if 'manifests' in component_data and 'nodeAgent' in component_data['manifests']:
                    nodeagent_manifest = component_data['manifests']['nodeAgent']

            na_values = self._build_node_agent_values(
                component_name,
                component_config['nodeAgent'],
                nodeagent_manifest
            )
            values.update(na_values)

        return values

    def _build_controller_manager_values(
        self,
        component_name: str,
        cm_config: Dict[str, Any],
        component_manifest: Optional[Dict[str, Any]]
    ) -> Dict[str, Any]:
        """Build controller manager values.

        Args:
            component_name: Component name
            cm_config: Controller manager configuration
            component_manifest: Controller manager Deployment manifest

        Returns:
            Controller manager values dictionary
        """
        values = {}

        # Extract the base path from valuePath (e.g., "kserve.controller" from "kserve.controller.image")
        # We need to determine the controller key name from the valuePath
        controller_key = 'controllerManager'  # default

        if 'image' in cm_config and 'repository' in cm_config['image']:
            value_path = cm_config['image']['repository'].get('valuePath', '')
            # Parse valuePath like "kserve.controller.image" or "kserve.controllerManager.image.repository"
            if value_path:
                parts = value_path.split('.')
                if len(parts) >= 2:
                    # The second part is the controller key (controller or controllerManager)
                    controller_key = parts[1]

        values[controller_key] = {}

        # Image configuration - use path field if available, otherwise fallback to hardcoded
        if 'image' in cm_config and component_manifest:
            img_values = self._extract_image_values(
                cm_config['image'],
                component_manifest,
                component_name,
                controller_key,
                workload_type='deployment'
            )
            values[controller_key].update(img_values)

        # Resources configuration - use path field if available, otherwise fallback to hardcoded
        if 'resources' in cm_config and component_manifest:
            res_config = cm_config['resources']
            if isinstance(res_config, dict) and 'path' in res_config:
                # Use path field to extract resources
                try:
                    resources = extract_from_manifest(component_manifest, res_config['path'])
                    if resources:
                        values[controller_key]['resources'] = resources
                except (KeyError, IndexError, ValueError) as e:
                    print(f"Warning: Failed to extract resources using path '{res_config['path']}': {e}")
                    # Fallback to hardcoded path
                    actual_resources = component_manifest['spec']['template']['spec']['containers'][0].get('resources', {})
                    if actual_resources:
                        values[controller_key]['resources'] = actual_resources
            else:
                # No path field - fallback to hardcoded (backward compatibility)
                actual_resources = component_manifest['spec']['template']['spec']['containers'][0].get('resources', {})
                if actual_resources:
                    values[controller_key]['resources'] = actual_resources

        return values

    def _build_node_agent_values(
        self,
        component_name: str,
        na_config: Dict[str, Any],
        nodeagent_manifest: Optional[Dict[str, Any]]
    ) -> Dict[str, Any]:
        """Build node agent values.

        Args:
            component_name: Component name
            na_config: Node agent configuration
            nodeagent_manifest: Node agent DaemonSet manifest

        Returns:
            Node agent values dictionary
        """
        values = {}

        # Extract the base path from valuePath
        nodeagent_key = 'nodeAgent'  # default

        if 'image' in na_config and 'repository' in na_config['image']:
            value_path = na_config['image']['repository'].get('valuePath', '')
            if value_path:
                parts = value_path.split('.')
                if len(parts) >= 2:
                    nodeagent_key = parts[1]

        values[nodeagent_key] = {}

        # Image configuration
        if 'image' in na_config and nodeagent_manifest:
            img_values = self._extract_image_values(
                na_config['image'],
                nodeagent_manifest,
                component_name,
                nodeagent_key,
                workload_type='daemonset'
            )
            values[nodeagent_key].update(img_values)

        # Resources configuration
        if 'resources' in na_config and nodeagent_manifest:
            res_config = na_config['resources']
            if isinstance(res_config, dict) and 'path' in res_config:
                try:
                    resources = extract_from_manifest(nodeagent_manifest, res_config['path'])
                    if resources:
                        values[nodeagent_key]['resources'] = resources
                except (KeyError, IndexError, ValueError) as e:
                    print(f"Warning: Failed to extract resources using path '{res_config['path']}': {e}")
                    actual_resources = nodeagent_manifest['spec']['template']['spec']['containers'][0].get('resources', {})
                    if actual_resources:
                        values[nodeagent_key]['resources'] = actual_resources
            else:
                actual_resources = nodeagent_manifest['spec']['template']['spec']['containers'][0].get('resources', {})
                if actual_resources:
                    values[nodeagent_key]['resources'] = actual_resources

        return values

    def _extract_image_values(
        self,
        img_config: Dict[str, Any],
        manifest: Dict[str, Any],
        component_name: str,
        key_name: str,
        workload_type: str = 'deployment'
    ) -> Dict[str, Any]:
        """Extract image values (repository, tag, pullPolicy) from manifest.

        Args:
            img_config: Image configuration from mapper
            manifest: Kubernetes manifest (Deployment or DaemonSet)
            component_name: Component name
            key_name: Key name for this image (e.g., 'controller', 'nodeAgent')
            workload_type: 'deployment' or 'daemonset'

        Returns:
            Dictionary with image values
        """
        values = {}

        # Extract repository
        repository = None
        if 'repository' in img_config:
            repo_config = img_config['repository']
            if 'path' in repo_config:
                # Use path field to extract repository
                try:
                    repository = extract_from_manifest(manifest, repo_config['path'])
                except (KeyError, IndexError, ValueError) as e:
                    print(f"Warning: Failed to extract repository using path '{repo_config['path']}': {e}")
                    # Fallback to hardcoded path
                    actual_image = manifest['spec']['template']['spec']['containers'][0]['image']
                    repository = actual_image.rsplit(':', 1)[0] if ':' in actual_image else actual_image
            else:
                # No path field - fallback to hardcoded (backward compatibility)
                actual_image = manifest['spec']['template']['spec']['containers'][0]['image']
                repository = actual_image.rsplit(':', 1)[0] if ':' in actual_image else actual_image

        # Extract tag
        tag = None
        if 'tag' in img_config:
            tag_config = img_config['tag']
            if 'path' in tag_config:
                # Use path field to extract tag
                try:
                    tag = extract_from_manifest(manifest, tag_config['path'])

                    # Replace :latest with version for comparison consistency
                    chart_version = self.mapping['metadata'].get('appVersion', 'latest')
                    if chart_version != 'latest' and 'latest' in tag:
                        tag = tag.replace('latest', chart_version)

                except IndexError:
                    # No colon in image, use default tag
                    tag = 'latest'
                except (KeyError, ValueError) as e:
                    print(f"Warning: Failed to extract tag using path '{tag_config['path']}': {e}")
                    # Fallback to hardcoded path
                    actual_image = manifest['spec']['template']['spec']['containers'][0]['image']
                    tag = actual_image.rsplit(':', 1)[1] if ':' in actual_image else 'latest'
            else:
                # No path field - fallback to hardcoded (backward compatibility)
                actual_image = manifest['spec']['template']['spec']['containers'][0]['image']
                tag = actual_image.rsplit(':', 1)[1] if ':' in actual_image else 'latest'

            # Track useVersionAnchor fields for controller tags
            if tag_config.get('useVersionAnchor', False):
                # Build the path: component_name.key_name.tag
                anchor_path = f"{component_name}.{key_name}.tag"
                self.version_anchor_fields.append(anchor_path)

        # Extract pullPolicy
        pull_policy = None
        if 'pullPolicy' in img_config:
            policy_config = img_config['pullPolicy']
            if 'path' in policy_config:
                # Use path field to extract pullPolicy
                try:
                    pull_policy = extract_from_manifest(manifest, policy_config['path'])
                except (KeyError, IndexError, ValueError) as e:
                    print(f"Warning: Failed to extract pullPolicy using path '{policy_config['path']}': {e}")
                    # Fallback to hardcoded path
                    pull_policy = manifest['spec']['template']['spec']['containers'][0].get('imagePullPolicy', 'Always')
            else:
                # No path field - fallback to hardcoded (backward compatibility)
                pull_policy = manifest['spec']['template']['spec']['containers'][0].get('imagePullPolicy', 'Always')

        # Build values structure based on valuePath format
        repo_path = img_config.get('repository', {}).get('valuePath', '')
        if repo_path and repo_path.count('.') == 2:
            # Flat structure: kserve.controller.image
            if repository:
                values['image'] = repository
            if tag:
                values['tag'] = tag
            if pull_policy:
                values['imagePullPolicy'] = pull_policy
        else:
            # Nested structure: kserve.controller.image.repository
            values['image'] = {}
            if repository:
                values['image']['repository'] = repository
            if tag:
                values['image']['tag'] = tag
            if pull_policy:
                values['image']['pullPolicy'] = pull_policy

        return values
