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
	"errors"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/constants"
)

func TestPreparationState(t *testing.T) {
	startedAt := metav1.Now()
	tests := []struct {
		name       string
		job        *batchv1.Job
		current    v1alpha1.KernelCacheNodePreparationState
		podFailure string
		wantState  v1alpha1.KernelCacheNodePreparationState
		wantReason string
	}{
		{
			name:       "no prefetch job",
			wantState:  v1alpha1.KernelCacheNodePreparationStatePending,
			wantReason: "waiting for the OCI prefetch Job",
		},
		{
			name:       "prefetch job is running",
			job:        &batchv1.Job{Status: batchv1.JobStatus{Active: 1, StartTime: &startedAt}},
			wantState:  v1alpha1.KernelCacheNodePreparationStatePulling,
			wantReason: "OCI artifact is being prefetched",
		},
		{
			name:       "prefetch pod image pull failed",
			job:        &batchv1.Job{Status: batchv1.JobStatus{Active: 1, StartTime: &startedAt}},
			podFailure: "Back-off pulling image",
			wantState:  v1alpha1.KernelCacheNodePreparationStateError,
			wantReason: "Back-off pulling image",
		},
		{
			name: "prefetch job completed",
			job: &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
				Type: batchv1.JobComplete, Status: corev1.ConditionTrue,
			}}}},
			wantState:  v1alpha1.KernelCacheNodePreparationStateReady,
			wantReason: "OCI artifact was prefetched on the node",
		},
		{
			name: "prefetch job failed",
			job: &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
				Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Message: "image pull failed",
			}}}},
			wantState:  v1alpha1.KernelCacheNodePreparationStateError,
			wantReason: "image pull failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, message := preparationState(test.job, test.current, test.podFailure)
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

	podsUsing, changed, err := reconciler.discoverCaches(context.Background(), kernelCacheNode)
	if err != nil {
		t.Fatal(err)
	}
	if podsUsing != 1 {
		t.Fatalf("expected one Pod using the cache, got %d", podsUsing)
	}
	if !changed {
		t.Fatal("expected cache discovery to report a new CacheStatus entry")
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

func TestCurrentNodeReadinessPredicate(t *testing.T) {
	reconciler := &KernelCacheNodeReconciler{NodeName: "gpu-node"}
	pred := reconciler.currentNodeReadinessPredicate()

	readyNode := func(name string, ready bool) *corev1.Node {
		status := corev1.ConditionFalse
		if ready {
			status = corev1.ConditionTrue
		}
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}},
		}
	}

	if !pred.Update(event.UpdateEvent{ObjectOld: readyNode("gpu-node", false), ObjectNew: readyNode("gpu-node", true)}) {
		t.Fatal("expected NotReady to Ready transition to enqueue the node")
	}
	if !pred.Update(event.UpdateEvent{ObjectOld: readyNode("gpu-node", true), ObjectNew: readyNode("gpu-node", false)}) {
		t.Fatal("expected Ready to NotReady transition to enqueue the node")
	}
	if pred.Update(event.UpdateEvent{ObjectOld: readyNode("gpu-node", true), ObjectNew: readyNode("gpu-node", true)}) {
		t.Fatal("did not expect an enqueue for an unchanged readiness state")
	}
	if pred.Update(event.UpdateEvent{ObjectOld: readyNode("other-node", false), ObjectNew: readyNode("other-node", true)}) {
		t.Fatal("did not expect another node to enqueue this agent")
	}
}

