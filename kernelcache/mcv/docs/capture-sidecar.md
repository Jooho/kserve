# MCV Capture Sidecar Contract

The MCV capture sidecar runs next to a model workload during KernelCache
capture. It creates a baseline cache snapshot, waits for the workload to become
ready, packages the cache changes, and reports the result to the
`KernelCacheCapture` resource.

## Capture flow

1. Read the capture and readiness configuration.
2. Create a baseline cache snapshot before waiting for workload readiness.
3. Wait for reporter access and report `WaitingForWorkload`.
4. Poll the workload readiness URL.
5. Report `Capturing` and wait for registry access.
6. Run the MCV OCI delta create command.
7. Report `Succeeded`, `Unchanged`, or `Failed`.
8. Keep the sidecar idle after the capture finishes.

The baseline snapshot is created before readiness so files generated while the
workload starts are treated as capture output.

## Grouped configuration

The webhook provides grouped JSON configuration through three environment
variables. The grouped variables are the preferred interface for new Pods.

### `MCV_CAPTURE_CONFIG`

```json
{
  "version": 1,
  "cacheDir": "/workspace/cache",
  "targetImage": "quay.io/example/kernel-cache:capture-1",
  "capture": {
    "name": "kernel-cache-capture",
    "namespace": "default",
    "sessionID": "capture-session-id"
  },
  "cachePaths": [
    {
      "containerName": "kserve-container",
      "containerPath": "/root/.cache/vllm",
      "ociPath": "io.vllm.cache"
    }
  ]
}
```

`cacheDir`, `targetImage`, and the three capture identity fields are required.
`cachePaths` describes the paths reported with the capture result.

### `MCV_READINESS_CONFIG`

```json
{
  "url": "http://127.0.0.1:8080/health",
  "mcvCaptureReadinessTimeoutSeconds": 600
}
```

The sidecar sends HTTP requests to `url` until the workload responds or the
timeout expires. The default timeout is 600 seconds (10 minutes).

The cluster-wide default is configured in the `kernelcache` section of
`inferenceservice-config`:

```json
{
  "mcvCaptureReadinessTimeoutSeconds": 600
}
```

The value must be greater than zero.

### `MCV_RUNTIME_INFO`

```json
{
  "commandHash": "...",
  "argsHash": "...",
  "modelURIHash": "..."
}
```

Runtime information is copied into the capture result when present.

## Snapshot retry behavior

The sidecar retries the baseline snapshot when the MCV snapshot command exits
with an error. It makes up to three attempts with a five-second delay between
attempts. The retry count and delay are internal sidecar settings; they are not
currently configurable through the API.

If all attempts fail, the sidecar reports `CaptureFailed` and does not start
image creation.

## Result and status reporting

The sidecar writes the MCV result to `MCV_RESULT_PATH` and uses the result when
reporting the capture status. The default result path is:

```text
/tmp/mcv/result.json
```

The baseline snapshot path can be overridden with `MCV_SNAPSHOT_PATH`. Its
default is:

```text
/tmp/mcv/cache-snapshot.json
```

The sidecar reports the active capture session and source Pod with every status
update. A deleted or superseded capture causes the sidecar to stop retrying and
remain idle.

## Registry and reporter access

Reporter access and registry access are separate credentials:

- `MCV_REPORTER_ACCESS_FILE` is used for status updates to the Kubernetes API.
- `MCV_REGISTRY_ACCESS_FILE` or `MCV_REGISTRY_TOKEN_FILE` is used by MCV for
  registry pulls and pushes.
- `MCV_KUBERNETES_CA_FILE` controls certificate verification for Kubernetes API
  requests.
- `MCV_REGISTRY_CA_FILE` controls certificate verification for registry
  requests.

The legacy `MCV_REPORTER_CREDENTIAL_FILE`, `MCV_REGISTRY_CREDENTIAL_FILE`, and
individual capture environment variables remain available for compatibility.
New webhook-generated Pods use the grouped configuration and the dedicated
access file variables.

## Failure behavior

The sidecar reports `CaptureFailed` for invalid configuration, readiness
timeout, snapshot failure, registry access failure, or OCI image creation
failure. The failure message is bounded before it is sent to the resource
status. After reporting, the sidecar remains idle instead of restarting the
capture session.
