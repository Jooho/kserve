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

package pod

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/constants"
)

func TestKernelCacheInjectorInjectsOCIImageVolume(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	kernelCache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"},
		Spec: v1alpha1.KernelCacheSpec{
			MountType: v1alpha1.KernelCacheMountTypeOCI,
			Artifact: v1alpha1.KernelCacheArtifact{
				ImageReference: "quay.io/example/qwen-cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				CachePaths: []v1alpha1.KernelCachePath{
					{
						ContainerName: "kserve-container",
						ContainerPath: "/root/.cache/vllm",
						OCIPath:       "vllm",
					},
				},
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kernelCache).Build()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen-predictor",
			Namespace: "staging",
			Labels: map[string]string{
				constants.KernelCacheLabel: kernelCache.Name,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: constants.InferenceServiceContainerName}},
		},
	}

	injector := &KernelCacheInjector{client: client}
	require.NoError(t, injector.Inject(context.Background(), pod, v1alpha1.KernelCacheMountTypePVC))

	require.Len(t, pod.Spec.Volumes, 1)
	require.NotNil(t, pod.Spec.Volumes[0].Image)
	require.Equal(t, kernelCache.Spec.Artifact.ImageReference, pod.Spec.Volumes[0].Image.Reference)
	require.Equal(t, corev1.PullIfNotPresent, pod.Spec.Volumes[0].Image.PullPolicy)
	require.Len(t, pod.Spec.Containers[0].VolumeMounts, 1)
	require.Equal(t, "/root/.cache/vllm", pod.Spec.Containers[0].VolumeMounts[0].MountPath)
	require.Equal(t, "vllm", pod.Spec.Containers[0].VolumeMounts[0].SubPath)

	require.NoError(t, injector.Inject(context.Background(), pod, v1alpha1.KernelCacheMountTypePVC))
	require.Len(t, pod.Spec.Volumes, 1)
	require.Len(t, pod.Spec.Containers[0].VolumeMounts, 1)
}

func TestKernelCacheInjectorResolvesOptionalContainerPath(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	kernelCache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"},
		Spec: v1alpha1.KernelCacheSpec{
			MountType: v1alpha1.KernelCacheMountTypeOCI,
			Artifact: v1alpha1.KernelCacheArtifact{
				ImageReference: "quay.io/example/qwen-cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				CachePaths:     []v1alpha1.KernelCachePath{{OCIPath: "vllm"}},
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kernelCache).Build()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen-predictor",
			Namespace: "staging",
			Labels:    map[string]string{constants.KernelCacheLabel: kernelCache.Name},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: constants.InferenceServiceContainerName,
			Env:  []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/runtime/cache"}},
		}}},
	}

	injector := &KernelCacheInjector{client: client}
	require.NoError(t, injector.Inject(context.Background(), pod, v1alpha1.KernelCacheMountTypePVC))
	require.Contains(t, pod.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      kernelCacheVolumeName,
		MountPath: "/runtime/cache",
		ReadOnly:  true,
		SubPath:   "vllm",
	})
}

func TestKernelCacheInjectorIgnoresNonOCIArtifact(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	kernelCache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "staging"},
		Spec: v1alpha1.KernelCacheSpec{
			MountType: v1alpha1.KernelCacheMountTypePVC,
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kernelCache).Build()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "staging",
			Labels:    map[string]string{constants.KernelCacheLabel: kernelCache.Name},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: constants.InferenceServiceContainerName}},
		},
	}

	injector := &KernelCacheInjector{client: client}
	require.NoError(t, injector.Inject(context.Background(), pod, v1alpha1.KernelCacheMountTypeOCI))
	require.Empty(t, pod.Spec.Volumes)
	require.Empty(t, pod.Spec.Containers[0].VolumeMounts)
}
