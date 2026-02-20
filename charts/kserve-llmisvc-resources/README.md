# kserve-llmisvc-resources

Helm chart for deploying KServe LLMInferenceService resources

![Version: v0.16.0](https://img.shields.io/badge/Version-v0.16.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.16.0](https://img.shields.io/badge/AppVersion-v0.16.0-informational?style=flat-square)

## Installing the Chart

To install the chart, run the following:

```console
$ helm install kserve-llmisvc oci://ghcr.io/kserve/charts/kserve-llmisvc-resources --version v0.16.0
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| commonAnnotations | object | `{}` | Common annotations to add to all resources |
| commonLabels | object | `{}` | Common labels to add to all resources |
| kserve.agent.image | string | `"kserve/agent"` |  |
| kserve.agent.tag | string | `""` |  |
| kserve.autoscaler.scaleDownStabilizationWindowSeconds | string | `"300"` |  |
| kserve.autoscaler.scaleUpStabilizationWindowSeconds | string | `"0"` |  |
| kserve.certManager.enabled | string | `""` |  |
| kserve.controller.deploymentMode | string | `"Knative"` | KServe deployment mode: "Standard", "Knative". |
| kserve.controller.gateway.additionalIngressDomains | list | `[]` | Optional additional domains for ingress routing. |
| kserve.controller.gateway.disableIngressCreation | bool | `false` | Whether to disable ingress creation for RawDeployment mode. |
| kserve.controller.gateway.disableIstioVirtualHost | bool | `false` | DisableIstioVirtualHost controls whether to use istio as network layer. |
| kserve.controller.gateway.domain | string | `"example.com"` | Ingress domain for RawDeployment mode, for Serverless it is configured in Knative. |
| kserve.controller.gateway.domainTemplate | string | `"{{ .Name }}-{{ .Namespace }}.{{ .IngressDomain }}"` | Ingress domain template for RawDeployment mode, for Serverless mode it is configured in Knative. |
| kserve.controller.gateway.ingressGateway.className | string | `"istio"` |  |
| kserve.controller.gateway.ingressGateway.enableGatewayApi | bool | `false` |  |
| kserve.controller.gateway.ingressGateway.gateway | string | `"knative-serving/knative-ingress-gateway"` |  |
| kserve.controller.gateway.ingressGateway.kserveGateway | string | `"kserve/kserve-ingress-gateway"` |  |
| kserve.controller.gateway.localGateway.gateway | string | `"knative-serving/knative-local-gateway"` |  |
| kserve.controller.gateway.localGateway.gatewayService | string | `"knative-local-gateway.istio-system.svc.cluster.local"` |  |
| kserve.controller.gateway.localGateway.knativeGatewayService | string | `""` |  |
| kserve.controller.gateway.pathTemplate | string | `""` | pathTemplate specifies the template for generating path based url for each inference service. |
| kserve.controller.gateway.urlScheme | string | `"http"` | HTTP endpoint url scheme. |
| kserve.createSharedResources | bool | `true` |  |
| kserve.inferenceServiceConfig.enabled | string | `""` |  |
| kserve.inferenceservice.resources.limits.cpu | string | `"1"` |  |
| kserve.inferenceservice.resources.limits.memory | string | `"2Gi"` |  |
| kserve.inferenceservice.resources.requests.cpu | string | `"1"` |  |
| kserve.inferenceservice.resources.requests.memory | string | `"2Gi"` |  |
| kserve.llmisvc.controller.affinity | object | `{}` | A Kubernetes Affinity, if required For more information, see [Affinity v1 core](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#affinity-v1-core)  For example:   affinity:     nodeAffinity:      requiredDuringSchedulingIgnoredDuringExecution:        nodeSelectorTerms:        - matchExpressions:          - key: foo.bar.com/role            operator: In            values:            - master |
| kserve.llmisvc.controller.annotations | object | `{}` | Optional additional annotations to add to the controller deployment |
| kserve.llmisvc.controller.containerSecurityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"privileged":false,"readOnlyRootFilesystem":true,"runAsNonRoot":true,"runAsUser":1000,"seccompProfile":{"type":"RuntimeDefault"}}` | Container Security Context to be set on the controller component container For more information, see [Configure a Security Context for a Pod or Container](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/) |
| kserve.llmisvc.controller.env | list | `[]` | Environment variables to be set on the controller container |
| kserve.llmisvc.controller.extraArgs | list | `[]` | Additional command line arguments |
| kserve.llmisvc.controller.extraVolumeMounts | list | `[]` | Additional volume mounts |
| kserve.llmisvc.controller.extraVolumes | list | `[]` | Additional volumes to be mounted |
| kserve.llmisvc.controller.image | string | `"kserve/llmisvc-controller"` | KServe LLM ISVC controller manager container image |
| kserve.llmisvc.controller.imagePullPolicy | string | `"IfNotPresent"` | Specifies when to pull controller image from registry |
| kserve.llmisvc.controller.imagePullSecrets | list | `[]` | Reference to one or more secrets to be used when pulling images For more information, see [Pull an Image from a Private Registry](https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/)  For example:  imagePullSecrets:    - name: "image-pull-secret" |
| kserve.llmisvc.controller.labels | object | `{}` | Optional additional labels to add to the controller deployment |
| kserve.llmisvc.controller.livenessProbe | object | `{"enabled":true,"failureThreshold":5,"httpGet":{"path":"/healthz","port":8081},"initialDelaySeconds":30,"periodSeconds":10,"timeoutSeconds":5}` | Liveness probe configuration |
| kserve.llmisvc.controller.metricsBindAddress | string | `"127.0.0.1"` | Metrics bind address |
| kserve.llmisvc.controller.metricsBindPort | string | `"8443"` | Metrics bind port |
| kserve.llmisvc.controller.nodeSelector | object | `{}` | The nodeSelector on Pods tells Kubernetes to schedule Pods on the nodes with matching labels For more information, see [Assigning Pods to Nodes](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/)  For example:   nodeSelector:     kubernetes.io/arch: amd64 |
| kserve.llmisvc.controller.podAnnotations | object | `{}` | Optional additional annotations to add to the controller Pods |
| kserve.llmisvc.controller.podLabels | object | `{}` | Optional additional labels to add to the controller Pods |
| kserve.llmisvc.controller.readinessProbe | object | `{"enabled":true,"failureThreshold":5,"httpGet":{"path":"/readyz","port":8081},"initialDelaySeconds":30,"periodSeconds":5,"timeoutSeconds":5}` | Readiness probe configuration |
| kserve.llmisvc.controller.replicas | int | `1` | Number of replicas for the controller deployment |
| kserve.llmisvc.controller.resources | object | `{"limits":{"cpu":"100m","memory":"300Mi"},"requests":{"cpu":"100m","memory":"300Mi"}}` | Resources to provide to the llmisvc controller pod  For example:  resources:    limits:      cpu: 100m      memory: 300Mi    requests:      cpu: 100m      memory: 300Mi  For more information, see [Resource Management for Pods and Containers](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/) |
| kserve.llmisvc.controller.securityContext | object | `{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod Security Context For more information, see [Configure a Security Context for a Pod or Container](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/) |
| kserve.llmisvc.controller.service | object | `{"port":8443,"targetPort":"metrics","type":"ClusterIP"}` | Service configuration |
| kserve.llmisvc.controller.service.port | int | `8443` | Service port for metrics |
| kserve.llmisvc.controller.service.targetPort | string | `"metrics"` | Service target port |
| kserve.llmisvc.controller.service.type | string | `"ClusterIP"` | Service type |
| kserve.llmisvc.controller.serviceAccount | object | `{"name":""}` | Service account configuration |
| kserve.llmisvc.controller.serviceAccount.name | string | `""` | Name of the service account to use If not set, a name is generated using the deployment name |
| kserve.llmisvc.controller.serviceAnnotations | object | `{}` | Optional additional annotations to add to the controller service |
| kserve.llmisvc.controller.strategy | object | `{"rollingUpdate":{"maxSurge":1,"maxUnavailable":0},"type":"RollingUpdate"}` | Deployment strategy |
| kserve.llmisvc.controller.tag | string | `""` | KServe LLM ISVC controller container image tag |
| kserve.llmisvc.controller.terminationGracePeriodSeconds | int | `10` | Termination grace period in seconds |
| kserve.llmisvc.controller.tolerations | list | `[]` | A list of Kubernetes Tolerations, if required For more information, see [Toleration v1 core](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#toleration-v1-core)  For example:   tolerations:   - key: foo.bar.com/role     operator: Equal     value: master     effect: NoSchedule |
| kserve.llmisvc.controller.topologySpreadConstraints | list | `[]` | A list of Kubernetes TopologySpreadConstraints, if required For more information, see [Topology spread constraint v1 core](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#topologyspreadconstraint-v1-core)  For example:   topologySpreadConstraints:   - maxSkew: 2     topologyKey: topology.kubernetes.io/zone     whenUnsatisfiable: ScheduleAnyway     labelSelector:       matchLabels:         app.kubernetes.io/instance: llmisvc-controller-manager         app.kubernetes.io/component: controller |
| kserve.localmodel.agent.reconcilationFrequencyInSecs | int | `60` |  |
| kserve.localmodel.disableVolumeManagement | bool | `false` |  |
| kserve.localmodel.enabled | bool | `false` |  |
| kserve.localmodel.jobNamespace | string | `"kserve-localmodel-jobs"` |  |
| kserve.localmodel.jobTTLSecondsAfterFinished | int | `3600` |  |
| kserve.localmodel.securityContext.fsGroup | int | `1000` |  |
| kserve.metricsaggregator.enableMetricAggregation | string | `"false"` | configures metric aggregation annotation. This adds the annotation serving.kserve.io/enable-metric-aggregation to every service with the specified boolean value. If true enables metric aggregation in queue-proxy by setting env vars in the queue proxy container to configure scraping ports. |
| kserve.metricsaggregator.enablePrometheusScraping | string | `"false"` | If true, prometheus annotations are added to the pod to scrape the metrics. If serving.kserve.io/enable-metric-aggregation is false, the prometheus port is set with the default prometheus scraping port 9090, otherwise the prometheus port annotation is set with the metric aggregation port. |
| kserve.opentelemetryCollector.metricReceiverEndpoint | string | `"keda-otel-scaler.keda.svc:4317"` |  |
| kserve.opentelemetryCollector.metricScalerEndpoint | string | `"keda-otel-scaler.keda.svc:4318"` |  |
| kserve.opentelemetryCollector.resource.cpuLimit | string | `"1"` |  |
| kserve.opentelemetryCollector.resource.cpuRequest | string | `"200m"` |  |
| kserve.opentelemetryCollector.resource.memoryLimit | string | `"2Gi"` |  |
| kserve.opentelemetryCollector.resource.memoryRequest | string | `"512Mi"` |  |
| kserve.opentelemetryCollector.scrapeInterval | string | `"5s"` |  |
| kserve.router.image | string | `"kserve/router"` |  |
| kserve.router.imagePullPolicy | string | `"IfNotPresent"` | Specifies when to pull router image from registry. |
| kserve.router.imagePullSecrets | list | `[]` | specifies the list of secrets to be used for pulling the router image from registry. |
| kserve.router.tag | string | `""` |  |
| kserve.security.autoMountServiceAccountToken | bool | `true` |  |
| kserve.service.serviceClusterIPNone | bool | `false` |  |
| kserve.servingruntime.art.defaultVersion | string | `"v0.16.0"` |  |
| kserve.servingruntime.art.image | string | `"kserve/art-explainer"` |  |
| kserve.storage.caBundleConfigMapName | string | `""` | Mounted CA bundle config map name for storage initializer. |
| kserve.storage.caBundleVolumeMountPath | string | `"/etc/ssl/custom-certs"` | Mounted path for CA bundle config map. |
| kserve.storage.containerSecurityContext.allowPrivilegeEscalation | bool | `false` |  |
| kserve.storage.containerSecurityContext.capabilities.drop[0] | string | `"ALL"` |  |
| kserve.storage.containerSecurityContext.privileged | bool | `false` |  |
| kserve.storage.containerSecurityContext.runAsNonRoot | bool | `true` |  |
| kserve.storage.cpuModelcar | string | `"10m"` | Model sidecar cpu requirement. |
| kserve.storage.enableModelcar | bool | `true` | Flag for enabling model sidecar feature. |
| kserve.storage.image | string | `"kserve/storage-initializer"` |  |
| kserve.storage.memoryModelcar | string | `"15Mi"` | Model sidecar memory requirement. |
| kserve.storage.resources.limits.cpu | string | `"1"` |  |
| kserve.storage.resources.limits.memory | string | `"1Gi"` |  |
| kserve.storage.resources.requests.cpu | string | `"100m"` |  |
| kserve.storage.resources.requests.memory | string | `"100Mi"` |  |
| kserve.storage.s3 | object | `{"CABundle":"","CABundleConfigMap":"","accessKeyIdName":"AWS_ACCESS_KEY_ID","endpoint":"","region":"","secretAccessKeyName":"AWS_SECRET_ACCESS_KEY","useAnonymousCredential":"","useHttps":"","useVirtualBucket":"","verifySSL":""}` | Configurations for S3 storage |
| kserve.storage.s3.CABundle | string | `""` | When used with a configured CA bundle config map, specifies the full path (mount path + file name) for the mounted config map data. When used absent of a configured CA bundle config map, specifies the path to a certificate bundle to use for HTTPS certificate validation. |
| kserve.storage.s3.CABundleConfigMap | string | `""` | Mounted CA bundle config map name. |
| kserve.storage.s3.accessKeyIdName | string | `"AWS_ACCESS_KEY_ID"` | AWS S3 static access key id. |
| kserve.storage.s3.endpoint | string | `""` | AWS S3 endpoint. |
| kserve.storage.s3.region | string | `""` | Default region name of AWS S3. |
| kserve.storage.s3.secretAccessKeyName | string | `"AWS_SECRET_ACCESS_KEY"` | AWS S3 static secret access key. |
| kserve.storage.s3.useAnonymousCredential | string | `""` | Whether to use anonymous credentials to download the model or not, default to false. |
| kserve.storage.s3.useHttps | string | `""` | Whether to use secured https or http to download models, allowed values are 0 and 1 and default to 1. |
| kserve.storage.s3.useVirtualBucket | string | `""` | Whether to use virtual bucket or not, default to false. |
| kserve.storage.s3.verifySSL | string | `""` | Whether to verify the tls/ssl certificate, default to true. |
| kserve.storage.storageSecretNameAnnotation | string | `"serving.kserve.io/secretName"` | Storage secret name reference for storage initializer. |
| kserve.storage.storageSpecSecretName | string | `"storage-config"` | Storage spec secret name. |
| kserve.storage.tag | string | `""` |  |
| kserve.storage.uidModelcar | int | `1010` | Model sidecar UID. |
| kserve.storagecontainer.enabled | string | `""` |  |
| kserve.version | string | `"v0.16.0"` | Version of KServe LLM ISVC components |
