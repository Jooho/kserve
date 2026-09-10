# V1alpha1KernelCachePVCConfig

KernelCachePVCConfig contains PVC-only storage configuration.
## Properties
Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**access_modes** | **list[str]** | AccessModes configures the PVC access modes. | [optional] 
**storage_class_name** | **str** | StorageClassName selects dynamic provisioning. | [optional] 
**storage_size** | [**ResourceQuantity**](ResourceQuantity.md) |  | [optional] 
**volume_name** | **str** | VolumeName binds to an existing PV. | [optional] 

[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


