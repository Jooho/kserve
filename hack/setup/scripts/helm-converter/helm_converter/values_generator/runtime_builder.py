"""
Runtime Builder Module

Builds runtime values for ClusterServingRuntimes.
"""

from typing import Dict, Any
from .path_extractor import extract_from_manifest


class RuntimeBuilder:
    """Builds runtime values for ClusterServingRuntimes"""

    def __init__(self, mapping: Dict[str, Any]):
        self.mapping = mapping
        self.version_anchor_fields = []

    def build_runtime_values(
        self,
        runtime_key: str,
        manifests: Dict[str, Any]
    ) -> Dict[str, Any]:
        """Build runtime values from specified key.

        Uses actual values from kustomize build result, not defaultValue from mapping.
        This ensures that helm template output matches kustomize build output.

        Args:
            runtime_key: Key in mapping ('clusterServingRuntimes' or 'runtimes')
            manifests: Kubernetes manifests

        Returns:
            Runtime values dictionary
        """
        runtimes_config = self.mapping[runtime_key]
        values = {}

        # Global enabled flag - runtimes are typically enabled
        if 'enabled' in runtimes_config:
            values['enabled'] = True

        # Individual runtime configurations
        for runtime_config in runtimes_config.get('runtimes', []):
            runtime_key_name = self._extract_runtime_key(runtime_config['enabledPath'])
            runtime_name = runtime_config['name']

            # Find the corresponding manifest
            runtime_manifest = None
            for runtime_data in manifests.get('runtimes', []):
                if runtime_data['config']['name'] == runtime_name:
                    runtime_manifest = runtime_data['manifest']
                    break

            # Skip if manifest not found (shouldn't happen in production)
            if not runtime_manifest:
                continue

            values[runtime_key_name] = {}
            values[runtime_key_name]['enabled'] = True  # Individual runtimes are enabled by default

            # Image configuration - use path field if available
            if 'image' in runtime_config:
                img_values = self._extract_runtime_image_values(
                    runtime_config['image'],
                    runtime_manifest,
                    runtime_key_name,
                    runtime_name
                )
                values[runtime_key_name]['image'] = img_values

            # Resources configuration - use path field if available
            if 'resources' in runtime_config:
                res_config = runtime_config['resources']
                if 'path' in res_config:
                    try:
                        resources = extract_from_manifest(runtime_manifest, res_config['path'])
                        if resources:
                            values[runtime_key_name]['resources'] = resources
                    except (KeyError, IndexError, ValueError) as e:
                        print(f"Warning: Failed to extract resources for {runtime_name}: {e}")
                        # Fallback to hardcoded
                        actual_resources = runtime_manifest['spec']['containers'][0].get('resources', {})
                        if actual_resources:
                            values[runtime_key_name]['resources'] = actual_resources
                else:
                    # No path field - fallback to hardcoded (backward compatibility)
                    actual_resources = runtime_manifest['spec']['containers'][0].get('resources', {})
                    if actual_resources:
                        values[runtime_key_name]['resources'] = actual_resources

        return values

    def _extract_runtime_image_values(
        self,
        img_config: Dict[str, Any],
        runtime_manifest: Dict[str, Any],
        runtime_key: str,
        runtime_name: str
    ) -> Dict[str, Any]:
        """Extract image values for a runtime.

        Args:
            img_config: Image configuration from mapper
            runtime_manifest: Runtime manifest
            runtime_key: Runtime key name (e.g., 'sklearn')
            runtime_name: Runtime full name

        Returns:
            Image values dictionary
        """
        img_values = {}

        # Extract repository
        if 'repository' in img_config:
            repo_config = img_config['repository']
            if 'path' in repo_config:
                try:
                    repository = extract_from_manifest(runtime_manifest, repo_config['path'])
                    img_values['repository'] = repository
                except (KeyError, IndexError, ValueError) as e:
                    print(f"Warning: Failed to extract repository for {runtime_name}: {e}")
                    # Fallback to hardcoded
                    actual_image = runtime_manifest['spec']['containers'][0]['image']
                    repository = actual_image.rsplit(':', 1)[0] if ':' in actual_image else actual_image
                    img_values['repository'] = repository
            else:
                # No path field - fallback to hardcoded (backward compatibility)
                actual_image = runtime_manifest['spec']['containers'][0]['image']
                repository = actual_image.rsplit(':', 1)[0] if ':' in actual_image else actual_image
                img_values['repository'] = repository

        # Extract tag
        if 'tag' in img_config:
            tag_config = img_config['tag']
            if 'path' in tag_config:
                try:
                    tag = extract_from_manifest(runtime_manifest, tag_config['path'])

                    # Replace :latest with version for comparison consistency
                    # compare_manifests.py does the same replacement on kustomize output
                    # Get version from Chart metadata
                    chart_version = self.mapping['metadata'].get('appVersion', 'latest')
                    if chart_version != 'latest' and 'latest' in tag:
                        tag = tag.replace('latest', chart_version)

                    img_values['tag'] = tag

                    # Track useVersionAnchor fields for runtimes
                    if tag_config.get('useVersionAnchor'):
                        value_path = tag_config.get('valuePath', '')
                        if value_path:
                            self.version_anchor_fields.append(value_path)
                except IndexError:
                    # No colon in image, use default tag
                    img_values['tag'] = 'latest'
                except (KeyError, ValueError) as e:
                    print(f"Warning: Failed to extract tag for {runtime_name}: {e}")
                    # Fallback to hardcoded
                    actual_image = runtime_manifest['spec']['containers'][0]['image']
                    tag = actual_image.rsplit(':', 1)[1] if ':' in actual_image else 'latest'
                    img_values['tag'] = tag
            else:
                # No path field - fallback to hardcoded (backward compatibility)
                actual_image = runtime_manifest['spec']['containers'][0]['image']
                tag = actual_image.rsplit(':', 1)[1] if ':' in actual_image else 'latest'
                img_values['tag'] = tag

        return img_values

    def _extract_runtime_key(self, enabled_path: str) -> str:
        """Extract runtime key from enabledPath.

        Args:
            enabled_path: Enabled path (e.g., "runtimes.sklearn.enabled")

        Returns:
            Runtime key (e.g., "sklearn")
        """
        parts = enabled_path.split('.')
        if len(parts) >= 2:
            return parts[1]
        return parts[0]
