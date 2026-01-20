"""
Chart Generator Module

Generates Helm chart templates from Kubernetes manifests and mapping configuration.
"""

import yaml
from pathlib import Path
from typing import Dict, Any, List
import re


class LiteralString(str):
    """String subclass to represent literal block scalars in YAML"""
    pass


class CustomDumper(yaml.SafeDumper):
    """Custom YAML dumper that represents multiline strings as literal block scalars"""
    pass


def str_representer(dumper, data):
    """Represent strings, using literal block scalar for multiline strings"""
    if '\n' in data:
        return dumper.represent_scalar('tag:yaml.org,2002:str', data, style='|-')
    return dumper.represent_scalar('tag:yaml.org,2002:str', data)


def literal_str_representer(dumper, data):
    """Represent LiteralString as YAML literal block scalar (|-)"""
    return dumper.represent_scalar('tag:yaml.org,2002:str', data, style='|-')


# Add custom representers
CustomDumper.add_representer(str, str_representer)
CustomDumper.add_representer(LiteralString, literal_str_representer)


class ChartGenerator:
    """Generates Helm chart files from manifests and mapping"""

    def __init__(self, mapping: Dict[str, Any], manifests: Dict[str, Any], output_dir: Path, repo_root: Path):
        self.mapping = mapping
        self.manifests = manifests
        self.output_dir = output_dir
        self.repo_root = repo_root
        self.templates_dir = output_dir / 'templates'

    def generate(self):
        """Generate all Helm chart files"""
        # Create output directories
        self.output_dir.mkdir(parents=True, exist_ok=True)
        self.templates_dir.mkdir(parents=True, exist_ok=True)

        # Generate Chart.yaml
        self._generate_chart_yaml()

        # Generate templates
        self._generate_common_templates()
        self._generate_component_templates()
        self._generate_runtime_templates()
        self._generate_crd_templates()

        # Generate helpers
        self._generate_helpers()

        # Generate NOTES.txt
        self._generate_notes()

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

        if self.manifests.get('crds'):
            print(f"  Would generate templates/crds/")

    def _generate_chart_yaml(self):
        """Generate Chart.yaml"""
        metadata = self.mapping['metadata']
        chart_yaml = {
            'apiVersion': 'v2',
            'name': metadata['name'],
            'description': metadata['description'],
            'type': 'application',
            'version': metadata['version'],
            'appVersion': metadata['appVersion'],
            'keywords': ['kserve', 'inference', 'machine-learning'],
            'home': 'https://kserve.github.io/website/',
            'sources': ['https://github.com/kserve/kserve'],
            'maintainers': [
                {
                    'name': 'KServe Community',
                    'email': 'kserve-technical-discuss@lists.lfaidata.foundation'
                }
            ]
        }

        chart_file = self.output_dir / 'Chart.yaml'
        with open(chart_file, 'w') as f:
            yaml.dump(chart_yaml, f, default_flow_style=False, sort_keys=False)

    def _generate_common_templates(self):
        """Generate templates for common/base resources (inferenceServiceConfig and certManager)"""
        if 'common' not in self.manifests or not self.manifests['common']:
            return

        common_dir = self.templates_dir / 'common'
        common_dir.mkdir(exist_ok=True)

        # Generate ConfigMap template if inferenceServiceConfig is enabled
        if 'inferenceservice-config' in self.manifests['common'] and 'inferenceServiceConfig' in self.mapping:
            self._generate_configmap_template(common_dir)

        # Generate cert-manager Issuer template if certManager is enabled
        if 'certManager-issuer' in self.manifests['common'] and 'certManager' in self.mapping:
            self._generate_issuer_template(common_dir)

    def _generate_configmap_template(self, output_dir: Path):
        """Generate inferenceservice-config ConfigMap template"""
        config = self.mapping['inferenceServiceConfig']['configMap']

        # ConfigMap controlled by inferenceServiceConfig.enabled
        template = f'''{{{{- if .Values.inferenceServiceConfig.enabled }}}}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {config['name']}
  namespace: {{{{ .Release.Namespace }}}}
  labels:
    {{{{- include "{self.mapping['metadata']['name']}.labels" . | nindent 4 }}}}
data:
'''

        # Add each data field from mapping
        for field in config['dataFields']:
            key = field['key']
            value_path = field['valuePath']
            # Convert valuePath to Helm template syntax
            helm_path = self._convert_value_path_to_helm(value_path)
            template += f'  {key}: {{{{- toJson .Values.{helm_path} | nindent 4 }}}}\n'

        template += '{{- end }}\n'

        output_file = output_dir / 'inferenceservice-config.yaml'
        with open(output_file, 'w') as f:
            f.write(template)

    def _generate_issuer_template(self, output_dir: Path):
        """Generate cert-manager Issuer template"""
        issuer_manifest = self.manifests['common']['certManager-issuer']
        issuer_config = self.mapping['certManager']['issuer']

        # Issuer controlled by certManager.enabled only
        template = f'''{{{{- if .Values.certManager.enabled }}}}
apiVersion: {issuer_manifest['apiVersion']}
kind: {issuer_manifest['kind']}
metadata:
  name: {issuer_manifest['metadata']['name']}
  namespace: {{{{ .Release.Namespace }}}}
  labels:
    {{{{- include "{self.mapping['metadata']['name']}.labels" . | nindent 4 }}}}
spec:
'''
        # Add spec fields
        template += self._yaml_to_string(issuer_manifest['spec'], indent=2)

        template += '{{- end }}\n'

        output_file = output_dir / 'cert-manager-issuer.yaml'
        with open(output_file, 'w') as f:
            f.write(template)

    def _generate_component_templates(self):
        """Generate templates for components (kserve, llmisvc, localmodel)"""
        for component_name, component_data in self.manifests.get('components', {}).items():
            component_dir = self.templates_dir / component_name
            component_dir.mkdir(exist_ok=True)

            # Generate deployment template (with values templating)
            if 'manifests' in component_data and 'controllerManager' in component_data['manifests']:
                self._generate_deployment_template(
                    component_dir,
                    component_name,
                    component_data
                )

            # Generate other resources from kustomize build (static with namespace replacement)
            if 'manifests' in component_data and 'resources' in component_data['manifests']:
                self._generate_kustomize_resources(
                    component_dir,
                    component_name,
                    component_data['manifests']['resources']
                )

    def _generate_deployment_template(self, output_dir: Path, component_name: str, component_data: Dict[str, Any]):
        """Generate Deployment template for a component"""
        config = component_data['config']
        manifest = component_data['manifests'].get('controllerManager')

        if not manifest:
            return

        # Get deployment from manifest (could be multi-doc)
        deployment = manifest if isinstance(manifest, dict) else manifest[0]

        cm_config = config.get('controllerManager', {})
        chart_name = self.mapping['metadata']['name']

        # Main component (kserve/llmisvc) is always installed, localmodel needs enabled check
        is_main_component = component_name in [chart_name, 'kserve', 'llmisvc']

        if is_main_component:
            template = f'''apiVersion: {deployment['apiVersion']}
kind: {deployment['kind']}
metadata:
  name: {deployment['metadata']['name']}
  namespace: {{{{ .Release.Namespace }}}}
  labels:
    {{{{- include "{self.mapping['metadata']['name']}.labels" . | nindent 4 }}}}
spec:
  selector:
    matchLabels:
'''
        else:
            enabled_path = config['enabled']['valuePath']
            template = f'''{{{{- if .Values.{enabled_path} }}}}
apiVersion: {deployment['apiVersion']}
kind: {deployment['kind']}
metadata:
  name: {deployment['metadata']['name']}
  namespace: {{{{ .Release.Namespace }}}}
  labels:
    {{{{- include "{self.mapping['metadata']['name']}.labels" . | nindent 4 }}}}
spec:
  selector:
    matchLabels:
'''

        # Add selector labels
        for key, value in deployment['spec']['selector']['matchLabels'].items():
            template += f'      {key}: {value}\n'

        template += '  template:\n'
        template += '    metadata:\n'
        template += '      labels:\n'

        # Add pod labels
        for key, value in deployment['spec']['template']['metadata']['labels'].items():
            template += f'        {key}: {value}\n'

        # Add annotations if present
        if 'annotations' in deployment['spec']['template']['metadata']:
            template += '      annotations:\n'
            for key, value in deployment['spec']['template']['metadata']['annotations'].items():
                template += f'        {key}: {value}\n'

        template += '    spec:\n'

        # Service account
        template += f'      serviceAccountName: {deployment["spec"]["template"]["spec"]["serviceAccountName"]}\n'

        # Security context
        if 'securityContext' in deployment['spec']['template']['spec']:
            template += '      securityContext:\n'
            template += self._yaml_to_string(deployment['spec']['template']['spec']['securityContext'], indent=8)

        # Containers
        template += '      containers:\n'
        for container in deployment['spec']['template']['spec']['containers']:
            template += f'      - name: {container["name"]}\n'

            # Image - make it configurable via values
            if 'image' in cm_config:
                img_repo_path = cm_config['image']['repository']['valuePath']
                img_tag_path = cm_config['image']['tag']['valuePath']
                template += f'        image: "{{{{ .Values.{img_repo_path} }}}}:{{{{ .Values.{img_tag_path} }}}}"\n'
            else:
                template += f'        image: {container["image"]}\n'

            # Image pull policy
            if 'imagePullPolicy' in container:
                if 'image' in cm_config and 'pullPolicy' in cm_config['image']:
                    policy_path = cm_config['image']['pullPolicy']['valuePath']
                    template += f'        imagePullPolicy: {{{{ .Values.{policy_path} }}}}\n'
                else:
                    template += f'        imagePullPolicy: {container["imagePullPolicy"]}\n'

            # Command
            if 'command' in container:
                template += '        command:\n'
                for cmd in container['command']:
                    template += f'        - {cmd}\n'

            # Args
            if 'args' in container:
                template += '        args:\n'
                for arg in container['args']:
                    template += f'        - {arg}\n'

            # Security context
            if 'securityContext' in container:
                template += '        securityContext:\n'
                template += self._yaml_to_string(container['securityContext'], indent=10)

            # Env
            if 'env' in container:
                template += '        env:\n'
                template += self._yaml_to_string(container['env'], indent=10)

            # Ports
            if 'ports' in container:
                template += '        ports:\n'
                template += self._yaml_to_string(container['ports'], indent=10)

            # Probes
            for probe_name in ['livenessProbe', 'readinessProbe']:
                if probe_name in container:
                    template += f'        {probe_name}:\n'
                    template += self._yaml_to_string(container[probe_name], indent=10)

            # Resources - make configurable
            if 'resources' in cm_config:
                resources_path = cm_config['resources']['valuePath']
                template += f'        resources: {{{{- toYaml .Values.{resources_path} | nindent 10 }}}}\n'
            elif 'resources' in container:
                template += '        resources:\n'
                template += self._yaml_to_string(container['resources'], indent=10)

            # Volume mounts
            if 'volumeMounts' in container:
                template += '        volumeMounts:\n'
                template += self._yaml_to_string(container['volumeMounts'], indent=10)

        # Termination grace period
        if 'terminationGracePeriodSeconds' in deployment['spec']['template']['spec']:
            template += f'      terminationGracePeriodSeconds: {deployment["spec"]["template"]["spec"]["terminationGracePeriodSeconds"]}\n'

        # Volumes
        if 'volumes' in deployment['spec']['template']['spec']:
            template += '      volumes:\n'
            template += self._yaml_to_string(deployment['spec']['template']['spec']['volumes'], indent=8)

        # Only add closing if for non-main components
        if not is_main_component:
            template += '{{- end }}\n'

        output_file = output_dir / 'deployment.yaml'
        with open(output_file, 'w') as f:
            f.write(template)

    def _generate_runtime_templates(self):
        """Generate templates for ClusterServingRuntimes"""
        if not self.manifests.get('runtimes'):
            return

        runtimes_dir = self.templates_dir / 'runtimes'
        runtimes_dir.mkdir(exist_ok=True)

        for runtime_data in self.manifests['runtimes']:
            self._generate_runtime_template(runtimes_dir, runtime_data)

    def _generate_runtime_template(self, output_dir: Path, runtime_data: Dict[str, Any]):
        """Generate a single ClusterServingRuntime template"""
        config = runtime_data['config']
        manifest = runtime_data['manifest']

        runtime_name = config['name']
        enabled_path = config['enabledPath']

        template = f'''{{{{- if .Values.runtimes.enabled }}}}
{{{{- if .Values.{enabled_path} }}}}
apiVersion: {manifest['apiVersion']}
kind: {manifest['kind']}
metadata:
  name: {manifest['metadata']['name']}
  labels:
    {{{{- include "{self.mapping['metadata']['name']}.labels" . | nindent 4 }}}}
spec:
'''

        # Add annotations if present
        if 'annotations' in manifest['spec']:
            template += '  annotations:\n'
            for key, value in manifest['spec']['annotations'].items():
                template += f'    {key}: \'{value}\'\n'

        # Add supported model formats
        if 'supportedModelFormats' in manifest['spec']:
            template += '  supportedModelFormats:\n'
            template += self._yaml_to_string(manifest['spec']['supportedModelFormats'], indent=4)

        # Add protocol versions
        if 'protocolVersions' in manifest['spec']:
            template += '  protocolVersions:\n'
            for version in manifest['spec']['protocolVersions']:
                template += f'  - {version}\n'

        # Containers with configurable image
        template += '  containers:\n'
        for container in manifest['spec']['containers']:
            template += f'  - name: {container["name"]}\n'

            # Image - make it configurable
            if 'image' in config:
                img_repo_path = config['image']['repositoryPath']
                img_tag_path = config['image']['tagPath']
                template += f'    image: "{{{{ .Values.{img_repo_path} }}}}:{{{{ .Values.{img_tag_path} }}}}"\n'
            else:
                template += f'    image: {container["image"]}\n'

            # Args (escape Go template expressions for KServe runtimes)
            if 'args' in container:
                template += '    args:\n'
                for arg in container['args']:
                    # Escape Go template expressions using {{ "{{" }} syntax
                    # This handles both {{.Foo}} and {{- if .Foo -}} style expressions
                    # and avoids conflicts with backticks used in Go templates
                    # Use placeholders to avoid double-replacement
                    escaped_arg = arg.replace('{{', '__HELM_OPEN__').replace('}}', '__HELM_CLOSE__')
                    escaped_arg = escaped_arg.replace('__HELM_OPEN__', '{{ "{{" }}').replace('__HELM_CLOSE__', '{{ "}}" }}')

                    # For multiline args, use YAML literal block scalar
                    if '\n' in arg:
                        # Use |- to strip final newline
                        template += '    - |-\n'
                        for line in escaped_arg.split('\n'):
                            if line:  # Skip empty lines at the end
                                template += f'      {line}\n'
                    else:
                        template += f'    - {escaped_arg}\n'

            # Security context
            if 'securityContext' in container:
                template += '    securityContext:\n'
                template += self._yaml_to_string(container['securityContext'], indent=6)

            # Resources - make configurable
            if 'resources' in config:
                resources_path = config['resources']['valuePath']
                template += f'    resources: {{{{- toYaml .Values.{resources_path} | nindent 6 }}}}\n'
            elif 'resources' in container:
                template += '    resources:\n'
                template += self._yaml_to_string(container['resources'], indent=6)

        template += '{{- end }}\n{{- end }}\n'

        # Sanitize filename
        filename = runtime_name.replace('kserve-', '') + '.yaml'
        output_file = output_dir / filename

        with open(output_file, 'w') as f:
            f.write(template)

    def _escape_go_templates_in_resource(self, obj: Any) -> Any:
        """
        Recursively escape Go template expressions in a resource object

        This is needed for resources that contain Go templates as part of their
        configuration (e.g., LLMInferenceServiceConfig args).

        Uses {{ "{{" }} syntax instead of backticks to avoid conflicts with
        backticks used within Go template expressions.
        """
        if isinstance(obj, str):
            # Escape Go template expressions using {{ "{{" }} syntax
            # Use placeholders to avoid double-replacement
            result = obj.replace('{{', '__HELM_OPEN__').replace('}}', '__HELM_CLOSE__')
            result = result.replace('__HELM_OPEN__', '{{ "{{" }}').replace('__HELM_CLOSE__', '{{ "}}" }}')
            # Use LiteralString for multiline strings to ensure YAML uses literal block scalar (|-)
            if '\n' in result:
                return LiteralString(result)
            return result
        elif isinstance(obj, dict):
            return {k: self._escape_go_templates_in_resource(v) for k, v in obj.items()}
        elif isinstance(obj, list):
            return [self._escape_go_templates_in_resource(item) for item in obj]
        else:
            return obj

    def _generate_kustomize_resources(self, output_dir: Path, component_name: str, resources: Dict[str, Any]):
        """
        Generate templates for resources from kustomize build

        Skip Deployment (we generate it separately with templating)
        For other resources, replace namespace with .Release.Namespace
        """
        chart_name = self.mapping['metadata']['name']
        is_main_component = component_name in [chart_name, 'kserve', 'llmisvc']

        for resource_key, resource in resources.items():
            kind = resource.get('kind')
            name = resource.get('metadata', {}).get('name', 'unnamed')

            # Skip Deployment - we generate it separately
            if kind == 'Deployment' and 'manager' in name:
                continue

            # Skip Namespace - Helm manages namespaces via --namespace flag and {{ .Release.Namespace }}
            # Users should create namespace with --create-namespace or beforehand
            if kind == 'Namespace':
                continue

            # Skip LLMInferenceServiceConfig - these contain Go templates that conflict with Helm
            # These should be managed separately or applied directly via kubectl
            if kind == 'LLMInferenceServiceConfig':
                print(f"  Skipping {kind}/{name} - contains Go templates incompatible with Helm")
                continue

            # Create sanitized filename
            filename = f"{kind.lower()}_{name}.yaml"

            # Escape Go template expressions for resources that need it
            resource = self._escape_go_templates_in_resource(resource)

            # Replace namespace with Helm template (after escaping to avoid double-escape)
            if 'metadata' in resource and 'namespace' in resource['metadata']:
                resource['metadata']['namespace'] = '{{ .Release.Namespace }}'

            # For webhook configurations, also replace namespace in webhooks
            if kind in ['MutatingWebhookConfiguration', 'ValidatingWebhookConfiguration']:
                if 'webhooks' in resource:
                    for webhook in resource['webhooks']:
                        if 'clientConfig' in webhook and 'service' in webhook['clientConfig']:
                            webhook['clientConfig']['service']['namespace'] = '{{ .Release.Namespace }}'

            # Main component resources are always installed, localmodel needs enabled check
            # Use CustomDumper to handle LiteralString properly (for multiline args)
            # Use very large width to prevent YAML from breaking Helm template expressions across lines
            if is_main_component:
                template = yaml.dump(resource, Dumper=CustomDumper, default_flow_style=False, sort_keys=False, width=float('inf'))
            else:
                enabled_path = f"{component_name}.enabled"
                template = f'{{{{- if .Values.{enabled_path} }}}}\n'
                template += yaml.dump(resource, Dumper=CustomDumper, default_flow_style=False, sort_keys=False, width=float('inf'))
                template += '{{- end }}\n'

            # Write template file
            output_file = output_dir / filename
            with open(output_file, 'w') as f:
                f.write(template)

    def _generate_crd_templates(self):
        """Generate CRD templates"""
        # CRDs are typically not templated in Helm
        # They can be included as-is in the crds/ directory
        # For now, we'll skip this as CRDs should be managed separately
        pass

    def _generate_helpers(self):
        """Generate _helpers.tpl file"""
        chart_name = self.mapping['metadata']['name']

        helpers = f'''{{{{/*
Expand the name of the chart.
*/}}}}
{{{{- define "{chart_name}.name" -}}}}
{{{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}}}
{{{{- end }}}}

{{{{/*
Create a default fully qualified app name.
*/}}}}
{{{{- define "{chart_name}.fullname" -}}}}
{{{{- if .Values.fullnameOverride }}}}
{{{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}}}
{{{{- else }}}}
{{{{- $name := default .Chart.Name .Values.nameOverride }}}}
{{{{- if contains $name .Release.Name }}}}
{{{{- .Release.Name | trunc 63 | trimSuffix "-" }}}}
{{{{- else }}}}
{{{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}}}
{{{{- end }}}}
{{{{- end }}}}
{{{{- end }}}}

{{{{/*
Create chart name and version as used by the chart label.
*/}}}}
{{{{- define "{chart_name}.chart" -}}}}
{{{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}}}
{{{{- end }}}}

{{{{/*
Common labels
*/}}}}
{{{{- define "{chart_name}.labels" -}}}}
helm.sh/chart: {{{{ include "{chart_name}.chart" . }}}}
{{{{ include "{chart_name}.selectorLabels" . }}}}
{{{{- if .Chart.AppVersion }}}}
app.kubernetes.io/version: {{{{ .Chart.AppVersion | quote }}}}
{{{{- end }}}}
app.kubernetes.io/managed-by: {{{{ .Release.Service }}}}
{{{{- end }}}}

{{{{/*
Selector labels
*/}}}}
{{{{- define "{chart_name}.selectorLabels" -}}}}
app.kubernetes.io/name: {{{{ include "{chart_name}.name" . }}}}
app.kubernetes.io/instance: {{{{ .Release.Name }}}}
{{{{- end }}}}
'''

        helpers_file = self.templates_dir / '_helpers.tpl'
        with open(helpers_file, 'w') as f:
            f.write(helpers)

    def _generate_notes(self):
        """Generate NOTES.txt file"""
        chart_name = self.mapping['metadata']['name']

        notes = f'''Thank you for installing {{{{ .Chart.Name }}}}.

Your release is named {{{{ .Release.Name }}}}.

To learn more about the release, try:

  $ helm status {{{{ .Release.Name }}}}
  $ helm get all {{{{ .Release.Name }}}}

'''

        # Add component status based on what's in the mapping
        if 'inferenceServiceConfig' in self.mapping or 'certManager' in self.mapping:
            notes += '''Component Status:
{{- if .Values.inferenceServiceConfig.enabled }}
  ✓ InferenceService Config: Enabled
{{- else }}
  ✗ InferenceService Config: Disabled
{{- end }}
{{- if .Values.certManager.enabled }}
  ✓ Cert-Manager Issuer: Enabled
{{- else }}
  ✗ Cert-Manager Issuer: Disabled
{{- end }}

'''

        # Add main component status (kserve, llmisvc)
        if chart_name in ['kserve', 'llmisvc']:
            notes += f'''  ✓ {chart_name.upper()} controller: Always Enabled

'''

        # Add localmodel status
        if 'localmodel' in self.mapping:
            notes += '''{{- if .Values.localmodel.enabled }}
  ✓ LocalModel controller: Enabled
{{- else }}
  ✗ LocalModel controller: Disabled
{{- end }}

'''

        # Add runtimes status
        if 'clusterServingRuntimes' in self.mapping or 'runtimes' in self.mapping:
            notes += '''{{- if .Values.runtimes.enabled }}
  ✓ ClusterServingRuntimes: Enabled
{{- else }}
  ✗ ClusterServingRuntimes: Disabled
{{- end }}

'''

        # Add llmisvc configs status
        if 'llmisvc' in self.mapping and chart_name != 'llmisvc':
            notes += '''{{- if .Values.llmisvcConfigs.enabled }}
  ✓ LLM Inference Service Configs: Enabled
{{- else }}
  ✗ LLM Inference Service Configs: Disabled
{{- end }}

'''

        notes_file = self.templates_dir / 'NOTES.txt'
        with open(notes_file, 'w') as f:
            f.write(notes)

    def _convert_value_path_to_helm(self, value_path: str) -> str:
        """Convert a dot-notation path to Helm template path"""
        # e.g., "common.config.explainers" -> "common.config.explainers"
        return value_path

    def _yaml_to_string(self, obj: Any, indent: int = 0) -> str:
        """Convert a Python object to indented YAML string"""
        yaml_str = yaml.dump(obj, default_flow_style=False, sort_keys=False)
        # Add indentation
        lines = yaml_str.split('\n')
        indented = '\n'.join(' ' * indent + line if line else '' for line in lines)
        return indented
