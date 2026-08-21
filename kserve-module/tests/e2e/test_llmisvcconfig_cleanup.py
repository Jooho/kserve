"""E2E test for well-known LLMInferenceServiceConfig cleanup on Kserve CR delete.

Covers the cross-component behavior that integration tests cannot:
  - the real llmisvc webhook rejecting a well-known config delete while the guard
    (PREVENT_WELL_KNOWN_CONFIG_DELETION) is on, then allowing it once the module
    flips the guard on the real controller deployment and its rollout completes;
  - status.referencedBy populated by the real llmisvc config controller, which
    holds the delete back with a Degraded/DeletionBlocked condition until the
    referencing service is removed.
"""

import json

import pytest

from conftest import (
    run,
    get_cr,
    cr_exists,
    resource_exists,
    get_jsonpath,
    list_resource_names,
    wait_for,
    wait_consistently,
    force_delete_kserve_cr,
    KSERVE_CR_NAME,
    MODULE_FINALIZER,
    NAMESPACE,
    TIMEOUT_60S,
    TIMEOUT_120S,
)

# Cleanup flips the guard, waits for the llmisvc rollout, then deletes every
# well-known config, so a blocked deletion can run well past a minute.
CLEANUP_TIMEOUT = 180.0

WELL_KNOWN_ANNOTATION = "serving.kserve.io/well-known-config"
CONFIG_KIND = "llminferenceserviceconfig"
REF_ISVC_NAME = "e2e-cleanup-ref"


def _require_well_known_config(kubectl):
    """Return the name of a well-known config, waiting for the operands to deploy."""

    def _pick():
        names = list_resource_names(
            kubectl,
            CONFIG_KIND,
            namespace=NAMESPACE,
            annotation=(WELL_KNOWN_ANNOTATION, "true"),
        )
        assert names, "expected shipped well-known LLMInferenceServiceConfigs"
        return names[0]

    return wait_for(_pick, timeout=TIMEOUT_120S)


def _minimal_llmisvc_yaml(name, namespace):
    """Smallest LLMInferenceService that passes admission (model.uri is the only
    required field). It does not need to serve -- merely existing makes the module
    treat every well-known config as referenced."""
    return json.dumps(
        {
            "apiVersion": "serving.kserve.io/v1alpha2",
            "kind": "LLMInferenceService",
            "metadata": {"name": name, "namespace": namespace},
            "spec": {"model": {"uri": "hf://kserve-e2e/does-not-serve"}},
        }
    )


def _wait_cr_deleted(kubectl, timeout=CLEANUP_TIMEOUT):
    """Poll until the Kserve CR is fully gone (short per-poll get, long budget)."""

    def _gone():
        assert not cr_exists(kubectl), "Kserve CR still terminating"

    wait_for(_gone, timeout=timeout, interval=5.0)


def _force_unwedge(kubectl, isvc_name):
    """Safety teardown: drop the referencing service and force-delete the CR so a
    failed spec does not wedge the singleton Kserve CR for the rest of the suite."""
    run(
        [
            kubectl, "delete", "llmisvc", isvc_name,
            "-n", NAMESPACE, "--ignore-not-found", "--wait=false",
        ],
        check=False,
    )
    force_delete_kserve_cr(kubectl)


@pytest.mark.sanity
class TestLLMISVCConfigCleanupOnDelete:
    """Well-known config cleanup during Kserve CR deletion."""

    def test_guard_flip_and_reference_block(self, kubectl, apply_kserve_cr):
        """Deletion flips the guard to remove configs, but holds while referenced.

        1. While the guard is on, a manual config delete is rejected by the webhook.
        2. A referencing LLMInferenceService makes the config referenced, so
           deleting the Kserve CR blocks with Degraded/DeletionBlocked and the
           config is preserved.
        3. Removing the service clears the reference, and the module then flips the
           guard, deletes the config, and releases the finalizer.
        """
        target = _require_well_known_config(kubectl)
        assert not list_resource_names(kubectl, "llmisvc"), (
            "test requires no LLMInferenceService present; a stray service keeps "
            "well-known configs referenced and would never unblock"
        )

        # 1. Guard is on by default: a manual delete must be rejected by the webhook.
        res = run(
            [kubectl, "delete", CONFIG_KIND, target, "-n", NAMESPACE], check=False
        )
        assert res.returncode != 0, "manual delete of a well-known config should be rejected"
        assert "cannot be deleted" in (res.stderr + res.stdout), (
            f"unexpected delete output: {res.stderr}{res.stdout}"
        )

        try:
            # 2. A referencing service makes every well-known config referenced.
            run(
                [kubectl, "apply", "-f", "-"],
                input_text=_minimal_llmisvc_yaml(REF_ISVC_NAME, NAMESPACE),
            )

            def _referenced():
                names = get_jsonpath(
                    kubectl, CONFIG_KIND, target,
                    "{.status.referencedBy[*].name}", namespace=NAMESPACE,
                )
                assert REF_ISVC_NAME in names, f"referencedBy not populated: {names!r}"

            wait_for(_referenced, timeout=TIMEOUT_60S)

            run([kubectl, "delete", "kserve", KSERVE_CR_NAME, "--wait=false"])

            def _blocked():
                cr = get_cr(kubectl, check=False)
                assert cr is not None, "CR should still exist while blocked"
                assert cr.get("metadata", {}).get(
                    "deletionTimestamp"
                ), "CR should be Terminating"
                assert MODULE_FINALIZER in cr.get("metadata", {}).get(
                    "finalizers", []
                ), "module finalizer should be held"
                conds = {
                    c["type"]: c for c in cr.get("status", {}).get("conditions", [])
                }
                deg = conds.get("Degraded", {})
                assert (
                    deg.get("status") == "True"
                    and deg.get("reason") == "DeletionBlocked"
                ), f"expected Degraded/DeletionBlocked, got {deg}"
                assert REF_ISVC_NAME in deg.get(
                    "message", ""
                ), f"Degraded message should name the service: {deg.get('message')!r}"

            wait_for(_blocked, timeout=TIMEOUT_60S)

            # The config is preserved for as long as it is referenced.
            def _config_still_present():
                assert resource_exists(
                    kubectl, CONFIG_KIND, target, namespace=NAMESPACE
                ), f"{target} deleted while still referenced"

            wait_consistently(_config_still_present, duration=10.0)

            # 3. Dereference: the module flips the guard, deletes the config, and
            #    releases the finalizer so the CR completes deletion.
            run([kubectl, "delete", "llmisvc", REF_ISVC_NAME, "-n", NAMESPACE])
            _wait_cr_deleted(kubectl)

            assert not resource_exists(
                kubectl, CONFIG_KIND, target, namespace=NAMESPACE
            ), f"{target} should be deleted once dereferenced"
        finally:
            _force_unwedge(kubectl, REF_ISVC_NAME)
