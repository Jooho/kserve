package kservemodule

import appsv1 "k8s.io/api/apps/v1"

// LLMISVCConfigGVK exposes the well-known config GVK to the external
// kservemodule_test package for integration tests.
var LLMISVCConfigGVK = llmISVCConfigGVK

// DeploymentRolloutCompleteForTest exposes deploymentRolloutComplete for the
// external kservemodule_test package, so integration tests wait for exactly the
// rollout condition the cleanup path checks (no duplicated criteria).
func DeploymentRolloutCompleteForTest(dep *appsv1.Deployment) bool {
	return deploymentRolloutComplete(dep)
}
