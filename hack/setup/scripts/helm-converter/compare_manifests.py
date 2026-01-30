#!/usr/bin/env python3
"""
Manifest comparison tool for Kustomize vs Helm outputs
Validates that Helm charts generate equivalent resources to Kustomize
"""

import yaml
import sys
import subprocess
from typing import Dict, List, Any, Tuple
from pathlib import Path


def run_command(cmd: List[str]) -> str:
    """Run shell command and return output"""
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise Exception(f"Command failed: {' '.join(cmd)}\n{result.stderr}")
    return result.stdout


def read_kserve_version(repo_root: Path) -> str:
    """
    Read KSERVE_VERSION from kserve-deps.env file.

    Returns:
        KSERVE_VERSION value (e.g., "v0.16.0" or "latest")
    """
    deps_file = repo_root / 'kserve-deps.env'
    try:
        with open(deps_file, 'r') as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith('#'):
                    if '=' in line and line.startswith('KSERVE_VERSION'):
                        return line.split('=')[1].strip()
    except Exception as e:
        print(f"Warning: Could not read kserve-deps.env: {e}")
    return 'latest'


def load_manifests(file_path: str) -> List[Dict[str, Any]]:
    """Load all YAML documents from a file"""
    with open(file_path, 'r') as f:
        docs = list(yaml.safe_load_all(f))
    return [doc for doc in docs if doc is not None]


def normalize_labels_annotations(obj: Any) -> Any:
    """Recursively normalize label/annotation values (convert numbers to strings)"""
    if isinstance(obj, dict):
        return {k: normalize_labels_annotations(v) for k, v in obj.items()}
    elif isinstance(obj, list):
        return [normalize_labels_annotations(item) for item in obj]
    elif isinstance(obj, (int, float)):
        # Convert numbers to strings for label/annotation comparison
        return str(obj)
    else:
        return obj


