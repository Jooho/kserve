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

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/constants"
)

func TestNodeChangeRequeuesOnlyMatchingKernelCaches(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	gpuGroup := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-workers"},
		Spec:       v1alpha1.KernelCacheNodeGroupSpec{NodeSelector: map[string]string{"role": "gpu"}},
	}
	cpuGroup := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "cpu-workers"},
		Spec:       v1alpha1.KernelCacheNodeGroupSpec{NodeSelector: map[string]string{"role": "cpu"}},
	}
	gpuCache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-cache", Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: gpuGroup.Name},
		},
	}
	cpuCache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cpu-cache", Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{
			NodeGroupRef: &corev1.LocalObjectReference{Name: cpuGroup.Name},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gpuGroup, cpuGroup, gpuCache, cpuCache).Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}

	requests := reconciler.enqueueKCsOnNodeChange(t.Context(), &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-node", Labels: map[string]string{"role": "gpu"}},
	})

	require.Equal(t, []types.NamespacedName{{Namespace: "team", Name: "gpu-cache"}}, reconcileRequestNames(requests))
}

func TestKernelCacheNodePredicate(t *testing.T) {
	pred := kernelCacheNodePredicate()
	newNode := func(labels map[string]string, ready bool) *corev1.Node {
		status := corev1.ConditionFalse
		if ready {
			status = corev1.ConditionTrue
		}
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
				Type: corev1.NodeReady, Status: status,
			}}},
		}
	}

	tests := []struct {
		name string
		old  *corev1.Node
		new  *corev1.Node
		want bool
	}{
		{name: "unrelated update", old: newNode(map[string]string{"role": "gpu"}, true), new: newNode(map[string]string{"role": "gpu"}, true), want: false},
		{name: "label change", old: newNode(map[string]string{"role": "gpu"}, true), new: newNode(map[string]string{"role": "cpu"}, true), want: true},
		{name: "readiness change", old: newNode(map[string]string{"role": "gpu"}, false), new: newNode(map[string]string{"role": "gpu"}, true), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := pred.Update(event.UpdateEvent{ObjectOld: test.old, ObjectNew: test.new}); got != test.want {
				t.Fatalf("predicate result = %t, want %t", got, test.want)
			}
		})
	}

	node := newNode(map[string]string{"role": "gpu"}, true)
	require.True(t, pred.Create(event.CreateEvent{Object: node}))
	require.True(t, pred.Delete(event.DeleteEvent{Object: node}))
	require.False(t, pred.Generic(event.GenericEvent{Object: node}))
}

func TestConfigMapChangeRequeuesKernelCaches(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	cache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team"}}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cache).Build()
	reconciler := &KernelCacheReconciler{Client: k8sClient}

	requests := reconciler.enqueueKCsOnConfigChange(t.Context(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.InferenceServiceConfigMapName,
			Namespace: constants.KServeNamespace,
		},
	})
	require.Equal(t, []types.NamespacedName{{Namespace: "team", Name: "cache"}}, reconcileRequestNames(requests))

	requests = reconciler.enqueueKCsOnConfigChange(t.Context(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: constants.KServeNamespace},
	})
	require.Empty(t, requests)
}

func reconcileRequestNames(requests []reconcile.Request) []types.NamespacedName {
	names := make([]types.NamespacedName, 0, len(requests))
	for _, request := range requests {
		names = append(names, request.NamespacedName)
	}
	return names
}
