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
        # Generic workload configuration (Deployment, DaemonSet, etc.)
        component_data = manifests.get('components', {}).get(component_name)

        if component_data:
            for config_key, config_value in component_config.items():
                # Check if this is a workload config (has 'kind' field)
                if not isinstance(config_value, dict) or 'kind' not in config_value:
                    continue

                # Get the workload manifest
                workload_manifest = None
                if 'manifests' in component_data and config_key in component_data['manifests']:
                    workload_manifest = component_data['manifests'][config_key]

                # Build workload values using generic method
                workload_kind = config_value['kind']
                workload_values = self._build_workload_values(
                    component_name,
                    config_key,
                    config_value,
                    workload_manifest,
                    workload_kind
                )
                values.update(workload_values)

        return values

    def _build_workload_values(
        self,
        component_name: str,
        config_key: str,
        workload_config: Dict[str, Any],
        workload_manifest: Optional[Dict[str, Any]],
        workload_kind: str
    ) -> Dict[str, Any]:
        """Build workload values (generic for Deployment, DaemonSet, etc.).

        Unified method that replaces _build_controller_manager_values() and
        _build_node_agent_values().

        Args:
            component_name: Component name
            config_key: Key in component_config (e.g., 'controllerManager', 'nodeAgent')
            workload_config: Workload configuration from mapper
            workload_manifest: Workload manifest (Deployment, DaemonSet, etc.)
            workload_kind: Kind of workload ('Deployment', 'DaemonSet', etc.)

        Returns:
            Workload values dictionary
        """
        values = {}

        # Extract the base path from valuePath (e.g., "kserve.controller" from "kserve.controller.image")
        # Default to config_key if not specified
        workload_key = config_key

        if 'image' in workload_config and 'repository' in workload_config['image']:
            value_path = workload_config['image']['repository'].get('valuePath', '')
            if value_path:
                parts = value_path.split('.')
                if len(parts) >= 2:
                    # The second part is the workload key (e.g., 'controller', 'nodeAgent')
                    workload_key = parts[1]

        values[workload_key] = {}

        # Determine workload type for _extract_image_values
        # 'Deployment' -> 'deployment', 'DaemonSet' -> 'daemonset'
        workload_type = workload_kind.lower()

        # Image configuration
        if 'image' in workload_config and workload_manifest:
            img_values = self._extract_image_values(
                workload_config['image'],
                workload_manifest,
                component_name,
                workload_key,
                workload_type=workload_type
            )
            values[workload_key].update(img_values)

        # Resources configuration
        if 'resources' in workload_config and workload_manifest:
            res_config = workload_config['resources']
            if isinstance(res_config, dict) and 'path' in res_config:
                try:
                    resources = extract_from_manifest(workload_manifest, res_config['path'])
                    if resources:
                        values[workload_key]['resources'] = resources
                except (KeyError, IndexError, ValueError) as e:
                    print(f"Warning: Failed to extract resources using path '{res_config['path']}': {e}")
                    # Fallback to hardcoded path
                    actual_resources = workload_manifest['spec']['template']['spec']['containers'][0].get('resources', {})
                    if actual_resources:
                        values[workload_key]['resources'] = actual_resources
            else:
                # No path field - fallback to hardcoded (backward compatibility)
                actual_resources = workload_manifest['spec']['template']['spec']['containers'][0].get('resources', {})
                if actual_resources:
                    values[workload_key]['resources'] = actual_resources

        # Generic fields (nodeSelector, affinity, tolerations, etc.)
        # All fields except 'image' and 'resources' are processed generically
        generic_fields = self._extract_generic_fields(
            workload_config,
            workload_manifest,
            config_key
        )
        values[workload_key].update(generic_fields)

        return values

    def _extract_generic_fields(
        self,
        config: Dict[str, Any],
        manifest: Optional[Dict[str, Any]],
        manifest_type: str
    ) -> Dict[str, Any]:
        """Extract generic fields from manifest based on mapper configuration.

        This method provides generic field extraction for fields that follow
        the standard path-based extraction pattern. Special fields like 'image'
        and 'resources' that require complex processing are excluded.

        Args:
            config: Configuration from mapper (e.g., na_config, cm_config)
            manifest: Kubernetes manifest (Deployment or DaemonSet)
            manifest_type: 'controllerManager' or 'nodeAgent'

        Returns:
            Dictionary with extracted field values
        """
        result = {}

        if not manifest:
            return result

        # Special fields that need custom processing logic
        SPECIAL_FIELDS = {'image', 'resources'}

        # Process all fields generically
        for field_name, field_config in config.items():
            # Skip special fields - they have their own processing logic
            if field_name in SPECIAL_FIELDS:
                continue

            # Generic extraction for path-based fields
            if isinstance(field_config, dict) and 'path' in field_config:
                try:
                    value = extract_from_manifest(manifest, field_config['path'])
                    # Allow None check to include empty dict {} and empty list []
                    if value is not None:
                        result[field_name] = value
                except (KeyError, IndexError, ValueError) as e:
                    print(f"Warning: Failed to extract {field_name} for {manifest_type} "
                          f"using path '{field_config['path']}': {e}")

        return result

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
