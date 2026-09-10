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
	"reflect"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	kernelcachetypes "github.com/kserve/kserve/pkg/kernelcache/types"
)

func TestKernelCacheVerificationBlocksWhenTrustBundleIsUnavailable(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{Artifact: v1alpha1.KernelCacheArtifact{
			ImageReference: "registry.example.com/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cache).WithStatusSubresource(cache).Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}
	config := &v1beta1.KernelCacheConfig{ArtifactSecurity: v1beta1.KernelCacheArtifactSecurityConfig{
		Mode:          string(kernelcachetypes.ModeCert),
		FailurePolicy: string(kernelcachetypes.FailurePolicyReject),
		Cert: v1beta1.KernelCacheArtifactCertConfig{
			TrustBundle:   "kserve/kernel-cache-ca",
			SubjectRegexp: "kernel-cache-signer",
		},
	}}

	verified, err := reconciler.reconcileArtifactVerification(t.Context(), cache, config)
	if err == nil || verified {
		t.Fatal("expected unavailable trust bundle to block verification")
	}
	updated := &v1alpha1.KernelCache{}
	if err := k8sClient.Get(t.Context(), client.ObjectKeyFromObject(cache), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Verification == nil || updated.Status.Verification.State != v1alpha1.KernelCacheArtifactSecurityStateFailed {
		t.Fatalf("expected failed verification status, got %#v", updated.Status.Verification)
	}
}

func TestKernelCacheReconcilerCleansUpUnusedPrefetchRoleBindingAfterDeletion(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, v1beta1.AddToScheme, corev1.AddToScheme, batchv1.AddToScheme, rbacv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	binding := buildPrefetchImagePullRoleBinding("source", "jobs", &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"})
	binding.UID = "binding-uid"
	config := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{v1beta1.KernelCacheConfigName: `{"enabled":false,"jobNamespace":"jobs"}`},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(binding, config).Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}

	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "source", Name: "deleted-cache"}}); err != nil {
		t.Fatal(err)
	}

	remaining := &rbacv1.RoleBinding{}
	if err := k8sClient.Get(t.Context(), client.ObjectKey{Namespace: "source", Name: kernelCachePrefetchRoleBinding}, remaining); !apierrors.IsNotFound(err) {
		t.Fatalf("expected unused prefetch RoleBinding to be deleted, got %v", err)
	}
}

