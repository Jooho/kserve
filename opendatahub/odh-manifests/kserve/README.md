# KServe Manifests

KServe comes with 1 component:

1. [kserve](#kserve)

## kserve

- [kserve](https://github.com/opendatahub-io/kserve)
  - Forked upstream kserve/kserve repository

## KServe Architecture

A complete architecture can be found at https://kserve.github.io/website/0.10/modelserving/control_plane

KServe Control Plane: Responsible for reconciling the InferenceService custom resources. It creates the Knative serverless deployment for predictor, transformer, explainer to enable autoscaling based on incoming request workload including scaling down to zero when no traffic is received. When raw deployment mode is enabled, control plane creates Kubernetes deployment, service, ingress, HPA.

### Parameters

You can set images though `parameters`.

- kserve-controller
- kserve-explainer-version
- kserve-alibi-explainer
- kserve-art-explainer
- kserve-agent
- kserve-router
- kserve-storage-initializer

##### Examples

Example ServingRuntime and Predictors can be found at: https://github.com/kserve/kserve/blob/master/docs/OPENSHIFT_GUIDE.md

### Overlays

None

### Installation process

Following are the steps to install Model Mesh as a part of OpenDataHub install:

1. Install the OpenDataHub operator
2. Create a KfDef that includes the kserve component.

```
apiVersion: kfdef.apps.kubeflow.org/v1
kind: KfDef
metadata:
  name: opendatahub
  namespace: opendatahub
spec:
  applications:
    - kustomizeConfig:
        repoRef:
          name: manifests
          path: odh-common
      name: odh-common
    - kustomizeConfig:
        repoRef:
          name: manifests
          path: kserve
      name: kserve
  repos:
    - name: manifests
      uri: https://api.github.com/repos/opendatahub-io/odh-manifests/tarball/master
  version: master

```

3. You can now create a new project and create an InferenceService CR.
