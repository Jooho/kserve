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
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

func TestKernelCacheNodeReconcilerConfiguresAgentDaemonSetFromNodeGroups(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	h100 := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "h100"},
		Spec: v1alpha1.KernelCacheNodeGroupSpec{
			NodeSelector: map[string]string{"nvidia.com/gpu.product": "NVIDIA-H100"},
			Tolerations:  []corev1.Toleration{{Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}},
		},
	}
	l40s := &v1alpha1.KernelCacheNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "l40s"},
		Spec: v1alpha1.KernelCacheNodeGroupSpec{
			NodeSelector: map[string]string{"nvidia.com/gpu.product": "NVIDIA-L40S"},
			Tolerations:  []corev1.Toleration{{Key: "workload", Operator: corev1.TolerationOpEqual, Value: "gpu", Effect: corev1.TaintEffectNoSchedule}},
		},
	}
	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kserve", Name: kernelCacheNodeAgentDaemonSetName},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			NodeSelector: map[string]string{"kserve/localmodel": "worker"},
		}}},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(h100, l40s, daemonSet).Build()
	reconciler := &KernelCacheNodeReconciler{Client: k8sClient}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: h100.Name}})
	require.NoError(t, err)

	updated := &appsv1.DaemonSet{}
	require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(daemonSet), updated))
	require.Nil(t, updated.Spec.Template.Spec.NodeSelector)
	terms := updated.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	require.ElementsMatch(t, []corev1.NodeSelectorTerm{
		{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "nvidia.com/gpu.product", Operator: corev1.NodeSelectorOpIn, Values: []string{"NVIDIA-H100"}}}},
		{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "nvidia.com/gpu.product", Operator: corev1.NodeSelectorOpIn, Values: []string{"NVIDIA-L40S"}}}},
	}, terms)
	require.ElementsMatch(t, append(h100.Spec.Tolerations, l40s.Spec.Tolerations...), updated.Spec.Template.Spec.Tolerations)

	require.NoError(t, k8sClient.Delete(context.Background(), l40s))
	_, err = reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: l40s.Name}})
	require.NoError(t, err)
	require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(daemonSet), updated))
	require.Equal(t, []corev1.NodeSelectorTerm{{
		MatchExpressions: []corev1.NodeSelectorRequirement{{
			Key: "nvidia.com/gpu.product", Operator: corev1.NodeSelectorOpIn, Values: []string{"NVIDIA-H100"},
		}},
	}}, updated.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms)
	require.Equal(t, h100.Spec.Tolerations, updated.Spec.Template.Spec.Tolerations)
}

func TestAgentDaemonSetDoesNotScheduleWithoutNodeGroups(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	daemonSet := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Namespace: "kserve", Name: kernelCacheNodeAgentDaemonSetName}}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemonSet).Build()
	reconciler := &KernelCacheNodeReconciler{Client: k8sClient}

	require.NoError(t, reconciler.reconcileAgentDaemonSet(context.Background()))
	updated := &appsv1.DaemonSet{}
	require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(daemonSet), updated))
	terms := updated.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	require.Equal(t, []corev1.NodeSelectorTerm{{
		MatchExpressions: []corev1.NodeSelectorRequirement{{
			Key: kernelCacheNodeAgentDisabledKey, Operator: corev1.NodeSelectorOpIn, Values: []string{kernelCacheNodeAgentDisabledValue},
		}},
	}}, terms)
}
