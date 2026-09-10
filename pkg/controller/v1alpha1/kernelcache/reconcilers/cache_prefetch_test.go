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
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
)

func TestKernelCacheCheckNamespaceUsesAPIReader(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cachedClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	apiReader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "jobs"}},
	).Build()
	reconciler := &KernelCacheReconciler{Client: cachedClient, Reader: apiReader}

	if err := reconciler.checkNamespace(t.Context(), "jobs"); err != nil {
		t.Fatalf("expected API reader namespace lookup to succeed: %v", err)
	}
}

func TestOCIPrefetchChecksNodeCacheStatus(t *testing.T) {
	for _, readyImage := range []string{"registry.example.com/cache@sha256:aaa", "registry.example.com/cache@sha256:bbb"} {
		t.Run(readyImage, func(t *testing.T) {
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, corev1.AddToScheme, batchv1.AddToScheme} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}
			cache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team"}}
			cache.Spec.Artifact.ImageReference = "registry.example.com/cache@sha256:aaa"
			node := &v1alpha1.KernelCacheNode{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"}}
			node.Status.CacheStatus = map[string]v1alpha1.KernelCacheNodeCacheInfo{
				"team/cache": {State: v1alpha1.KernelCacheNodePreparationStateReady, ImageReference: readyImage},
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
			r := &KernelCacheReconciler{Client: cl}
			config := &v1beta1.KernelCacheConfig{JobNamespace: "jobs", PrefetchImage: "prefetch:test"}
			if err := r.ensureOCIPrefetchJob(context.Background(), cache, &v1alpha1.KernelCacheNodeGroup{}, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: node.Name}}, config); err != nil {
				t.Fatal(err)
			}
			jobs := &batchv1.JobList{}
			if err := cl.List(context.Background(), jobs); err != nil {
				t.Fatal(err)
			}
			want := 0
			if readyImage != cache.Spec.Artifact.ImageReference {
				want = 1
			}
			if len(jobs.Items) != want {
				t.Fatalf("expected %d Jobs, got %d", want, len(jobs.Items))
			}
		})
	}
}

func TestKernelCachePrefetchJobNameIsReadableAndDeterministic(t *testing.T) {
	cache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{
		Name:      "opt-cache-kcc-abcdef",
		Namespace: "team-a",
	}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ip-10-0-1-2"}}

	name := kernelCachePrefetchJobName(cache, node)
	if len(name) > 63 {
		t.Fatalf("expected Job name to fit Kubernetes limit, got %d characters: %q", len(name), name)
	}
	if !strings.HasPrefix(name, "kc-opt-cache-kcc-abcdef-ip-10-0-1-2-") {
		t.Fatalf("expected readable KCC and node identity in Job name, got %q", name)
	}
	if name != kernelCachePrefetchJobName(cache, node) {
		t.Fatalf("expected deterministic Job name, got %q", name)
	}

	otherNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ip-10-0-1-3"}}
	if name == kernelCachePrefetchJobName(cache, otherNode) {
		t.Fatal("expected different nodes to produce different Job names")
	}
}

// Keep successful preparation Jobs while the node image status catches up.
func TestOCIPrefetchJobWaitsForSuccessfulCompletion(t *testing.T) {
	r, cl, cache, node, completedJob, config := newPrefetchJobLifecycleHarness(t, v1alpha1.KernelCacheNodePreparationStatePending, batchv1.JobComplete)

	err := r.ensureOCIPrefetchJob(t.Context(), cache, &v1alpha1.KernelCacheNodeGroup{}, node, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Get(t.Context(), client.ObjectKeyFromObject(completedJob), &batchv1.Job{}); err != nil {
		t.Fatalf("expected successful Job to remain: %v", err)
	}
}

// Keep failed preparation Jobs until TTL cleanup triggers a retry.
func TestOCIPrefetchJobFailureWaitsForCleanup(t *testing.T) {
	r, cl, cache, node, failedJob, config := newPrefetchJobLifecycleHarness(t, v1alpha1.KernelCacheNodePreparationStateError, batchv1.JobFailed)

	err := r.ensureOCIPrefetchJob(t.Context(), cache, &v1alpha1.KernelCacheNodeGroup{}, node, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Get(t.Context(), client.ObjectKeyFromObject(failedJob), &batchv1.Job{}); err != nil {
		t.Fatalf("expected failed Job to remain: %v", err)
	}
}

// Do not create a prefetch Job before the node KCN exists.
func TestOCIPrefetchSkipsWithoutKernelCacheNode(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, corev1.AddToScheme, batchv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{Artifact: v1alpha1.KernelCacheArtifact{
			ImageReference: "registry.example.com/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"}}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cache).Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}
	config := &v1beta1.KernelCacheConfig{JobNamespace: "jobs", PrefetchImage: "prefetch:test"}

	if err := reconciler.ensureOCIPrefetchJob(t.Context(), cache, &v1alpha1.KernelCacheNodeGroup{}, node, config); err != nil {
		t.Fatal(err)
	}
	jobs := &batchv1.JobList{}
	if err := k8sClient.List(t.Context(), jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("expected no prefetch Jobs before KCN creation, got %d", len(jobs.Items))
	}
}

func TestOCIPrefetchWaitsForKernelCacheNodeStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, corev1.AddToScheme, batchv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{Artifact: v1alpha1.KernelCacheArtifact{
			ImageReference: "registry.example.com/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"}}
	kernelCacheNode := &v1alpha1.KernelCacheNode{ObjectMeta: metav1.ObjectMeta{Name: node.Name}}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cache, node, kernelCacheNode).Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}
	config := &v1beta1.KernelCacheConfig{JobNamespace: "jobs", PrefetchImage: "prefetch:test"}

	if err := reconciler.ensureOCIPrefetchJob(t.Context(), cache, &v1alpha1.KernelCacheNodeGroup{}, node, config); err != nil {
		t.Fatal(err)
	}
	jobs := &batchv1.JobList{}
	if err := k8sClient.List(t.Context(), jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("expected no prefetch Job before KCN status validation, got %d", len(jobs.Items))
	}
}

func newPrefetchJobLifecycleHarness(
	t *testing.T,
	state v1alpha1.KernelCacheNodePreparationState,
	conditionType batchv1.JobConditionType,
) (*KernelCacheReconciler, client.Client, *v1alpha1.KernelCache, *corev1.Node, *batchv1.Job, *v1beta1.KernelCacheConfig) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, corev1.AddToScheme, batchv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{Artifact: v1alpha1.KernelCacheArtifact{
			ImageReference: "registry.example.com/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"}}
	kernelCacheNode := &v1alpha1.KernelCacheNode{ObjectMeta: metav1.ObjectMeta{Name: node.Name}}
	kernelCacheNode.Status.CacheStatus = map[string]v1alpha1.KernelCacheNodeCacheInfo{
		"team/cache": {
			ImageReference: cache.Spec.Artifact.ImageReference,
			State:          state,
		},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: kernelCachePrefetchJobName(cache, node), Namespace: "jobs"},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: conditionType, Status: corev1.ConditionTrue,
		}}},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cache, kernelCacheNode, job).Build()
	return &KernelCacheReconciler{Client: cl}, cl, cache, node, job, &v1beta1.KernelCacheConfig{JobNamespace: "jobs", PrefetchImage: "prefetch:test"}
}