func TestCurrentJobPredicate(t *testing.T) {
	reconciler := &KernelCacheNodeReconciler{NodeName: "gpu-node"}
	pred := reconciler.currentJobPredicate()

	job := func(namespace string, labels map[string]string) *batchv1.Job {
		return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Labels: labels}}
	}
	relevantLabels := map[string]string{
		kernelCacheNameLabel:      "cache",
		kernelCacheNamespaceLabel: "team-a",
		kernelCacheNodeLabel:      "gpu-node",
	}

	if !pred.Create(event.CreateEvent{Object: job("kernel-cache-jobs", relevantLabels)}) {
		t.Fatal("expected the configured node Job to enqueue the node")
	}
	if !pred.Create(event.CreateEvent{Object: job("other-jobs", relevantLabels)}) {
		t.Fatal("expected a labeled Job to pass the predicate before namespace resolution")
	}
	if pred.Create(event.CreateEvent{Object: job("kernel-cache-jobs", map[string]string{})}) {
		t.Fatal("did not expect an unrelated Job to enqueue the node")
	}
	wrongNodeLabels := map[string]string{
		kernelCacheNameLabel:      "cache",
		kernelCacheNamespaceLabel: "team-a",
		kernelCacheNodeLabel:      "other-node",
	}
	if pred.Create(event.CreateEvent{Object: job("kernel-cache-jobs", wrongNodeLabels)}) {
		t.Fatal("did not expect another node's Job to enqueue this node")
	}
}