def normalize_resource(resource: Dict[str, Any]) -> Dict[str, Any]:
    """Remove Helm-specific fields that we want to ignore"""
    import copy
    normalized = copy.deepcopy(resource)

    # Helper function to normalize labels/annotations in metadata
    def normalize_metadata(metadata: Dict[str, Any]):
        if 'labels' in metadata:
            labels = metadata['labels']
            helm_labels = [
                'helm.sh/chart',
                'app.kubernetes.io/managed-by',
                'app.kubernetes.io/instance',
                'app.kubernetes.io/name',
                'app.kubernetes.io/version'
            ]
            for label in helm_labels:
                labels.pop(label, None)
            # Normalize label values (convert numbers to strings)
            metadata['labels'] = normalize_labels_annotations(labels)
            # Remove empty labels dict
            if not metadata['labels']:
                del metadata['labels']

        if 'annotations' in metadata:
            annotations = metadata['annotations']
            helm_annotations = ['meta.helm.sh/release-name', 'meta.helm.sh/release-namespace']
            for annotation in helm_annotations:
                annotations.pop(annotation, None)
            # Normalize annotation values (convert numbers to strings)
            metadata['annotations'] = normalize_labels_annotations(annotations)
            # Remove empty annotations dict
            if not metadata['annotations']:
                del metadata['annotations']

    # Normalize top-level metadata
    if 'metadata' in normalized:
        normalize_metadata(normalized['metadata'])

    # Normalize spec.template.metadata (for Deployments, etc.)
    if normalized.get('kind') in ['Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'CronJob']:
        if 'spec' in normalized and 'template' in normalized['spec']:
            if 'metadata' in normalized['spec']['template']:
                normalize_metadata(normalized['spec']['template']['metadata'])

    # Normalize spec.selector.matchLabels
    if 'spec' in normalized and 'selector' in normalized['spec']:
        if 'matchLabels' in normalized['spec']['selector']:
            normalized['spec']['selector']['matchLabels'] = normalize_labels_annotations(
                normalized['spec']['selector']['matchLabels']
            )

    # Normalize ConfigMap data fields (parse YAML/JSON strings to dicts for comparison)
    if normalized.get('kind') == 'ConfigMap' and 'data' in normalized:
        data = normalized['data']
        normalized_data = {}
        for key, value in data.items():
            # Skip _example key (only in Kustomize, not in Helm)
            if key == '_example':
                continue
            # If value is a string, try to parse it as YAML/JSON
            if isinstance(value, str):
                try:
                    normalized_data[key] = yaml.safe_load(value)
                except:
                    # If parsing fails, keep as string
                    normalized_data[key] = value
            else:
                normalized_data[key] = value
        normalized['data'] = normalized_data

    return normalized


def get_resource_key(resource: Dict[str, Any]) -> str:
    """Get unique key for a resource (kind/namespace/name)"""
    kind = resource.get('kind', 'Unknown')
    name = resource.get('metadata', {}).get('name', 'unknown')
    namespace = resource.get('metadata', {}).get('namespace', '')
    return f"{kind}/{namespace}/{name}" if namespace else f"{kind}/{name}"


def sort_dict_deep(obj: Any) -> Any:
    """Recursively sort dictionary keys for consistent comparison"""
    if isinstance(obj, dict):
        return {k: sort_dict_deep(v) for k, v in sorted(obj.items())}
    elif isinstance(obj, list):
        return [sort_dict_deep(item) for item in obj]
    else:
        return obj


def get_value_from_path(obj: Dict[str, Any], path: str) -> Any:
    """
    Extract value from object using path like 'spec.containers[0].image'

    Supports:
    - Nested dict access: 'spec.template.spec'
    - Array index: 'containers[0]'
    - Split operation: 'image+(:,0)' splits by ':' and takes first part
    """
    import re

    # Handle split operation (e.g., 'path+(:,0)')
    if '+(' in path:
        base_path, split_op = path.split('+(', 1)
        split_op = split_op.rstrip(')')
    else:
        base_path = path
        split_op = None

    # Navigate path
    value = obj
    parts = base_path.split('.')

    for part in parts:
        # Handle array index: containers[0]
        match = re.match(r'(\w+)\[(\d+)\]', part)
        if match:
            key, index = match.groups()
            value = value[key][int(index)]
        else:
            value = value[part]

    # Apply split if specified
    if split_op:
        delimiter, index = split_op.split(',')
        index = int(index)
        if delimiter == ':':
            value = value.rsplit(delimiter, 1)[index]
        else:
            value = value.split(delimiter)[index]

    return value


def compare_configmap_data(kustomize_cm: Dict[str, Any], helm_cm: Dict[str, Any]) -> Tuple[bool, str]:
    """
    Compare ConfigMap data fields with JSON parsing and deep sorting

    This function handles JSON-formatted data fields by parsing them,
    sorting keys recursively, and comparing semantically.
    """
    import json

    k_data = kustomize_cm.get('data', {})
    h_data = helm_cm.get('data', {})

    # Ignore _example key (documentation field, not actual config)
    k_data_filtered = {k: v for k, v in k_data.items() if k != '_example'}
    h_data_filtered = {k: v for k, v in h_data.items() if k != '_example'}

    # Check keys match
    if set(k_data_filtered.keys()) != set(h_data_filtered.keys()):
        missing = set(k_data_filtered.keys()) - set(h_data_filtered.keys())
        extra = set(h_data_filtered.keys()) - set(k_data_filtered.keys())
        return False, f"Keys differ - missing: {missing}, extra: {extra}"

    # Compare each data field
    differences = []
    for key in k_data_filtered.keys():
        try:
            # Try to parse as JSON
            k_json = json.loads(k_data_filtered[key])
            h_json = json.loads(h_data_filtered[key])

            # Deep sort for consistent comparison
            k_sorted = sort_dict_deep(k_json)
            h_sorted = sort_dict_deep(h_json)

            # Compare
            if k_sorted != h_sorted:
                differences.append(f"  - data.{key}")
        except json.JSONDecodeError as e:
            # Not JSON, compare as string
            if k_data_filtered[key] != h_data_filtered[key]:
                differences.append(f"  - data.{key} (string)")

    if differences:
        return False, "Data fields differ:\n" + "\n".join(differences)

    return True, "All data fields match"


def load_mappers(repo_root: Path) -> Dict[str, Any]:
    """Load all mapper YAML files from mappers directory"""
    mapper_dir = repo_root / 'hack/setup/scripts/helm-converter/mappers'
    mappers = {}

    for mapper_file in mapper_dir.glob('helm-mapping-*.yaml'):
        with open(mapper_file) as f:
            mapper = yaml.safe_load(f)
            mappers[mapper_file.stem] = mapper

    return mappers


def find_mapper_config_for_resource(mappers: Dict[str, Any], resource: Dict[str, Any]) -> Dict[str, Any]:
    """
    Find mapper configuration for a given resource

    Searches through all mapper files to find the config for a specific resource
    based on its kind and name.
    """
    kind = resource.get('kind')
    name = resource.get('metadata', {}).get('name', '')

    # Search in all mappers
    for mapper_name, mapper in mappers.items():
        # Check runtimes (ClusterServingRuntime)
        if 'runtimes' in mapper or 'clusterServingRuntimes' in mapper:
            runtimes_key = 'runtimes' if 'runtimes' in mapper else 'clusterServingRuntimes'
            runtime_list = mapper.get(runtimes_key, {}).get('runtimes', [])
            for runtime in runtime_list:
                if runtime.get('name') == name and runtime.get('kind', 'ClusterServingRuntime') == kind:
                    return runtime

        # Check components (kserve, llmisvc, localmodel)
        for comp_key in ['kserve', 'llmisvc', 'localmodel']:
            if comp_key in mapper:
                comp = mapper[comp_key]

                # Check controllerManager (Deployment)
                if 'controllerManager' in comp:
                    cm = comp['controllerManager']
                    if cm.get('name') == name and cm.get('kind', 'Deployment') == kind:
                        return cm

                # Check nodeAgent (DaemonSet)
                if 'nodeAgent' in comp:
                    na = comp['nodeAgent']
                    if na.get('name') == name and na.get('kind', 'DaemonSet') == kind:
                        return na

    return None


def compare_mapped_fields(kustomize_resource: Dict[str, Any], helm_resource: Dict[str, Any],
                          mapper_config: Dict[str, Any]) -> Tuple[bool, str]:
    """
    Compare only fields defined in mapper configuration

    This allows semantic comparison of only the fields that Helm manages,
    ignoring fields that are copied as-is from kustomize.
    """
    differences = []

    # Compare image fields
    if 'image' in mapper_config:
        img_config = mapper_config['image']

        # Repository
        if 'repository' in img_config and 'path' in img_config['repository']:
            path = img_config['repository']['path']
            try:
                k_val = get_value_from_path(kustomize_resource, path)
                h_val = get_value_from_path(helm_resource, path)
                if k_val != h_val:
                    differences.append(f"  - image.repository: '{k_val}' vs '{h_val}'")
            except (KeyError, IndexError, TypeError) as e:
                differences.append(f"  - image.repository: path error - {e}")

        # Tag
        if 'tag' in img_config and 'path' in img_config['tag']:
            path = img_config['tag']['path']
            try:
                k_val = get_value_from_path(kustomize_resource, path)
                h_val = get_value_from_path(helm_resource, path)
                if k_val != h_val:
                    differences.append(f"  - image.tag: '{k_val}' vs '{h_val}'")
            except (KeyError, IndexError, TypeError) as e:
                differences.append(f"  - image.tag: path error - {e}")

    # Compare resources
    if 'resources' in mapper_config and isinstance(mapper_config['resources'], dict):
        if 'path' in mapper_config['resources']:
            path = mapper_config['resources']['path']
            try:
                k_val = get_value_from_path(kustomize_resource, path)
                h_val = get_value_from_path(helm_resource, path)
                if k_val != h_val:
                    differences.append(f"  - resources differ")
            except (KeyError, IndexError, TypeError) as e:
                differences.append(f"  - resources: path error - {e}")

    if differences:
        return False, "Mapped fields differ:\n" + "\n".join(differences)

    return True, "All mapped fields match"


def compare_resources(kustomize_docs: List[Dict], helm_docs: List[Dict],
                      test_name: str, mappers: Dict[str, Any] = None,
                      exclude_crds: bool = True) -> Tuple[bool, str]:
    """
    Compare two sets of manifests and return (success, report)

    Uses mapper-based comparison for resources with mapper configs,
    and full comparison for resources without mapper configs.
    """

    # Filter out CRDs if requested
    if exclude_crds:
        kustomize_docs = [d for d in kustomize_docs if d.get('kind') != 'CustomResourceDefinition']
        helm_docs = [d for d in helm_docs if d.get('kind') != 'CustomResourceDefinition']

    # Index resources by kind/name
    kustomize_resources = {get_resource_key(r): normalize_resource(r) for r in kustomize_docs}
    helm_resources = {get_resource_key(r): normalize_resource(r) for r in helm_docs}

    # Find missing resources
    only_in_kustomize = set(kustomize_resources.keys()) - set(helm_resources.keys())
    only_in_helm = set(helm_resources.keys()) - set(kustomize_resources.keys())
    common = set(kustomize_resources.keys()) & set(helm_resources.keys())

    # Build report
    report = []
    report.append(f"\n{'='*60}")
    report.append(f"Test: {test_name}")
    report.append(f"{'='*60}")
    report.append(f"Kustomize: {len(kustomize_docs)} resources")
    report.append(f"Helm: {len(helm_docs)} resources")

    success = True

    # Namespace differences are allowed (Helm doesn't create namespaces)
    namespace_only = {k for k in only_in_kustomize if k.startswith('Namespace/')}
    other_only_kustomize = only_in_kustomize - namespace_only

    if namespace_only:
        report.append(f"\n✓ Namespaces only in Kustomize (expected): {len(namespace_only)}")
        for key in sorted(namespace_only):
            report.append(f"  - {key}")

    if other_only_kustomize:
        report.append(f"\n❌ Only in Kustomize ({len(other_only_kustomize)}):")
        for key in sorted(other_only_kustomize):
            report.append(f"  - {key}")
        success = False

    if only_in_helm:
        report.append(f"\n❌ Only in Helm ({len(only_in_helm)}):")
        for key in sorted(only_in_helm):
            report.append(f"  - {key}")
        success = False

    report.append(f"\n✓ Common resources: {len(common)}")

    # Check for differences in common resources
    # New approach: use mapper-based comparison for resources with mapper configs
    critical_differences = []
    minor_differences = []

    for key in sorted(common):
        # Get original resources (before normalization)
        k_original = next(r for r in kustomize_docs if get_resource_key(r) == key)
        h_original = next(r for r in helm_docs if get_resource_key(r) == key)

        # Special handling for inferenceservice-config ConfigMap
        if key == 'ConfigMap/kserve/inferenceservice-config':
            success, msg = compare_configmap_data(k_original, h_original)
            if not success:
                critical_differences.append(key)
            continue

        # Try to find mapper config for this resource
        mapper_config = None
        if mappers:
            mapper_config = find_mapper_config_for_resource(mappers, k_original)

        if mapper_config:
            # Use mapper-based comparison (only compare defined fields)
            success, msg = compare_mapped_fields(k_original, h_original, mapper_config)
            if not success:
                critical_differences.append(key)
        else:
            # No mapper config - use old comparison logic
            # Get normalized resources (labels/annotations removed)
            k_normalized = kustomize_resources[key]
            h_normalized = helm_resources[key]

            # If originals differ
            if k_original != h_original:
                # Check if normalized versions are the same
                if k_normalized == h_normalized:
                    # Only labels/annotations differ -> minor difference (warning)
                    minor_differences.append(key)
                else:
                    # Other fields differ -> critical difference (error)
                    critical_differences.append(key)

    if critical_differences:
        report.append(f"\n❌ Resources with CRITICAL differences ({len(critical_differences)}):")
        report.append("   (Fields other than labels/annotations differ)")
        for key in critical_differences:
            report.append(f"  - {key}")
        success = False

    if minor_differences:
        report.append(f"\n⚠️  Resources with minor differences ({len(minor_differences)}):")
        report.append("   (Only labels/annotations differ)")
        for key in minor_differences:
            report.append(f"  - {key}")

    if not critical_differences and not minor_differences:
        report.append("\n✓ All common resources are identical!")

    if success and not minor_differences:
        report.append("\n✅ PASS: Manifests are equivalent!")
    elif success and minor_differences:
        report.append("\n⚠️  PASS with warnings: Minor differences found")
    else:
        report.append("\n❌ FAIL: Critical differences found")

    return success, '\n'.join(report)


def main():
    """Run all comparison tests"""

    repo_root = Path.cwd()
    reports = []
    all_success = True

    # Test configurations
    tests = [
        {
            'name': 'KServe (standalone)',
            'kustomize_target': 'config/overlays/standalone/kserve',
            'helm_charts': ['./charts/kserve-resources'],
            'helm_values': {'localmodel.enabled': False},
        },
        {
            'name': 'KServe + LocalModel',
            'kustomize_overlays': ['config/overlays/standalone/kserve', 'config/overlays/addons/localmodel'],
            'helm_charts': ['./charts/kserve-resources', './charts/kserve-localmodel-resources'],
            'helm_values': {'localmodel.enabled': 'true'},
        },
        {
            'name': 'LLM Inference Service',
            'kustomize_target': 'config/overlays/standalone/llmisvc',
            'helm_charts': ['./charts/kserve-llmisvc-resources'],
            'helm_values': {'localmodel.enabled': False},
            'note': 'LLMInferenceServiceConfig resources contain Go templates and cannot be converted to Helm',
        },
        {
            'name': 'LLM Inference Service + LocalModel',
            'kustomize_overlays': ['config/overlays/standalone/llmisvc', 'config/overlays/addons/localmodel'],
            'helm_charts': ['./charts/kserve-llmisvc-resources', './charts/kserve-localmodel-resources'],
            'helm_values': {'localmodel.enabled': 'true'},
            'note': 'LLMInferenceServiceConfig resources contain Go templates and cannot be converted to Helm',
        },
        {
            'name': 'ClusterServingRuntimes',
            'kustomize_target': 'config/runtimes',
            'helm_charts': ['./charts/kserve-runtime-configs'],
            'helm_values': {'runtimes.enabled': True, 'llmisvcConfigs.enabled': False},
            'exclude_crds': False,
        },
        {
            'name': 'LLMInferenceServiceConfigs',
            'kustomize_target': 'config/llmisvcconfig',
            'helm_charts': ['./charts/kserve-runtime-configs'],
            'helm_values': {'runtimes.enabled': False, 'llmisvcConfigs.enabled': True},
        },
    ]

    # Read KSERVE_VERSION for image tag replacement
    kserve_version = read_kserve_version(repo_root)

    # Load mapper configurations for field-based comparison
    mappers = load_mappers(repo_root)

    for test in tests:
        try:
            print(f"\nRunning: {test['name']}...")

            # Build Kustomize output
            if 'kustomize_overlays' in test:
                # Multiple overlays need to be combined
                kustomize_docs = []
                for overlay in test['kustomize_overlays']:
                    output = run_command(['kustomize', 'build', overlay])
                    # Replace ':latest' with version from kserve-deps.env for comparison
                    # This handles both ':latest' and ':latest-gpu' -> ':v0.16.0-gpu'
                    # Use regex to only replace image tags, not other occurrences of 'latest'
                    import re
                    output = re.sub(r':latest(-\w+)?', rf':{kserve_version}\1', output)
                    docs = list(yaml.safe_load_all(output))
                    kustomize_docs.extend([d for d in docs if d])
            else:
                output = run_command(['kustomize', 'build', test['kustomize_target']])
                # Replace ':latest' with version from kserve-deps.env for comparison
                # This handles both ':latest' and ':latest-gpu' -> ':v0.16.0-gpu'
                # Use regex to only replace image tags, not other occurrences of 'latest'
                import re
                output = re.sub(r':latest(-\w+)?', rf':{kserve_version}\1', output)
                kustomize_docs = list(yaml.safe_load_all(output))
                kustomize_docs = [d for d in kustomize_docs if d]

            # Build Helm output
            if 'helm_charts' in test:
                helm_docs = []
                for chart in test['helm_charts']:
                    cmd = ['helm', 'template', 'test', chart, '--namespace', 'kserve']

                    # Add values
                    for key, value in test.get('helm_values', {}).items():
                        cmd.extend(['--set', f'{key}={str(value).lower()}'])

                    output = run_command(cmd)
                    docs = list(yaml.safe_load_all(output))
                    helm_docs.extend([d for d in docs if d])
            else:
                helm_docs = []

            # Compare
            exclude_crds = test.get('exclude_crds', True)
            success, report = compare_resources(kustomize_docs, helm_docs, test['name'], mappers, exclude_crds)
            reports.append(report)

            if not success:
                all_success = False

        except Exception as e:
            report = f"\n{'='*60}\n"
            report += f"Test: {test['name']}\n"
            report += f"{'='*60}\n"
            report += f"❌ ERROR: {str(e)}\n"
            reports.append(report)
            all_success = False

    # Print all reports
    for report in reports:
        print(report)

    # Final summary
    print("\n" + "="*60)
    if all_success:
        print("✅ ALL TESTS PASSED")
        return 0
    else:
        print("❌ SOME TESTS FAILED")
        return 1


if __name__ == '__main__':
    sys.exit(main())
