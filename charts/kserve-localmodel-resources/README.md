# kserve-localmodel-resources

![Version: v0.16.0](https://img.shields.io/badge/Version-v0.16.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.16.0](https://img.shields.io/badge/AppVersion-v0.16.0-informational?style=flat-square)

KServe LocalModel - Local Model Storage and Caching for Edge and On-Premise Deployments

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| kserve.version | string | `"v0.16.0"` |  |
| localmodel.controller.image | string | `"kserve/kserve-localmodel-controller"` |  |
| localmodel.controller.imagePullPolicy | string | `"Always"` |  |
| localmodel.controller.resources.limits.cpu | string | `"100m"` |  |
| localmodel.controller.resources.limits.memory | string | `"300Mi"` |  |
| localmodel.controller.resources.requests.cpu | string | `"100m"` |  |
| localmodel.controller.resources.requests.memory | string | `"200Mi"` |  |
| localmodel.controller.tag | string | `"v0.16.0"` |  |
| localmodel.nodeAgent.affinity | object | `{}` |  |
| localmodel.nodeAgent.image | string | `"kserve/kserve-localmodelnode-agent"` |  |
| localmodel.nodeAgent.imagePullPolicy | string | `"Always"` |  |
| localmodel.nodeAgent.nodeSelector.kserve/localmodel | string | `"worker"` |  |
| localmodel.nodeAgent.resources.limits.cpu | string | `"100m"` |  |
| localmodel.nodeAgent.resources.limits.memory | string | `"300Mi"` |  |
| localmodel.nodeAgent.resources.requests.cpu | string | `"100m"` |  |
| localmodel.nodeAgent.resources.requests.memory | string | `"200Mi"` |  |
| localmodel.nodeAgent.tag | string | `"v0.16.0"` |  |
| localmodel.nodeAgent.tolerations | list | `[]` |  |