func TestEnqueueCurrentNodeForJobUsesConfiguredNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"jobNamespace":"kernel-cache-jobs"}`},
	}
	reconciler := &KernelCacheNodeReconciler{
		Client:   fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap).Build(),
		NodeName: "gpu-node",
	}
	labels := map[string]string{
		kernelCacheNameLabel:      "cache",
		kernelCacheNamespaceLabel: "team-a",
		kernelCacheNodeLabel:      "gpu-node",
	}
	job := func(namespace string) *batchv1.Job {
		return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Labels: labels}}
	}

	if requests := reconciler.enqueueCurrentNodeForJob(t.Context(), job("kernel-cache-jobs")); len(requests) != 1 {
		t.Fatalf("expected one request for the configured Job namespace, got %#v", requests)
	}
	if requests := reconciler.enqueueCurrentNodeForJob(t.Context(), job("other-jobs")); len(requests) != 0 {
		t.Fatalf("did not expect a request for another Job namespace, got %#v", requests)
	}
}

func TestInferenceServiceConfigMapPredicate(t *testing.T) {
	reconciler := &KernelCacheNodeReconciler{}
	pred := reconciler.inferenceServiceConfigMapPredicate()

	configMap := func(namespace, name string) *corev1.ConfigMap {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	}
	if !pred.Create(event.CreateEvent{Object: configMap(constants.KServeNamespace, constants.InferenceServiceConfigMapName)}) {
		t.Fatal("expected the InferenceService ConfigMap to enqueue the node")
	}
	if pred.Create(event.CreateEvent{Object: configMap("other", constants.InferenceServiceConfigMapName)}) {
		t.Fatal("did not expect a ConfigMap from another namespace to enqueue the node")
	}
	if pred.Create(event.CreateEvent{Object: configMap(constants.KServeNamespace, "other")}) {
		t.Fatal("did not expect another ConfigMap to enqueue the node")
	}
}

func TestCurrentPodPredicate(t *testing.T) {
	reconciler := &KernelCacheNodeReconciler{}
	pred := reconciler.currentPodPredicate()

	withISVCLabel := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{constants.InferenceServicePodLabelKey: "model"},
	}}
	withoutISVCLabel := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{kernelCacheNameLabel: "cache"},
	}}

	if !pred.Create(event.CreateEvent{Object: withISVCLabel}) {
		t.Fatal("expected an InferenceService Pod to be watched")
	}
	if pred.Create(event.CreateEvent{Object: withoutISVCLabel}) {
		t.Fatal("did not expect a Pod without an InferenceService label to be watched")
	}
}

func TestDiscoverCachesRemovesAndRestoresEntryWithNodeReadiness(t *testing.T) {
	const nodeName = "gpu-node-1"

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, corev1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Labels: map[string]string{"role": "gpu"}},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}},
	}
	nodeGroup := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-workers"},
		Spec:       v1alpha1.KernelCacheNodeGroupSpec{NodeSelector: map[string]string{"role": "gpu"}},
	}
	kernelCache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team"},
		Spec:       v1alpha1.KernelCacheSpec{NodeGroupRef: &corev1.LocalObjectReference{Name: nodeGroup.Name}},
	}
	kernelCacheNode := &v1alpha1.KernelCacheNode{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
			"team/cache": {State: v1alpha1.KernelCacheNodePreparationStateReady},
		}},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(node).WithObjects(node, nodeGroup, kernelCache).Build()
	reconciler := &KernelCacheNodeReconciler{Client: k8sClient, NodeName: nodeName}

	if _, _, err := reconciler.discoverCaches(t.Context(), kernelCacheNode); err != nil {
		t.Fatal(err)
	}
	if len(kernelCacheNode.Status.CacheStatus) != 0 {
		t.Fatalf("expected cache status to be removed for a NotReady node, got %#v", kernelCacheNode.Status.CacheStatus)
	}

	node.Status.Conditions[0].Status = corev1.ConditionTrue
	if err := k8sClient.Status().Update(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	storedNode := &corev1.Node{}
	if err := k8sClient.Get(t.Context(), client.ObjectKey{Name: nodeName}, storedNode); err != nil {
		t.Fatal(err)
	}
	if storedNode.Status.Conditions[0].Status != corev1.ConditionTrue {
		t.Fatalf("expected stored node to be Ready, got %q", storedNode.Status.Conditions[0].Status)
	}
	_, changed, err := reconciler.discoverCaches(t.Context(), kernelCacheNode)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected cache discovery to report a restored CacheStatus entry")
	}
	if _, ok := kernelCacheNode.Status.CacheStatus["team/cache"]; !ok {
		t.Fatal("expected cache status to be restored for a Ready node")
	}
}

// Retry KCN status updates after a resource version conflict.
func TestReconcileRetriesStatusConflict(t *testing.T) {
	const (
		nodeName       = "gpu-node-1"
		cacheNamespace = "team"
		cacheName      = "cache"
		imageReference = "registry.example.com/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		v1alpha1.AddToScheme,
		corev1.AddToScheme,
		batchv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: map[string]string{"kubernetes.io/hostname": nodeName},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type:   corev1.NodeReady,
			Status: corev1.ConditionTrue,
		}}},
	}
	nodeGroup := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-workers"},
		Spec: v1alpha1.KernelCacheNodeGroupSpec{
			NodeSelector: map[string]string{"kubernetes.io/hostname": nodeName},
		},
	}
	kernelCache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: cacheName, Namespace: cacheNamespace},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: nodeGroup.Name},
			Artifact:     v1alpha1.KernelCacheArtifact{ImageReference: imageReference},
		},
	}
	kernelCacheNode := &v1alpha1.KernelCacheNode{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
			cacheNamespace + "/" + cacheName: {
				KernelCacheRef: v1alpha1.NamespacedName{Namespace: cacheNamespace, Name: cacheName},
				ImageReference: imageReference,
				State:          v1alpha1.KernelCacheNodePreparationStatePending,
			},
		}},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prefetch-job",
			Namespace: "jobs",
			Labels: map[string]string{
				kernelCacheNameLabel:      cacheName,
				kernelCacheNamespaceLabel: cacheNamespace,
				kernelCacheNodeLabel:      nodeName,
			},
		},
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{Name: "kernel-cache", VolumeSource: corev1.VolumeSource{
				Image: &corev1.ImageVolumeSource{Reference: imageReference},
			}}},
		}}},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobComplete, Status: corev1.ConditionTrue,
		}}},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"jobNamespace":"jobs"}`},
	}

	baseClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(kernelCacheNode).
		WithObjects(node, nodeGroup, kernelCache, kernelCacheNode, job, configMap).
		Build()
	conflictClient := &conflictStatusClient{Client: baseClient, conflicts: 1}
	reconciler := &KernelCacheNodeReconciler{Client: conflictClient, NodeName: nodeName}

	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: nodeName}}); err != nil {
		t.Fatalf("expected status conflict to be retried: %v", err)
	}
	if conflictClient.statusUpdates != 2 {
		t.Fatalf("expected one conflict and one successful status update, got %d updates", conflictClient.statusUpdates)
	}

	updated := &v1alpha1.KernelCacheNode{}
	if err := baseClient.Get(t.Context(), client.ObjectKey{Name: nodeName}, updated); err != nil {
		t.Fatal(err)
	}
	if got := updated.Status.CacheStatus[cacheNamespace+"/"+cacheName].State; got != v1alpha1.KernelCacheNodePreparationStateReady {
		t.Fatalf("expected cache state Ready, got %q", got)
	}
}

