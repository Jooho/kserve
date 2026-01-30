"""
Chart Generator Module

Generates Helm chart templates from Kubernetes manifests and mapping configuration.
"""

import yaml
from pathlib import Path
from typing import Dict, Any

# Import generators
from .generators import (
    WorkloadGenerator,
    ResourceGenerator,
    MetadataGenerator,
    RuntimeTemplateGenerator,
    LLMIsvcConfigGenerator,
    CommonTemplateGenerator,
    CustomDumper,
    quote_numeric_strings_in_labels,
    escape_go_templates_in_resource
)


class ChartGenerator:
    """Generates Helm chart files from manifests and mapping"""

    def __init__(self, mapping: Dict[str, Any], manifests: Dict[str, Any], output_dir: Path, repo_root: Path):
        self.mapping = mapping
        self.manifests = manifests
        self.output_dir = output_dir
        self.repo_root = repo_root
        self.templates_dir = output_dir / 'templates'

        # Initialize generators
        self.workload_gen = WorkloadGenerator(mapping)
        self.resource_gen = ResourceGenerator(mapping)
        self.metadata_gen = MetadataGenerator(mapping, self.templates_dir)
        self.runtime_gen = RuntimeTemplateGenerator(mapping)
        self.llmisvc_config_gen = LLMIsvcConfigGenerator(mapping)
        self.common_gen = CommonTemplateGenerator(mapping)

    def generate(self):
        """Generate all Helm chart files"""
        # Create output directories
        self.output_dir.mkdir(parents=True, exist_ok=True)
        self.templates_dir.mkdir(parents=True, exist_ok=True)

        # Generate Chart.yaml
        self.metadata_gen.generate_chart_yaml(self.output_dir)

        # Generate templates
        self.common_gen.generate_common_templates(self.templates_dir, self.manifests)
        self._generate_component_templates()
        self.runtime_gen.generate_runtime_templates(self.templates_dir, self.manifests)
        self.llmisvc_config_gen.generate_llmisvc_configs_templates(self.templates_dir, self.manifests)

        # Generate helpers
        self.metadata_gen.generate_helpers()

        # Generate NOTES.txt
        self.metadata_gen.generate_notes()

    def show_plan(self):
        """Show what would be generated (dry run)"""
        print(f"  Would generate Chart.yaml")
        print(f"  Would generate templates/_helpers.tpl")
        print(f"  Would generate templates/NOTES.txt")

        if 'common' in self.manifests and self.manifests['common']:
            print(f"  Would generate templates/common/")

        for component_name in self.manifests.get('components', {}).keys():
            print(f"  Would generate templates/{component_name}/")

        if self.manifests.get('runtimes'):
            print(f"  Would generate templates/runtimes/ ({len(self.manifests['runtimes'])} runtimes)")

        if self.manifests.get('llmisvcConfigs'):
            print(f"  Would generate templates/llmisvcconfigs/ ({len(self.manifests['llmisvcConfigs'])} configs)")

        if self.manifests.get('crds'):
            print(f"  Would generate templates/crds/")

    def _generate_component_templates(self):
        """Generate templates for components (kserve, llmisvc, localmodel)"""
        for component_name, component_data in self.manifests.get('components', {}).items():
            # kserve-localmodel-resources chart only contains localmodel, so skip subfolder
            chart_name = self.mapping['metadata']['name']
            if chart_name == 'kserve-localmodel-resources' and component_name == 'localmodel':
                component_dir = self.templates_dir
            else:
                component_dir = self.templates_dir / component_name
            component_dir.mkdir(exist_ok=True)

            # Generate deployment template (with values templating)
            if 'manifests' in component_data and 'controllerManager' in component_data['manifests']:
                self.workload_gen.generate_deployment(
                    component_dir,
                    component_name,
                    component_data
                )

            # Generate daemonset template for nodeAgent (with values templating)
            if 'manifests' in component_data and 'nodeAgent' in component_data['manifests']:
                self.workload_gen.generate_daemonset(
                    component_dir,
                    component_name,
                    component_data
                )

            # Generate other resources from kustomize build (static with namespace replacement)
            if 'manifests' in component_data and 'resources' in component_data['manifests']:
                copy_as_is = component_data.get('copyAsIs', False)
                self._generate_kustomize_resources(
                    component_dir,
                    component_name,
                    component_data['manifests']['resources'],
                    copy_as_is
                )

    def _generate_kustomize_resources(self, output_dir: Path, component_name: str, resources: Dict[str, Any], copy_as_is: bool = False):
        """
        Generate templates for resources from kustomize build

        Skip Deployment (we generate it separately with templating)
        For other resources, replace namespace with .Release.Namespace

        Args:
            copy_as_is: If True, copy resources as-is without escaping Go templates (for resources that already use Go templates)
        """
        chart_name = self.mapping['metadata']['name']
        is_main_component = component_name in [chart_name, 'kserve', 'llmisvc', 'localmodel']

        # KServe Core CRD names (managed by kserve-crd chart via Makefile - always skip)
        kserve_core_crds = {
            'clusterservingruntimes.serving.kserve.io',
            'inferencegraphs.serving.kserve.io',
            'inferenceservices.serving.kserve.io',
            'servingruntimes.serving.kserve.io',
            'trainedmodels.serving.kserve.io',
            'clustertrainedmodels.serving.kserve.io'
        }

        # Component-specific CRDs (these should go to crds/ directory)
        # NOTE: localmodel CRDs are managed separately by user (kserve-localmodel-crd chart)
        component_crds = {
            'llmisvc': {
                'inferenceobjectives.inference.networking.x-k8s.io',
                'inferencepoolimports.inference.networking.x-k8s.io',
                'inferencepools.inference.networking.k8s.io',
                'inferencepools.inference.networking.x-k8s.io'
            }
        }

        # Collect component-specific CRDs for crds/ directory
        crds_for_crds_dir = []

        for resource_key, resource in resources.items():
            kind = resource.get('kind')
            name = resource.get('metadata', {}).get('name', 'unnamed')

            # Skip Deployment - we generate it separately
            if kind == 'Deployment' and 'manager' in name:
                continue

            # Skip DaemonSet - we generate it separately (e.g., nodeAgent)
            if kind == 'DaemonSet':
                continue

            # Skip Namespace - Helm manages namespaces via --namespace flag and {{ .Release.Namespace }}
            # Users should create namespace with --create-namespace or beforehand
            if kind == 'Namespace':
                continue

            # Handle CustomResourceDefinitions
            if kind == 'CustomResourceDefinition':
                # Skip KServe Core CRDs (managed by kserve-crd chart via Makefile)
                if name in kserve_core_crds:
                    continue

                # Component-specific CRDs go to crds/ directory
                if component_name in component_crds and name in component_crds[component_name]:
                    crds_for_crds_dir.append((name, resource))
                    continue

                # Skip any other CRDs
                continue

            # Create sanitized filename
            filename = f"{kind.lower()}_{name}.yaml"

            # For copyAsIs resources (e.g., LLMInferenceServiceConfig with Go templates),
            # don't escape Go templates - just copy as-is with namespace replacement
            if not copy_as_is:
                # Escape Go template expressions for resources that need it
                resource = escape_go_templates_in_resource(resource)

            # Replace namespace with placeholder (will be replaced after yaml.dump)
            if 'metadata' in resource and 'namespace' in resource['metadata']:
                resource['metadata']['namespace'] = '__NAMESPACE_PLACEHOLDER__'

            # For webhook configurations, also replace namespace in webhooks
            if kind in ['MutatingWebhookConfiguration', 'ValidatingWebhookConfiguration']:
                if 'webhooks' in resource:
                    for webhook in resource['webhooks']:
                        if 'clientConfig' in webhook and 'service' in webhook['clientConfig']:
                            webhook['clientConfig']['service']['namespace'] = '__NAMESPACE_PLACEHOLDER__'

            # Main component resources are always installed, localmodel needs enabled check
            # Use CustomDumper to handle LiteralString properly (for multiline args)
            # Use very large width to prevent YAML from breaking Helm template expressions across lines
            if is_main_component:
                template = yaml.dump(resource, Dumper=CustomDumper, default_flow_style=False, sort_keys=False, width=float('inf'))
                # Quote numeric strings in labels
                template = quote_numeric_strings_in_labels(template)
                # Replace namespace placeholder with Helm template
                template = template.replace('namespace: __NAMESPACE_PLACEHOLDER__', 'namespace: {{ .Release.Namespace }}')
            else:
                enabled_path = f"{component_name}.enabled"
                template = f'{{{{- if .Values.{enabled_path} }}}}\n'
                resource_yaml = yaml.dump(resource, Dumper=CustomDumper, default_flow_style=False, sort_keys=False, width=float('inf'))
                # Quote numeric strings in labels
                resource_yaml = quote_numeric_strings_in_labels(resource_yaml)
                # Replace namespace placeholder with Helm template
                resource_yaml = resource_yaml.replace('namespace: __NAMESPACE_PLACEHOLDER__', 'namespace: {{ .Release.Namespace }}')
                template += resource_yaml
                template += '{{- end }}\n'

            # Write template file
            output_file = output_dir / filename
            with open(output_file, 'w') as f:
                f.write(template)

        # Generate component-specific CRDs in crds/ directory
        if crds_for_crds_dir:
            crds_dir = self.output_dir / 'crds'
            crds_dir.mkdir(exist_ok=True)

            for crd_name, crd_resource in crds_for_crds_dir:
                # CRDs in crds/ directory should not have namespace
                if 'metadata' in crd_resource and 'namespace' in crd_resource['metadata']:
                    del crd_resource['metadata']['namespace']

                # CRDs don't need templating - write as-is
                filename = f"{crd_name}.yaml"
                crd_yaml = yaml.dump(crd_resource, Dumper=CustomDumper, default_flow_style=False, sort_keys=False, width=float('inf'))

                output_file = crds_dir / filename
                with open(output_file, 'w') as f:
                    f.write(crd_yaml)
