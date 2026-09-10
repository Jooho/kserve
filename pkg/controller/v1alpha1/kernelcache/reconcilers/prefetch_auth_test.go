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
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
)

func TestEnsurePrefetchIdentitySplitsJobAndSourceNamespaces(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{
		JobNamespace: "jobs",
		Registry: v1beta1.KernelCacheRegistryConfig{
			Endpoint: "registry.example/source",
			Auth:     v1beta1.KernelCacheRegistryAuth{Type: "openshift"},
		},
	}

	if err := r.ensurePrefetchIdentity(t.Context(), kernelCache, config); err != nil {
		t.Fatal(err)
	}

	serviceAccount := &corev1.ServiceAccount{}
	if err := cl.Get(t.Context(), client.ObjectKey{Namespace: "jobs", Name: kernelCachePrefetchServiceAccount}, serviceAccount); err != nil {
		t.Fatal(err)
	}
	if serviceAccount.Labels[kernelCachePrefetchManagedLabel] != "true" {
		t.Fatalf("prefetch ServiceAccount is not managed: %#v", serviceAccount.Labels)
	}

	binding := &rbacv1.RoleBinding{}
	if err := cl.Get(t.Context(), client.ObjectKey{Namespace: "source", Name: kernelCachePrefetchRoleBinding}, binding); err != nil {
		t.Fatal(err)
	}
	if binding.RoleRef.Name != "system:image-puller" {
		t.Fatalf("unexpected RoleRef: %#v", binding.RoleRef)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Namespace != "jobs" || binding.Subjects[0].Name != kernelCachePrefetchServiceAccount {
		t.Fatalf("unexpected RoleBinding subjects: %#v", binding.Subjects)
	}

	bindings := &rbacv1.RoleBindingList{}
	if err := cl.List(t.Context(), bindings); err != nil {
		t.Fatal(err)
	}
	if len(bindings.Items) != 1 || bindings.Items[0].Namespace != "source" {
		t.Fatalf("expected only the image source namespace binding, got %#v", bindings.Items)
	}
}

func TestEnsurePrefetchIdentityUsesKernelCacheNamespaceForSourceBinding(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{JobNamespace: "jobs"}

	if err := r.ensurePrefetchIdentity(t.Context(), kernelCache, config); err != nil {
		t.Fatal(err)
	}

	serviceAccounts := &corev1.ServiceAccountList{}
	if err := cl.List(t.Context(), serviceAccounts); err != nil {
		t.Fatal(err)
	}
	if len(serviceAccounts.Items) != 1 || serviceAccounts.Items[0].Namespace != "jobs" {
		t.Fatalf("expected one prefetch ServiceAccount in jobs, got %#v", serviceAccounts.Items)
	}
}

func TestPrefetchIdentityRequestsEnqueueAllKernelCaches(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache-a", Namespace: "team-a"}},
		&v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache-b", Namespace: "team-b"}},
	).Build()
	r := &KernelCacheReconciler{Client: cl}

	requests := r.prefetchIdentityRequests(t.Context(), &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
		Name: kernelCachePrefetchRoleBinding, Namespace: "team-a",
	}})
	if len(requests) != 2 {
		t.Fatalf("expected two KernelCache requests, got %#v", requests)
	}
}