func TestReconcileKeepsReadyCacheAfterJobDeletion(t *testing.T) {
	const (
		nodeName       = "gpu-node-1"
		cacheNamespace = "team"
		cacheName      = "cache"
		imageReference = "registry.example.com/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		v1alpha1.AddToScheme,
		corev1.AddToScheme,
		batchv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: map[string]string{"kubernetes.io/hostname": nodeName},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: corev1.ConditionTrue,
		}}},
	}
	nodeGroup := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-workers"},
		Spec: v1alpha1.KernelCacheNodeGroupSpec{
			NodeSelector: map[string]string{"kubernetes.io/hostname": nodeName},
		},
	}
	kernelCache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: cacheName, Namespace: cacheNamespace},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: nodeGroup.Name},
			Artifact:     v1alpha1.KernelCacheArtifact{ImageReference: imageReference},
		},
	}
	kernelCacheNode := &v1alpha1.KernelCacheNode{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
			cacheNamespace + "/" + cacheName: {
				KernelCacheRef: v1alpha1.NamespacedName{Namespace: cacheNamespace, Name: cacheName},
				ImageReference: imageReference,
				State:          v1alpha1.KernelCacheNodePreparationStateReady,
			},
		}},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"jobNamespace":"jobs"}`},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(kernelCacheNode).
		WithObjects(node, nodeGroup, kernelCache, kernelCacheNode, configMap).
		Build()
	reconciler := &KernelCacheNodeReconciler{Client: k8sClient, NodeName: nodeName}

	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: nodeName}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	updated := &v1alpha1.KernelCacheNode{}
	if err := k8sClient.Get(t.Context(), client.ObjectKey{Name: nodeName}, updated); err != nil {
		t.Fatal(err)
	}
	if got := updated.Status.CacheStatus[cacheNamespace+"/"+cacheName].State; got != v1alpha1.KernelCacheNodePreparationStateReady {
		t.Fatalf("expected cache state Ready after Job deletion, got %q", got)
	}

	if err := reconciler.validateNodeImages(t.Context()); err != nil {
		t.Fatalf("image validation failed: %v", err)
	}
	if err := k8sClient.Get(t.Context(), client.ObjectKey{Name: nodeName}, updated); err != nil {
		t.Fatal(err)
	}
	if got := updated.Status.CacheStatus[cacheNamespace+"/"+cacheName].State; got != v1alpha1.KernelCacheNodePreparationStatePending {
		t.Fatalf("expected periodic image validation to mark cache Pending, got %q", got)
	}
}

func TestReconcileValidatesNewCacheStatusFromNodeImage(t *testing.T) {
	const (
		nodeName       = "gpu-node-1"
		cacheNamespace = "team"
		cacheName      = "cache"
		imageReference = "registry.example.com/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, corev1.AddToScheme, batchv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Labels: map[string]string{"kubernetes.io/hostname": nodeName}},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Images:     []corev1.ContainerImage{{Names: []string{imageReference}}},
		},
	}
	nodeGroup := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-workers"},
		Spec:       v1alpha1.KernelCacheNodeGroupSpec{NodeSelector: map[string]string{"kubernetes.io/hostname": nodeName}},
	}
	kernelCache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: cacheName, Namespace: cacheNamespace},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: nodeGroup.Name},
			Artifact:     v1alpha1.KernelCacheArtifact{ImageReference: imageReference},
		},
	}
	kernelCacheNode := &v1alpha1.KernelCacheNode{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"jobNamespace":"jobs"}`},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(kernelCacheNode).
		WithObjects(node, nodeGroup, kernelCache, kernelCacheNode, configMap).
		Build()
	reconciler := &KernelCacheNodeReconciler{Client: k8sClient, NodeName: nodeName}

	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: nodeName}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	updated := &v1alpha1.KernelCacheNode{}
	if err := k8sClient.Get(t.Context(), client.ObjectKey{Name: nodeName}, updated); err != nil {
		t.Fatal(err)
	}
	if got := updated.Status.CacheStatus[cacheNamespace+"/"+cacheName].State; got != v1alpha1.KernelCacheNodePreparationStateReady {
		t.Fatalf("expected cache state Ready, got %q", got)
	}
	jobs := &batchv1.JobList{}
	if err := k8sClient.List(t.Context(), jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("expected no prefetch Job for an image already on the node, got %d", len(jobs.Items))
	}
}

