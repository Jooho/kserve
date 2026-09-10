# Copyright 2026 The KServe Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Manual E2E scenarios for the KernelCache workflow.

The test bodies are intentionally placeholders. Each scenario will be filled
with real Kubernetes operations after the KernelCache workflow is finalized.
The intended test entry point is an actual InferenceService, not a synthetic
Pod created only to exercise admission.
"""

import pytest


@pytest.mark.kernelcache
def test_kernelcache_capture_pushes_with_temporary_credentials():
    """An authenticated ISVC capture pushes its cache and cleans up credentials."""
    # TODO: Configure registry.auth.type=serviceAccountToken with the test
    # registry endpoint and push RoleRef, then create an ISVC using the default
    # ServiceAccount with capture enabled and no pre-created KCC.
    # Verify the workload Pod starts before the capture Secret exists, the
    # controller creates the capture-scoped pusher ServiceAccount and
    # RoleBinding, and the Secret-bound TokenRequest uses that Secret UID.
    # Verify only MCV receives the projected access.json, waits for the
    # credential, and pushes the real runtime cache to the OCI registry.
    # Wait for KCC completion and verify the capture Secret is deleted and its
    # bound token is rejected. Concurrent Pods must use separate credentials.
    pass


@pytest.mark.kernelcache
def test_kernelcache_registry_authentication_boundaries():
    """Publishing authentication stays isolated from runtime and pull Jobs."""
    # TODO: Verify auth.type=none creates no publishing identities or Secrets.
    # For serviceAccountToken authentication, verify TokenRequest is limited
    # to the configured pusher ServiceAccount, expired credentials and wrong
    # registry targets fail closed, and capture opt-out does not modify runtime
    # behavior. Verify preparation Jobs pull using their own identity, without
    # receiving publishing credentials.
    # Verify cross-Pod and cross-namespace requests cannot obtain or revoke
    # another capture session's credential.
    pass


@pytest.mark.kernelcache
def test_kernelcache_default_sidecar_and_registry_projection():
    """ISVC defaults and annotations control MCV injection and registry access."""
    # TODO: Create real ISVC and LLMISVC workloads without a KCC or trigger annotation.
    # Verify configured MCV image, shared writable cache directories, token and CA.
    # Verify global false and workload true/false overrides, feature disabled,
    # existing KC consumers, KCC path overrides, and webhook reinvocation.
    # Verify the workload ServiceAccount and GPU requests are preserved.
    # Test token projection and user Secret sources in both workload and Job
    # namespaces, missing resource/key failures, and anonymous access with CA.
    # Run MCV manually in the injected sidecar, then verify a preparation Job
    # extracts its image with the same registry settings.
    pass


@pytest.mark.kernelcache
def test_kernelcache_capture_oci_backend_under_restricted_scc():
    """MCV packages and pushes real runtime caches without elevated privileges."""
    # TODO: First validate with the MCV test Deployment; repeat with an actual
    # ISVC once capture injection is implemented. Use an arbitrary UID,
    # RuntimeDefault seccomp, drop ALL capabilities, and no privilege escalation.
    # Capture real cache files from the shared emptyDir using --builder oci.
    # Verify OCI creation/push succeeds without Buildah environment variables,
    # mount/user namespaces, a runtime socket, or access to the node filesystem.
    # Pull by the reported digest; verify manifest, labels, cache contents and
    # file ownership, then consume the OCI image on an ISVC.
    pass


@pytest.mark.kernelcache
def test_kernelcache_capture_oci_credentials_and_input_isolation():
    """Capture keeps credentials outside the artifact and rejects unsafe input."""
    # TODO: Exercise anonymous and authenticated registries, missing/invalid
    # credentials, an untrusted TLS certificate, and registry unavailability.
    # Mount a user-provided Docker config read-only; never include it in cache.
    # Verify symlinks, special files and whiteout names fail without publishing.
    # Exercise concurrent captures, writes during capture, cancellation and
    # ephemeral-storage exhaustion; verify isolated temporary directories,
    # cleanup, actionable errors and no success digest after a failed push.
    pass


@pytest.mark.kernelcache
def test_kernelcache_nodegroup_creates_node_resources():
    """A KernelCacheNodeGroup selects nodes for OCI prefetch preparation."""
    # TODO: Create a multi-node KernelCacheNodeGroup.
    # Verify one KernelCacheNode and one OCI prefetch Job are prepared for
    # every selected node.
    pass


@pytest.mark.kernelcache
def test_kernelcache_status_reports_successful_node_preparation():
    """Successful preparation produces ready node and aggregate status."""
    # TODO: Create a KernelCache and wait for all preparation Jobs to complete.
    # Verify KernelCacheNode status contains the cache reference, state, message,
    # last update, and footprints, and KernelCache counts are deterministic.
    pass


@pytest.mark.kernelcache
def test_kernelcache_status_reports_preparation_failure():
    """A failed preparation reports Error with an actionable message."""
    # TODO: Use an unavailable or invalid OCI image and wait for the preparation
    # Pod to fail. Verify KernelCacheNode and KernelCache status expose Error,
    # the failed node count, and the relevant Pod or Job failure message.
    pass


@pytest.mark.kernelcache
def test_kernelcache_reconciles_preparation_resources_idempotently():
    """Repeated reconciliation does not create duplicate nodes or Jobs."""
    # TODO: Reconcile the same KernelCache repeatedly and after controller or
    # agent restart. Verify resource ownership, stable names, and no duplicate
    # KernelCacheNode or OCI prefetch Job resources.
    pass


@pytest.mark.kernelcache
def test_kernelcache_oci_mount_is_injected_into_an_inferenceservice_pod():
    """An ISVC with a linked Ready cache receives the OCI ImageVolume."""
    # TODO: Capture from an actual ISVC and wait for its KC to become Ready.
    # Recreate its Pod and verify the read-only OCI source, writable emptyDir,
    # and linker-created file links for every configured cache path.
    pass


@pytest.mark.kernelcache
def test_kernelcache_sidecar_override_preserves_cache_mount():
    """Disabling capture does not disable mounting an available cache."""
    # TODO: Create an actual ISVC with a Ready KC and disable sidecar injection
    # using the annotation. Verify the linker still prepares readable cache
    # files, MCV is absent, and the runtime ServiceAccount remains unchanged.
    pass


@pytest.mark.kernelcache
def test_kernelcache_recapture_retains_previous_artifact():
    """A new capture creates a new KC without overwriting the previous KC."""
    # TODO: Capture from an actual ISVC, then change runtime args and recreate
    # its Pod. Verify linker and MCV coexist, linked cache files are readable
    # by MCV, and the full cache is pushed under a new digest. Check that the
    # same KCC references the new KC and the previous KC remains unchanged.
    # Repeat reconciliation and verify no duplicate KC for the same artifact.
    pass


@pytest.mark.kernelcache
def test_kernelcache_replacement_rejects_stale_capture_reports():
    """Only the selected Pod session can publish a capture result."""
    # TODO: Replace an ISVC Pod while its capture is running. Verify the new
    # Pod session becomes active, a late old-session status patch is rejected,
    # and the new artifact and KC reference survive subsequent reconciliation.
    pass


@pytest.mark.kernelcache
def test_kernelcache_oci_image_volume_is_usable_by_the_runtime_pod():
    """The runtime Pod can start with the injected OCI ImageVolume."""
    # TODO: Run an actual ISVC on a cluster with ImageVolume support. Verify the
    # Pod is scheduled, the OCI volume is mounted read-only at the configured
    # container path, and the runtime can read the expected cache subPath.
    pass


@pytest.mark.kernelcache
def test_kernelcache_capture_publishes_only_new_cache_directories():
    """Capture snapshots the mounted cache and publishes only runtime deltas."""
    # TODO: Start an actual ISVC with a Ready KC and verify the MCV sidecar
    # snapshots the cache before waiting for workload readiness. Make the
    # runtime create a new cache directory and verify the published OCI contains
    # that subtree and its parent directories, but not mounted baseline files.
    # Restart with a full cache hit and verify no image is pushed, the KCC
    # reports Unchanged, and its existing KC reference remains available.
    pass


@pytest.mark.kernelcache
def test_kernelcache_artifact_security_none_allows_preparation():
    """None mode records skipped security checks and preserves the full flow."""
    # TODO: Capture an OCI artifact from an actual ISVC with artifactSecurity
    # mode none. Verify KCC signing and KC verification report Skipped, the KC
    # is created, preparation Jobs run, and every selected node becomes Ready.
    pass


@pytest.mark.kernelcache
def test_kernelcache_cert_verification_blocks_untrusted_artifact():
    """Cert mode never prepares an artifact that is not trusted."""
    # TODO: Configure a CA bundle and signer identity, then create KCs for an
    # unsigned image, an image signed by another CA, and an image with a wrong
    # signer identity. Verify each KC reports failed verification and creates
    # no preparation or OCI prefetch Job.
    pass


@pytest.mark.kernelcache
def test_kernelcache_cert_signing_and_rotation():
    """Cert signing and trust-bundle overlap support safe key rotation."""
    # TODO: Capture and sign an image with the old key, add the new CA to the
    # trust bundle, and verify both old and new artifacts. Remove the old CA and
    # trigger re-verification; old-only signatures must fail before preparation.
    pass
