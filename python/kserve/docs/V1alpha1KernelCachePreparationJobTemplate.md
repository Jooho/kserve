# V1alpha1KernelCachePreparationJobTemplate

KernelCachePreparationJobTemplate customizes node preparation Jobs. It applies to both OCI and PVC preparation.
## Properties
Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**node_selector** | **dict(str, str)** | NodeSelector selects nodes for the preparation Job. | [optional] 
**priority_class_name** | **str** | PriorityClassName sets the priority class for the preparation Job. | [optional] 
**service_account_name** | **str** | ServiceAccountName selects the service account for registry access. | [optional] 
**tolerations** | [**list[V1Toleration]**](https://github.com/kubernetes-client/python/blob/master/kubernetes/docs/V1Toleration.md) | Tolerations configures tolerations for the preparation Job. | [optional] 

[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