type conflictStatusClient struct {
	client.Client
	conflicts     int
	statusUpdates int
}

func (c *conflictStatusClient) Status() client.SubResourceWriter {
	return &conflictStatusWriter{SubResourceWriter: c.Client.Status(), client: c}
}

type conflictStatusWriter struct {
	client.SubResourceWriter
	client *conflictStatusClient
}

func (w *conflictStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	w.client.statusUpdates++
	if w.client.conflicts > 0 {
		w.client.conflicts--
		return apierrors.NewConflict(
			schema.GroupResource{Group: "serving.kserve.io", Resource: "kernelcachenodes"},
			obj.GetName(),
			errors.New("injected conflict"),
		)
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

// A deleted Job must not invalidate a cache that was already Ready.
func TestCachePreparationStateAfterJobCleanup(t *testing.T) {
	tests := []struct {
		name        string
		job         *batchv1.Job
		state       v1alpha1.KernelCacheNodePreparationState
		wantState   v1alpha1.KernelCacheNodePreparationState
		wantMessage string
	}{
		{
			name: "completed Job wins over missing Node image",
			job: &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
				Type: batchv1.JobComplete, Status: corev1.ConditionTrue,
			}}}},
			state:       v1alpha1.KernelCacheNodePreparationStatePending,
			wantState:   v1alpha1.KernelCacheNodePreparationStateReady,
			wantMessage: "OCI artifact was prefetched on the node",
		},
		{
			name:        "ready cache remains ready after Job cleanup",
			state:       v1alpha1.KernelCacheNodePreparationStateReady,
			wantState:   v1alpha1.KernelCacheNodePreparationStateReady,
			wantMessage: "OCI artifact was prefetched on the node",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, message := cachePreparationState(test.job, v1alpha1.KernelCacheNodeCacheInfo{
				State: test.state,
			}, "")
			if state != test.wantState {
				t.Fatalf("expected state %q, got %q", test.wantState, state)
			}
			if message != test.wantMessage {
				t.Fatalf("expected message %q, got %q", test.wantMessage, message)
			}
		})
	}
}

func TestCacheImageValidationState(t *testing.T) {
	const imageReference = "registry.example.com/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	tests := []struct {
		name        string
		images      []string
		wantState   v1alpha1.KernelCacheNodePreparationState
		wantMessage string
	}{
		{
			name:        "image is present",
			images:      []string{imageReference},
			wantState:   v1alpha1.KernelCacheNodePreparationStateReady,
			wantMessage: "OCI artifact is present on the node",
		},
		{
			name:        "image is missing",
			wantState:   v1alpha1.KernelCacheNodePreparationStatePending,
			wantMessage: missingImageMessage,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := &corev1.Node{Status: corev1.NodeStatus{Images: []corev1.ContainerImage{{Names: test.images}}}}
			state, message := cacheImageValidationState(v1alpha1.KernelCacheNodeCacheInfo{
				ImageReference: imageReference,
			}, node)
			if state != test.wantState {
				t.Fatalf("expected state %q, got %q", test.wantState, state)
			}
			if message != test.wantMessage {
				t.Fatalf("expected message %q, got %q", test.wantMessage, message)
			}
		})
	}
}
