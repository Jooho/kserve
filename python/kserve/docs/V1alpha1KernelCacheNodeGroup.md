# V1alpha1KernelCacheNodeGroup

KernelCacheNodeGroup selects the set of Kubernetes nodes that host prepared KernelCache artifacts and describes how those artifacts are persisted on each selected node. Node selection is mandatory; storage is optional so that OCI-backed KernelCache deployments (which store artifacts in the container image layer instead of a PersistentVolume) can share the same grouping primitive.
## Properties
Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**api_version** | **str** | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources | [optional] 
**kind** | **str** | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds | [optional] 
**metadata** | [**V1ObjectMeta**](https://github.com/kubernetes-client/python/blob/master/kubernetes/docs/V1ObjectMeta.md) |  | [optional] 
**spec** | [**V1alpha1KernelCacheNodeGroupSpec**](V1alpha1KernelCacheNodeGroupSpec.md) |  | [optional] 

[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


