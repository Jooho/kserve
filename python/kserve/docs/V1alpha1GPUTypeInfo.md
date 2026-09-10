# V1alpha1GPUTypeInfo

GPUTypeInfo contains observed GPU information for a node.
## Properties
Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**cuda_version** | **str** | CUDAVersion is the observed CUDA version for NVIDIA GPUs. | [optional] 
**driver_version** | **str** | DriverVersion is the observed driver version. | [optional] 
**gpu_type** | **str** | GPUType identifies the GPU type. | [default to '']
**ids** | **list[int]** | Ids contains device IDs for this GPU type. | [optional] 
**rocm_version** | **str** | RocmVersion is the observed ROCm version for AMD GPUs. | [optional] 

[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