func TestKernelCacheReconcilerUpdatesStatusForMatchingNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"},
	}
	wantedTolerations := []corev1.Toleration{{
		Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule,
	}}
	nodeGroup := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-workers"},
		Spec: v1alpha1.KernelCacheNodeGroupSpec{
			NodeSelector: map[string]string{"role": "gpu-worker"},
			Tolerations:  wantedTolerations,
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "gpu-node-1",
			Labels: map[string]string{"kubernetes.io/hostname": "gpu-node-1", "role": "gpu-worker"},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type:   corev1.NodeReady,
			Status: corev1.ConditionTrue,
		}}},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "gpu-node-2",
			Labels: map[string]string{"kubernetes.io/hostname": "gpu-node-2", "role": "gpu-worker"},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type:   corev1.NodeReady,
			Status: corev1.ConditionTrue,
		}}},
	}
	kernelCacheNode1 := &v1alpha1.KernelCacheNode{
		ObjectMeta: metav1.ObjectMeta{Name: node.Name},
		Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
			"staging/qwen-cache": {State: v1alpha1.KernelCacheNodePreparationStatePending},
		}},
	}
	kernelCacheNode2 := &v1alpha1.KernelCacheNode{
		ObjectMeta: metav1.ObjectMeta{Name: node2.Name},
		Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
			"staging/qwen-cache": {State: v1alpha1.KernelCacheNodePreparationStatePending},
		}},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data: map[string]string{
			"kernelcache": `{"enabled":true,"defaultNodeGroup":"gpu-workers","jobNamespace":"kserve-kernelcache-jobs"}`,
		},
	}
	jobNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kserve-kernelcache-jobs"}}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cache, nodeGroup, node, node2, kernelCacheNode1, kernelCacheNode2, configMap, jobNamespace).
		WithStatusSubresource(cache).
		Build()
	reconciler := &KernelCacheReconciler{
		Client: k8sClient,
	}

	reconcileRequest := ctrl.Request{NamespacedName: client.ObjectKey{Name: cache.Name, Namespace: cache.Namespace}}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest); err != nil {
		t.Fatal(err)
	}

	updated := &v1alpha1.KernelCache{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.State != v1alpha1.KernelCacheStatePending {
		t.Fatalf("expected pending state, got %q", updated.Status.State)
	}
	if updated.Status.MountType != v1alpha1.KernelCacheMountTypeOCI {
		t.Fatalf("expected oci mount type, got %q", updated.Status.MountType)
	}
	if updated.Status.Verification == nil || updated.Status.Verification.State != v1alpha1.KernelCacheArtifactSecurityStateSkipped {
		t.Fatalf("expected verification to be skipped in none mode, got %#v", updated.Status.Verification)
	}
	if updated.Status.Counts == nil || updated.Status.Counts.NodeCount != 2 {
		t.Fatalf("expected two selected nodes, got %#v", updated.Status.Counts)
	}
	if len(updated.Status.Conditions) != 1 || updated.Status.Conditions[0].Reason != reasonWaitingForPreparation {
		t.Fatalf("unexpected conditions: %#v", updated.Status.Conditions)
	}

	configuredNamespace := &corev1.Namespace{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "kserve-kernelcache-jobs"}, configuredNamespace); err != nil {
		t.Fatal(err)
	}
	jobs := &batchv1.JobList{}
	if err := k8sClient.List(context.Background(), jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 2 {
		t.Fatalf("expected two preparation Jobs, got %d", len(jobs.Items))
	}
	for i := range jobs.Items {
		if !reflect.DeepEqual(jobs.Items[i].Spec.Template.Spec.Tolerations, wantedTolerations) {
			t.Fatalf("preparation Job %q tolerations = %#v, want %#v", jobs.Items[i].Name, jobs.Items[i].Spec.Template.Spec.Tolerations, wantedTolerations)
		}
	}
}

func TestAggregateKernelCacheNodeStatuses(t *testing.T) {
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"}}
	kernelCacheNodes := &v1alpha1.KernelCacheNodeList{
		Items: []v1alpha1.KernelCacheNode{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu-node-1"},
				Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
					"staging/qwen-cache": {State: v1alpha1.KernelCacheNodePreparationStateReady},
				}},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu-node-2"},
				Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
					"staging/qwen-cache": {State: v1alpha1.KernelCacheNodePreparationStateExtracting},
				}},
			},
		},
	}

	aggregate := aggregateKernelCacheNodeStatuses(kernelCache, []string{"gpu-node-1", "gpu-node-2"}, kernelCacheNodes)
	if aggregate.State != v1alpha1.KernelCacheStatePreparing {
		t.Fatalf("expected preparing state, got %q", aggregate.State)
	}
	if aggregate.NodesReady != 1 || aggregate.NodesPreparing != 1 || aggregate.NodesError != 0 {
		t.Fatalf("unexpected aggregate counts: %#v", aggregate)
	}
}

func TestAggregateKernelCacheStatusIgnoresNotReadyNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team"}}
	readyNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ready-node"}}
	notReadyNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "not-ready-node"}}
	kernelCacheNodes := &v1alpha1.KernelCacheNodeList{Items: []v1alpha1.KernelCacheNode{
		{
			ObjectMeta: metav1.ObjectMeta{Name: readyNode.Name},
			Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
				"team/cache": {State: v1alpha1.KernelCacheNodePreparationStateReady},
			}},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: notReadyNode.Name},
			Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
				"team/cache": {State: v1alpha1.KernelCacheNodePreparationStatePending},
			}},
		},
	}}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(&kernelCacheNodes.Items[0], &kernelCacheNodes.Items[1]).
		Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}

	aggregate, err := reconciler.aggregateKernelCacheStatus(t.Context(), kernelCache, &corev1.NodeList{Items: []corev1.Node{*readyNode}})
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.State != v1alpha1.KernelCacheStateReady {
		t.Fatalf("expected Ready state, got %q", aggregate.State)
	}
	if aggregate.NodeCount != 1 || aggregate.NodesReady != 1 || aggregate.NodesPreparing != 0 || aggregate.NodesError != 0 {
		t.Fatalf("unexpected aggregate counts: %#v", aggregate)
	}
}

