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

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
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

func TestOCIPrefetchAfterJobCleanup(t *testing.T) {
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

	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: "gpu-workers"},
		},
	}
	wantedTolerations := []corev1.Toleration{{
		Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule,
	}}
	nodeGroup := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-workers"},
		Spec: v1alpha1.KernelCacheNodeGroupSpec{
			NodeSelector: map[string]string{"role": "gpu-worker"},
			Tolerations:  wantedTolerations,
			Storage: &v1alpha1.KernelCacheNodeGroupStorage{
				PersistentVolumeSpec: &corev1.PersistentVolumeSpec{
					NodeAffinity: &corev1.VolumeNodeAffinity{
						Required: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{{
								MatchExpressions: []corev1.NodeSelectorRequirement{{
									Key:      "kubernetes.io/hostname",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"gpu-node-1", "gpu-node-2"},
								}},
							}},
						},
					},
				},
				PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			},
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
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data: map[string]string{
			"kernelcache": `{"enabled":true,"jobNamespace":"kserve-kernelcache-jobs"}`,
		},
	}
	jobNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kserve-kernelcache-jobs"}}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cache, nodeGroup, node, node2, configMap, jobNamespace).
		WithStatusSubresource(cache).
		Build()
	clientset := kubernetesfake.NewSimpleClientset()
	reconciler := &KernelCacheReconciler{
		Client:    k8sClient,
		Clientset: clientset,
		Log:       logr.Discard(),
		Scheme:    scheme,
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
	if updated.Status.MountType != v1alpha1.KernelCacheMountTypePVC {
		t.Fatalf("expected pvc mount type, got %q", updated.Status.MountType)
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
	createdPV := &corev1.PersistentVolume{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "staging-qwen-cache-gpu-workers-gpu-node-1-download"}, createdPV); err != nil {
		t.Fatal(err)
	}
	createdPVC := &corev1.PersistentVolumeClaim{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "staging-qwen-cache-gpu-workers-gpu-node-1-download", Namespace: "kserve-kernelcache-jobs"}, createdPVC); err != nil {
		t.Fatal(err)
	}
	createdPV = &corev1.PersistentVolume{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "staging-qwen-cache-gpu-workers-gpu-node-2-download"}, createdPV); err != nil {
		t.Fatal(err)
	}
	createdPVC = &corev1.PersistentVolumeClaim{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "staging-qwen-cache-gpu-workers-gpu-node-2-download", Namespace: "kserve-kernelcache-jobs"}, createdPVC); err != nil {
		t.Fatal(err)
	}

	servingPV, err := clientset.CoreV1().PersistentVolumes().Get(context.Background(), "qwen-cache-gpu-workers-staging", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if servingPV.Labels[kernelCacheStorageTypeLabel] != kernelCacheServingStorageType {
		t.Fatalf("expected serving PV label, got %#v", servingPV.Labels)
	}
	servingPVC, err := clientset.CoreV1().PersistentVolumeClaims("staging").Get(context.Background(), "qwen-cache-gpu-workers", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if servingPVC.Spec.VolumeName != servingPV.Name {
		t.Fatalf("expected serving PVC to reference %q, got %q", servingPV.Name, servingPVC.Spec.VolumeName)
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

	requests := reconciler.kernelCacheRequests(t.Context(), "gpu-workers")
	if len(requests) != 1 || requests[0].Name != matching.Name {
		t.Fatalf("expected only matching cache request, got %#v", requests)
	}
	if requests := reconciler.kernelCacheRequests(t.Context(), ""); len(requests) != 2 {
		t.Fatalf("expected all cache requests without filtering, got %#v", requests)
	}
}

func TestValidateKernelCachePersistentVolumeSpec(t *testing.T) {
	path := "/var/lib/kernel-cache"
	if err := validateKernelCachePersistentVolumeSpec(corev1.PersistentVolumeSpec{
		PersistentVolumeSource: corev1.PersistentVolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: path},
		},
	}); err == nil {
		t.Fatal("expected hostPath to be rejected")
	}

	if err := validateKernelCachePersistentVolumeSpec(corev1.PersistentVolumeSpec{
		PersistentVolumeSource: corev1.PersistentVolumeSource{
			Local: &corev1.LocalVolumeSource{Path: path},
		},
	}); err != nil {
		t.Fatalf("expected local volume to be accepted: %v", err)
	}
}

// PR-3 storage-mode tests.

