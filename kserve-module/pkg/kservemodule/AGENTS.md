# Status conditions (kserve-module)

Non-obvious semantics of the Kserve CR status conditions. Read before touching
`status.go` or anything that sets conditions.

## Condition layout

`.status.conditions` is a flat list. Each type is a sibling entry with its own
`status`, `severity`, `reason`, `message`:

- `Ready` (happy/aggregate), `ProvisioningSucceeded`, `Degraded` -- contract-mandated.
- `KServeReady`, `ModelControllerReady`, `WVAReady`, `ModelCacheReady`,
  `DependenciesAvailable` -- module-defined dependents of `Ready`.
- `KserveLLMInferenceServiceDependencies`, `KserveLLMInferenceServiceWideEPDependencies`,
  `LLM-D-WVADependencies` -- informational per-group dependency conditions.

`Degraded` is a single, standalone condition. It is not nested in the group
conditions; they are all flat siblings.

## How `Ready` is computed

`Ready` is the `conditions.Manager` "happy" condition, derived only from its
registered dependents (see `newConditionManager`). Rollup rule:

- `Ready=False` iff some **dependent** is `False`/`Unknown` with **Error**
  severity. `Info`-severity False/Unknown does not gate `Ready`.

**`Degraded` does NOT affect `Ready`.** Two reasons:
1. It is not registered as a dependent, so the happiness rollup never inspects it.
2. Even if it were, the rollup flags `False`/`Unknown` conditions; `Degraded` is
   `True` when bad (inverted polarity), so it would never match.

To make `Ready` reflect a problem, mark a registered dependent `False(Error)`
(this is what `DependenciesAvailable` does on a critical dependency; `Degraded`
is set in the same pass but is not what flips `Ready`).

## DSC aggregation (`ModulesReady`)

The DSC/odh-operator rolls per-module status into `ModulesReady`:

- **Enabled** module: DSC reads both `Ready` and `Degraded`. `Ready=True` +
  `Degraded=True` -> per-module `KserveReady` stays `True` (mirrors `Ready`) but
  the aggregate `ModulesReady=False` (reason `Degraded`). So `Ready=True` +
  `Degraded=True` is a valid, documented state meaning "functional but impaired".
- **Removed/deleting** module: DSC does NOT read the module CR's conditions -- the
  cleanup path and status-aggregation path are separate, and disabled modules are
  dropped from the enabled set. A condition set while the CR is terminating is not
  surfaced to `ModulesReady` on its own; that requires explicit DSC-side handling.

## Deletion-blocked reporting

When Kserve CR deletion is blocked (well-known `LLMInferenceServiceConfig`s still
referenced, or the llmisvc controller unavailable), report it as `Degraded=True`
(reason `DeletionBlocked`, severity Error) and keep `Ready=True`. Rationale: while blocked, the finalizer holds the CR
so operands are not garbage-collected and kserve stays fully functional (serving).
Forcing `Ready=False` would mislabel a working module as broken. `Phase` stays
`Ready` for the same reason; the block is visible via `Degraded` plus the
deletion timestamp.

Because the DSC ignores a terminating module's conditions (see above), surfacing
this block on the DSC needs a separate odh-operator change; PR-1 only sets the
module-CR condition.
