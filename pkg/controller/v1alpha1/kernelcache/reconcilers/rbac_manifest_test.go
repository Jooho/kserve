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
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

func TestLocalModelManagerCanReadKernelCacheNodeGroups(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	manifestPath := filepath.Join(filepath.Dir(filename), "../../../../../config/rbac/localmodel/role.yaml")
	data, err := os.ReadFile(manifestPath) // #nosec G304 -- the path is resolved from this test source file
	require.NoError(t, err)

	role := &rbacv1.ClusterRole{}
	require.NoError(t, yaml.Unmarshal(data, role))
	require.True(t, policyRulesAllow(
		role.Rules,
		"serving.kserve.io",
		"kernelcachenodegroups",
		[]string{"get", "list", "watch"},
	), "localmodel manager must be able to read and watch KernelCacheNodeGroups")
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
