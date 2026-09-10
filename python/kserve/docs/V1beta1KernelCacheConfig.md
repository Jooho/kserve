# V1beta1KernelCacheConfig

## Properties
Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**abandoned_capture_policy** | **str** | AbandonedCapturePolicy controls generated captures whose producer Pod disappears before completion. | [optional] 
**artifact_security** | [**V1beta1KernelCacheArtifactSecurityConfig**](V1beta1KernelCacheArtifactSecurityConfig.md) |  | [optional] 
**default_mount_type** | **str** |  | [optional] 
**default_node_group** | **str** | DefaultNodeGroup is used when an automatically created KernelCache has no workload override. | [optional] 
**default_sidecar_injection** | **bool** |  | [default to False]
**enabled** | **bool** |  | [default to False]
**job_namespace** | **str** |  | [default to '']
**job_ttl_seconds_after_finished** | **int** | JobTTLSecondsAfterFinished controls how long completed preparation Jobs are retained. | [optional] 
**mcv_capture_readiness_timeout_seconds** | **int** | MCVCaptureReadinessTimeoutSeconds limits how long the MCV capture sidecar waits for workload readiness. | [optional] 
**mcv_image** | **str** |  | [optional] 
**prefetch_image** | **str** |  | [optional] 
**reconcile_interval_seconds** | **int** | ReconcileIntervalSeconds controls KCN status reconciliation. Periodic Node image validation uses the node agent&#39;s internal interval. | [optional] 
**registry** | [**V1beta1KernelCacheRegistryConfig**](V1beta1KernelCacheRegistryConfig.md) |  | [optional] 

[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