func TestKernelCacheReconcilerReportsNoReadyNodesWithZeroCount(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, corev1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	group := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-workers"},
		Spec:       v1alpha1.KernelCacheNodeGroupSpec{NodeSelector: map[string]string{"role": "gpu"}},
	}
	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: group.Name},
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-node", Labels: map[string]string{"role": "gpu"}},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"jobNamespace":"jobs"}`},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cache).WithObjects(group, cache, node, configMap).Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}

	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cache)}); err != nil {
		t.Fatal(err)
	}
	updated := &v1alpha1.KernelCache{}
	if err := k8sClient.Get(t.Context(), client.ObjectKeyFromObject(cache), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.State != v1alpha1.KernelCacheStatePending {
		t.Fatalf("expected Pending state, got %q", updated.Status.State)
	}
	if updated.Status.Counts == nil || updated.Status.Counts.NodeCount != 0 {
		t.Fatalf("expected zero eligible nodes, got %#v", updated.Status.Counts)
	}
	if len(updated.Status.Conditions) != 1 || updated.Status.Conditions[0].Reason != reasonNoReadyNodes {
		t.Fatalf("expected NoReadyNodes condition, got %#v", updated.Status.Conditions)
	}
}

func TestKernelCacheReconcilerIgnoresNotReadyNodeInAggregate(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, corev1.AddToScheme, batchv1.AddToScheme, rbacv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	const imageReference = "registry.example.com/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	group := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-workers"},
		Spec:       v1alpha1.KernelCacheNodeGroupSpec{NodeSelector: map[string]string{"role": "gpu"}},
	}
	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: group.Name},
			Artifact:     v1alpha1.KernelCacheArtifact{ImageReference: imageReference},
		},
	}
	readyNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "ready-node", Labels: map[string]string{"role": "gpu", "kubernetes.io/hostname": "ready-node"}},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
	notReadyNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "not-ready-node", Labels: map[string]string{"role": "gpu", "kubernetes.io/hostname": "not-ready-node"}},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}},
	}
	readyKernelCacheNode := &v1alpha1.KernelCacheNode{
		ObjectMeta: metav1.ObjectMeta{Name: readyNode.Name},
		Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
			"team/cache": {State: v1alpha1.KernelCacheNodePreparationStateReady, ImageReference: imageReference},
		}},
	}
	notReadyKernelCacheNode := &v1alpha1.KernelCacheNode{ObjectMeta: metav1.ObjectMeta{Name: notReadyNode.Name}}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"jobNamespace":"jobs"}`},
	}
	jobNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "jobs"}}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(cache).
		WithObjects(group, cache, readyNode, notReadyNode, readyKernelCacheNode, notReadyKernelCacheNode, configMap, jobNamespace).
		Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}

	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cache)}); err != nil {
		t.Fatal(err)
	}
	updated := &v1alpha1.KernelCache{}
	if err := k8sClient.Get(t.Context(), client.ObjectKeyFromObject(cache), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.State != v1alpha1.KernelCacheStateReady {
		t.Fatalf("expected Ready state, got %q", updated.Status.State)
	}
	if updated.Status.Counts == nil || updated.Status.Counts.NodeCount != 1 || updated.Status.Counts.NodesReady != 1 || updated.Status.Counts.NodesPreparing != 0 {
		t.Fatalf("unexpected aggregate counts: %#v", updated.Status.Counts)
	}
}

func TestKernelCacheReconcilerReportsMissingNodeGroup(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: "missing-workers"},
		},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data: map[string]string{
			"kernelcache": `{"enabled":true,"jobNamespace":"kserve-kernelcache-jobs"}`,
		},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cache, configMap).
		WithStatusSubresource(cache).
		Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: cache.Name, Namespace: cache.Namespace}}); err != nil {
		t.Fatal(err)
	}

	updated := &v1alpha1.KernelCache{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.State != v1alpha1.KernelCacheStateError {
		t.Fatalf("expected error state, got %q", updated.Status.State)
	}
	if len(updated.Status.Conditions) != 1 || updated.Status.Conditions[0].Reason != reasonNodeGroupNotFound {
		t.Fatalf("unexpected conditions: %#v", updated.Status.Conditions)
	}
}

func TestKernelCacheReconcilerReportsMissingNodeGroupReference(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data: map[string]string{
			"kernelcache": `{"enabled":true,"jobNamespace":"kserve-kernelcache-jobs"}`,
		},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cache, configMap).
		WithStatusSubresource(cache).
		Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}

	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cache)}); err != nil {
		t.Fatal(err)
	}

	updated := &v1alpha1.KernelCache{}
	if err := k8sClient.Get(t.Context(), client.ObjectKeyFromObject(cache), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.State != v1alpha1.KernelCacheStateError {
		t.Fatalf("expected error state, got %q", updated.Status.State)
	}
	if len(updated.Status.Conditions) != 1 || updated.Status.Conditions[0].Reason != reasonNodeGroupNotFound {
		t.Fatalf("unexpected conditions: %#v", updated.Status.Conditions)
	}
}

func TestKernelCacheRequestsSkipMissingNodeGroupReferenceWhenFiltering(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	withoutRef := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "without-ref", Namespace: "team"}}
	matching := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "matching", Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: "gpu-workers"},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(withoutRef, matching).Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}

	requests := reconciler.kcRequestsForNodeGroup(t.Context(), "gpu-workers")
	if len(requests) != 1 || requests[0].Name != matching.Name {
		t.Fatalf("expected only matching cache request, got %#v", requests)
	}
	if requests := reconciler.kcRequestsForNodeGroup(t.Context(), ""); len(requests) != 2 {
		t.Fatalf("expected all cache requests without filtering, got %#v", requests)
	}
}

func newKernelCacheReconcilerHarness(t *testing.T, group *v1alpha1.KernelCacheNodeGroup) (*KernelCacheReconciler, client.Client, *v1alpha1.KernelCache) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: group.Name},
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "gpu-node-1",
			Labels: map[string]string{"kubernetes.io/hostname": "gpu-node-1", "role": "gpu-worker"},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"jobNamespace":"kserve-kernelcache-jobs"}`},
	}
	jobNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kserve-kernelcache-jobs"}}
	kernelCacheNode := &v1alpha1.KernelCacheNode{
		ObjectMeta: metav1.ObjectMeta{Name: node.Name},
		Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
			"staging/qwen-cache": {State: v1alpha1.KernelCacheNodePreparationStatePending},
		}},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cache, group, node, kernelCacheNode, configMap, jobNamespace).
		WithStatusSubresource(cache).
		Build()
	return &KernelCacheReconciler{Client: k8sClient}, k8sClient, cache
}

func TestKernelCacheReconciler_OCIOnlyGroup_Accepted(t *testing.T) {
	wantedTolerations := []corev1.Toleration{{
		Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule,
	}}
	group := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "oci-workers"},
		Spec: v1alpha1.KernelCacheNodeGroupSpec{
			NodeSelector: map[string]string{"role": "gpu-worker"},
			Tolerations:  wantedTolerations,
		},
	}
	reconciler, k8sClient, cache := newKernelCacheReconcilerHarness(t, group)
	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: cache.Name, Namespace: cache.Namespace}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	updated := &v1alpha1.KernelCache{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.State == v1alpha1.KernelCacheStateError {
		t.Fatalf("OCI-only node group should not error, got %#v", updated.Status.Conditions)
	}
	jobs := &batchv1.JobList{}
	if err := k8sClient.List(context.Background(), jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("expected one OCI prefetch Job, got %d", len(jobs.Items))
	}
	if !reflect.DeepEqual(jobs.Items[0].Spec.Template.Spec.Tolerations, wantedTolerations) {
		t.Fatalf("OCI prefetch Job tolerations = %#v, want %#v", jobs.Items[0].Spec.Template.Spec.Tolerations, wantedTolerations)
	}
}
