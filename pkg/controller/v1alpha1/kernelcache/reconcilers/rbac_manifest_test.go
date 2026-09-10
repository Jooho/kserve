/*
Copyright 2026 The KServe Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reconcilers

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

func TestKernelCacheManagerCanReadKernelCacheNodeGroups(t *testing.T) {
	role := findKernelCacheClusterRole(t, "kserve-kernelcache-registry-bootstrap")
	require.True(t, policyRulesAllow(
		role.Rules,
		"serving.kserve.io",
		"kernelcachenodegroups",
		[]string{"get", "list", "watch"},
	), "kernelcache manager must be able to read and watch KernelCacheNodeGroups")
}

// Registry credential decisions require reading Deployment and ReplicaSet owners.
func TestKernelCacheManagerCanReadWorkloadOwners(t *testing.T) {
	role := findKernelCacheClusterRole(t, "kserve-kernelcache-registry-bootstrap")
	require.True(t, policyRulesAllow(
		role.Rules,
		"apps",
		"deployments",
		[]string{"get"},
	), "kernelcache manager must be able to read Deployment owners")
	require.True(t, policyRulesAllow(
		role.Rules,
		"apps",
		"replicasets",
		[]string{"get"},
	), "kernelcache manager must be able to read ReplicaSet owners")
}

func TestKernelCacheTokenRequesterRoleIsTokenRequestOnly(t *testing.T) {
	role := findKernelCacheClusterRole(t, "kserve-kernelcache-token-requester")

	require.Len(t, role.Rules, 1)
	require.Equal(t, []string{""}, role.Rules[0].APIGroups)
	require.Equal(t, []string{"serviceaccounts/token"}, role.Rules[0].Resources)
	require.Equal(t, []string{"create"}, role.Rules[0].Verbs)
	require.Empty(t, role.Rules[0].ResourceNames)
}

func TestKernelCacheBootstrapDoesNotGrantTokenRequest(t *testing.T) {
	role := findKernelCacheClusterRole(t, "kserve-kernelcache-registry-bootstrap")

	for _, rule := range role.Rules {
		require.NotContains(t, rule.Resources, "serviceaccounts/token")
	}
}

func TestKernelCacheBootstrapCanDeleteCaptureServiceAccounts(t *testing.T) {
	role := findKernelCacheClusterRole(t, "kserve-kernelcache-registry-bootstrap")

	require.True(t, policyRulesAllow(
		role.Rules,
		"",
		"serviceaccounts",
		[]string{"delete"},
	), "kernelcache bootstrap must be able to delete capture ServiceAccounts during cleanup")
}

func TestKernelCacheBootstrapBindsOnlyPredefinedClusterRoles(t *testing.T) {
	role := findKernelCacheClusterRole(t, "kserve-kernelcache-registry-bootstrap")

	resourceNames := make([]string, 0)
	for index := range role.Rules {
		rule := &role.Rules[index]
		if containsString(rule.Resources, "clusterroles") && containsString(rule.Verbs, "bind") {
			resourceNames = append(resourceNames, rule.ResourceNames...)
		}
	}

	require.ElementsMatch(t, []string{
		"system:image-builder",
		"system:image-puller",
		"kserve-kernelcache-token-requester",
	}, resourceNames)
}

func TestKernelCacheTokenRequesterHasNoClusterRoleBinding(t *testing.T) {
	for _, object := range loadManifestDocuments(t, kernelCacheManifestPath(t, "role_binding.yaml")) {
		if object.GetKind() != "ClusterRoleBinding" {
			continue
		}
		roleRef, found, err := unstructured.NestedString(object.Object, "roleRef", "name")
		require.NoError(t, err)
		if found {
			require.NotEqual(t, "kserve-kernelcache-token-requester", roleRef)
		}
	}
}

func findKernelCacheClusterRole(t *testing.T, name string) *rbacv1.ClusterRole {
	t.Helper()
	for _, manifestName := range []string{"role.yaml", "token_requester_role.yaml"} {
		for _, object := range loadManifestDocuments(t, kernelCacheManifestPath(t, manifestName)) {
			if object.GetKind() != "ClusterRole" || object.GetName() != name {
				continue
			}
			role := &rbacv1.ClusterRole{}
			require.NoError(t, k8sruntime.DefaultUnstructuredConverter.FromUnstructured(object.Object, role))
			return role
		}
	}
	t.Fatalf("ClusterRole %q not found", name)
	return nil
}

func kernelCacheManifestPath(t *testing.T, name string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(filename), "../../../../../config/rbac/kernelcache", name)
}

func loadManifestDocuments(t *testing.T, path string) []unstructured.Unstructured {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- the path is resolved from this test source file
	require.NoError(t, err)

	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	objects := make([]unstructured.Unstructured, 0)
	for {
		var object map[string]interface{}
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if len(object) == 0 {
			continue
		}
		objects = append(objects, unstructured.Unstructured{Object: object})
	}
	return objects
}

func policyRulesAllow(rules []rbacv1.PolicyRule, apiGroup, resource string, requiredVerbs []string) bool {
	for _, rule := range rules {
		if !containsString(rule.APIGroups, apiGroup) || !containsString(rule.Resources, resource) {
			continue
		}
		allowed := true
		for _, verb := range requiredVerbs {
			if !containsString(rule.Verbs, verb) {
				allowed = false
				break
			}
		}
		if allowed {
			return true
		}
	}
	return false
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected || value == "*" {
			return true
		}
	}
	return false
}