func newKernelCacheReconcilerHarness(t *testing.T, group *v1alpha1.KernelCacheNodeGroup, mountType v1alpha1.KernelCacheMountType) (*KernelCacheReconciler, client.Client, *kubernetesfake.Clientset, *v1alpha1.KernelCache) {
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

	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: group.Name},
			MountType:    mountType,
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
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cache, group, node, configMap, jobNamespace).
		WithStatusSubresource(cache).
		Build()
	clientset := kubernetesfake.NewSimpleClientset()
	return &KernelCacheReconciler{Client: k8sClient, Clientset: clientset, Log: logr.Discard(), Scheme: scheme}, k8sClient, clientset, cache
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
	reconciler, k8sClient, _, cache := newKernelCacheReconcilerHarness(t, group, v1alpha1.KernelCacheMountTypeOCI)
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
		t.Fatalf("OCI + storage=nil should not error, got %#v", updated.Status.Conditions)
	}
	pvList := &corev1.PersistentVolumeList{}
	if err := k8sClient.List(context.Background(), pvList); err != nil {
		t.Fatal(err)
	}
	if len(pvList.Items) != 0 {
		t.Fatalf("OCI-only group must not create PersistentVolumes, got %d", len(pvList.Items))
	}
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := k8sClient.List(context.Background(), pvcList); err != nil {
		t.Fatal(err)
	}
	if len(pvcList.Items) != 0 {
		t.Fatalf("OCI-only group must not create PersistentVolumeClaims, got %d", len(pvcList.Items))
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

func TestKernelCacheReconciler_OCIOnlyGroup_RejectsPVCMount(t *testing.T) {
	group := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "oci-workers"},
		Spec: v1alpha1.KernelCacheNodeGroupSpec{
			NodeSelector: map[string]string{"role": "gpu-worker"},
		},
	}
	reconciler, k8sClient, _, cache := newKernelCacheReconcilerHarness(t, group, v1alpha1.KernelCacheMountTypePVC)
	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: cache.Name, Namespace: cache.Namespace}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	updated := &v1alpha1.KernelCache{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.State != v1alpha1.KernelCacheStateError {
		t.Fatalf("PVC mount with OCI-only group must error, got %q", updated.Status.State)
	}
	if len(updated.Status.Conditions) != 1 || updated.Status.Conditions[0].Reason != reasonStorageError {
		t.Fatalf("expected StorageError reason, got %#v", updated.Status.Conditions)
	}
}

func TestKernelCacheReconciler_StorageClassMode_CreatesPVCOnly(t *testing.T) {
	size := resource.MustParse("50Gi")
	group := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "sc-workers"},
		Spec: v1alpha1.KernelCacheNodeGroupSpec{
			NodeSelector: map[string]string{"role": "gpu-worker"},
			Storage: &v1alpha1.KernelCacheNodeGroupStorage{
				StorageClass: &v1alpha1.KernelCacheNodeGroupStorageClass{
					Name:        "fast-ssd",
					Size:        &size,
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			},
		},
	}
	reconciler, k8sClient, clientset, cache := newKernelCacheReconcilerHarness(t, group, v1alpha1.KernelCacheMountTypePVC)
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
		t.Fatalf("StorageClass mode should not error, got %#v", updated.Status.Conditions)
	}

	// No PVs created by controller; provisioner is responsible.
	pvList := &corev1.PersistentVolumeList{}
	if err := k8sClient.List(context.Background(), pvList); err != nil {
		t.Fatal(err)
	}
	if len(pvList.Items) != 0 {
		t.Fatalf("StorageClass mode must not create PersistentVolumes, got %d", len(pvList.Items))
	}
	if _, err := clientset.CoreV1().PersistentVolumes().Get(context.Background(), servingPVName(cache, group), metav1.GetOptions{}); err == nil {
		t.Fatalf("StorageClass mode must not create serving PersistentVolume")
	}

	// PVC created for the node download slot, with StorageClassName set and no VolumeName.
	downloadPVCName := downloadStorageName(cache, group, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node-1"}})
	downloadPVC := &corev1.PersistentVolumeClaim{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: downloadPVCName, Namespace: "kserve-kernelcache-jobs"}, downloadPVC); err != nil {
		t.Fatalf("download PVC missing: %v", err)
	}
	if downloadPVC.Spec.StorageClassName == nil || *downloadPVC.Spec.StorageClassName != "fast-ssd" {
		t.Fatalf("expected StorageClassName=fast-ssd, got %v", downloadPVC.Spec.StorageClassName)
	}
	if downloadPVC.Spec.VolumeName != "" {
		t.Fatalf("StorageClass mode must not pin VolumeName, got %q", downloadPVC.Spec.VolumeName)
	}

	// Serving PVC created via clientset, same expectations.
	servingPVC, err := clientset.CoreV1().PersistentVolumeClaims(cache.Namespace).Get(context.Background(), servingStorageName(cache, group), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("serving PVC missing: %v", err)
	}
	if servingPVC.Spec.StorageClassName == nil || *servingPVC.Spec.StorageClassName != "fast-ssd" {
		t.Fatalf("serving PVC expected StorageClassName=fast-ssd, got %v", servingPVC.Spec.StorageClassName)
	}
	if servingPVC.Spec.VolumeName != "" {
		t.Fatalf("StorageClass mode serving PVC must not pin VolumeName, got %q", servingPVC.Spec.VolumeName)
	}
}
