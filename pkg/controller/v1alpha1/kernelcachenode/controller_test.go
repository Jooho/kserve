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

package kernelcachenode

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

func TestPreparationState(t *testing.T) {
	kernelCache := &v1alpha1.KernelCache{
		Spec: v1alpha1.KernelCacheSpec{MountType: v1alpha1.KernelCacheMountTypePVC},
	}
	boundPVC := &corev1.PersistentVolumeClaim{Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound}}
	pendingPVC := &corev1.PersistentVolumeClaim{Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending}}
	startedAt := metav1.Now()

	tests := []struct {
		name       string
		job        *batchv1.Job
		pvc        *corev1.PersistentVolumeClaim
		current    v1alpha1.KernelCacheNodePreparationState
		podFailure string
		wantState  v1alpha1.KernelCacheNodePreparationState
		wantReason string
	}{
		{
			name:       "no resources",
			wantState:  v1alpha1.KernelCacheNodePreparationStatePending,
			wantReason: "waiting for the preparation Job",
		},
		{
			name:       "pvc is pending",
			pvc:        pendingPVC,
			wantState:  v1alpha1.KernelCacheNodePreparationStatePending,
			wantReason: "waiting for the download PVC to bind",
		},
		{
			name:       "pvc is bound without job",
			pvc:        boundPVC,
			wantState:  v1alpha1.KernelCacheNodePreparationStatePending,
			wantReason: "waiting for the preparation Job",
		},
		{
			name: "job is running",
			job: &batchv1.Job{Status: batchv1.JobStatus{
				Active:    1,
				StartTime: &startedAt,
			}},
			pvc:       boundPVC,
			wantState: v1alpha1.KernelCacheNodePreparationStateExtracting,
		},
		{
			name:       "pod image pull failed",
			job:        &batchv1.Job{Status: batchv1.JobStatus{Active: 1, StartTime: &startedAt}},
			pvc:        boundPVC,
			podFailure: "Back-off pulling image",
			wantState:  v1alpha1.KernelCacheNodePreparationStateError,
			wantReason: "Back-off pulling image",
		},
		{
			name: "job completed",
			job: &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
				Type:   batchv1.JobComplete,
				Status: corev1.ConditionTrue,
			}}}},
			pvc:       boundPVC,
			wantState: v1alpha1.KernelCacheNodePreparationStateReady,
		},
		{
			name: "job completed before pvc is available",
			job: &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
				Type:   batchv1.JobComplete,
				Status: corev1.ConditionTrue,
			}}}},
			wantState:  v1alpha1.KernelCacheNodePreparationStatePulling,
			wantReason: "waiting for the download PVC to bind",
		},
		{
			name: "job failed",
			job: &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
				Type:    batchv1.JobFailed,
				Status:  corev1.ConditionTrue,
				Message: "image pull failed",
			}}}},
			pvc:        boundPVC,
			wantState:  v1alpha1.KernelCacheNodePreparationStateError,
			wantReason: "image pull failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, message := preparationState(kernelCache, test.job, test.pvc, test.current, test.podFailure)
			if state != test.wantState {
				t.Fatalf("expected state %q, got %q", test.wantState, state)
			}
			if test.wantReason != "" && message != test.wantReason {
				t.Fatalf("expected message %q, got %q", test.wantReason, message)
			}
		})
	}
}

func TestDiscoverCachesForNode(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "gpu-node-1",
			Labels: map[string]string{"kubernetes.io/hostname": "gpu-node-1"},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type:   corev1.NodeReady,
			Status: corev1.ConditionTrue,
		}}},
	}
	nodeGroup := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-workers"},
		Spec: v1alpha1.KernelCacheNodeGroupSpec{
			NodeSelector: map[string]string{"kubernetes.io/hostname": "gpu-node-1"},
		},
	}
	kernelCache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: nodeGroup.Name},
			Artifact: v1alpha1.KernelCacheArtifact{
				Identity: v1alpha1.KernelCacheIdentity{
					Footprints: v1alpha1.KernelCacheFootprints{
						WorkloadFootprint:      "sha256:1111111111111111111111111111111111111111111111111111111111111111",
						CompatibilityFootprint: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
					},
				},
			},
		},
		Status: v1alpha1.KernelCacheStatus{
			Usage: &v1alpha1.KernelCacheUsage{Pods: []v1alpha1.KernelCachePodUsage{{
				PodUID:   "pod-1",
				NodeName: node.Name,
			}}},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node, nodeGroup, kernelCache).
		Build()
	reconciler := &KernelCacheNodeReconciler{Client: k8sClient, NodeName: node.Name}
	kernelCacheNode := &v1alpha1.KernelCacheNode{}

	podsUsing, err := reconciler.discoverCaches(context.Background(), kernelCacheNode)
	if err != nil {
		t.Fatal(err)
	}
	if podsUsing != 1 {
		t.Fatalf("expected one Pod using the cache, got %d", podsUsing)
	}

	cacheInfo, ok := kernelCacheNode.Status.CacheStatus["staging/qwen-cache"]
	if !ok {
		t.Fatal("expected matching KernelCache in node status")
	}
	if cacheInfo.KernelCacheRef.Name != kernelCache.Name || cacheInfo.KernelCacheRef.Namespace != kernelCache.Namespace {
		t.Fatalf("unexpected cache reference: %#v", cacheInfo.KernelCacheRef)
	}
	if cacheInfo.State != v1alpha1.KernelCacheNodePreparationStatePending {
		t.Fatalf("expected pending state, got %q", cacheInfo.State)
	}
	if cacheInfo.Footprints != kernelCache.Spec.Artifact.Identity.Footprints {
		t.Fatalf("expected cache footprints to be copied")
	}
}

func TestCacheWithoutNodeGroupDoesNotMatchNode(t *testing.T) {
	reconciler := &KernelCacheNodeReconciler{}
	matched, err := reconciler.cacheMatchesNode(t.Context(), &v1alpha1.KernelCache{})
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("expected cache without nodeGroupRef not to match the node")
	}
}
