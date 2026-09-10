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

package kernelcache

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	cacheidentity "github.com/kserve/kserve/pkg/kernelcache/identity"
)

func TestRuntimeIdentityInputIncludesSupportedRuntimeFactors(t *testing.T) {
	container := &corev1.Container{
		Image:   "registry.example/vllm:v0.28.0",
		Command: []string{"vllm", "--dtype", "bfloat16", "-tp", "2"},
		Args:    []string{"--max-model-len=4096", "--enforce-eager"},
		Env: []corev1.EnvVar{
			{Name: "VLLM_USE_AOT_COMPILE", Value: "true"},
			{Name: "VLLM_DISABLED_KERNELS", Value: "Marlin, ExLlama"},
			{Name: "VLLM_CACHE_ROOT", Value: "/tmp/vllm"},
			{Name: "VLLM_BATCH_INVARIANT", ValueFrom: &corev1.EnvVarSource{}},
		},
	}

	input := runtimeIdentityInput(container, "hf://org/model")
	require.Equal(t, "2", input.TensorParallelSize)
	require.Equal(t, map[string]string{
		cacheidentity.DTypeFactor:              "bfloat16",
		cacheidentity.TensorParallelSizeFactor: "2",
		cacheidentity.MaxModelLenFactor:        "4096",
		cacheidentity.EnforceEagerFactor:       "true",
		"env.VLLM_USE_AOT_COMPILE":             "true",
		"env.VLLM_DISABLED_KERNELS":            "Marlin,ExLlama",
	}, input.RuntimeFactors)
}

func TestInjectSidecar(t *testing.T) {
	for _, tc := range []struct {
		name              string
		capture           *v1alpha1.KernelCacheCapture
		registryEndpoint  string
		wantContainerPath string
		wantTargetImage   string
		wantCaptureName   string
		wantGeneratedName bool
	}{
		{
			name:              "uses defaults when capture is missing",
			registryEndpoint:  "registry.example:5000",
			wantContainerPath: "/cache/vllm",
			wantTargetImage:   "registry.example:5000/test/kernel-cache-qwen:",
			wantGeneratedName: true,
		},
		{
			name: "uses capture overrides",
			capture: &v1alpha1.KernelCacheCapture{
				ObjectMeta: metav1.ObjectMeta{Name: constants.KernelCacheCaptureName("qwen"), Namespace: "test"},
				Spec: v1alpha1.KernelCacheCaptureSpec{
					SourceRef:   v1alpha1.KernelCacheSourceRef{Kind: "InferenceService", Name: "qwen"},
					CachePaths:  []v1alpha1.KernelCachePath{{ContainerName: "kserve-container", ContainerPath: "/custom/vllm", OCIPath: "vllm"}},
					TargetImage: "registry.example:5000/test/custom:v1",
				},
			},
			registryEndpoint:  "registry.example:5000",
			wantContainerPath: "/custom/vllm",
			wantTargetImage:   "registry.example:5000/test/custom:v1",
			wantCaptureName:   constants.KernelCacheCaptureName("qwen"),
		},
		{
			name: "resolves omitted capture path from runtime environment",
			capture: &v1alpha1.KernelCacheCapture{
				ObjectMeta: metav1.ObjectMeta{Name: constants.KernelCacheCaptureName("qwen"), Namespace: "test"},
				Spec: v1alpha1.KernelCacheCaptureSpec{
					SourceRef:  v1alpha1.KernelCacheSourceRef{Kind: "InferenceService", Name: "qwen"},
					CachePaths: []v1alpha1.KernelCachePath{{ContainerName: "kserve-container", OCIPath: "vllm"}},
				},
			},
			registryEndpoint:  "registry.example:5000",
			wantContainerPath: "/cache/vllm",
			wantTargetImage:   "registry.example:5000/test/kernel-cache-qwen:",
			wantCaptureName:   constants.KernelCacheCaptureName("qwen"),
		},
		{
			name: "uses default paths when capture only sets target image",
			capture: &v1alpha1.KernelCacheCapture{
				ObjectMeta: metav1.ObjectMeta{Name: constants.KernelCacheCaptureName("qwen"), Namespace: "test"},
				Spec: v1alpha1.KernelCacheCaptureSpec{
					SourceRef:   v1alpha1.KernelCacheSourceRef{Kind: "InferenceService", Name: "qwen"},
					TargetImage: "registry.example:5000/test/custom:v1",
				},
			},
			wantContainerPath: "/cache/vllm",
			wantTargetImage:   "registry.example:5000/test/custom:v1",
			wantCaptureName:   constants.KernelCacheCaptureName("qwen"),
		},
		{
			name: "uses default target image when capture only sets paths",
			capture: &v1alpha1.KernelCacheCapture{
				ObjectMeta: metav1.ObjectMeta{Name: constants.KernelCacheCaptureName("qwen"), Namespace: "test"},
				Spec: v1alpha1.KernelCacheCaptureSpec{
					SourceRef:  v1alpha1.KernelCacheSourceRef{Kind: "InferenceService", Name: "qwen"},
					CachePaths: []v1alpha1.KernelCachePath{{ContainerName: "kserve-container", ContainerPath: "/custom/vllm", OCIPath: "vllm"}},
				},
			},
			registryEndpoint:  "registry.example:5000",
			wantContainerPath: "/custom/vllm",
			wantTargetImage:   "registry.example:5000/test/kernel-cache-qwen:",
			wantCaptureName:   constants.KernelCacheCaptureName("qwen"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, v1beta1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tc.capture != nil {
				builder.WithObjects(tc.capture)
			}
			mutator := &PodMutator{Client: builder.Build()}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Labels: map[string]string{constants.InferenceServicePodLabelKey: "qwen"}}, Spec: corev1.PodSpec{ServiceAccountName: "runtime", Containers: []corev1.Container{{Name: "kserve-container", Env: []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/cache/vllm"}}, ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt(8080)}}, PeriodSeconds: 5, TimeoutSeconds: 2}}}}}
			cfg := &v1beta1.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: v1beta1.KernelCacheRegistryConfig{Endpoint: tc.registryEndpoint}}
			originalConfig := cfg.DeepCopy()
			originalEnv := append([]corev1.EnvVar(nil), pod.Spec.Containers[0].Env...)
			require.NoError(t, mutator.injectSidecar(context.Background(), pod, cfg))
			require.Equal(t, originalConfig, cfg)
			require.Equal(t, originalEnv, pod.Spec.Containers[0].Env)
			require.Equal(t, "runtime", pod.Spec.ServiceAccountName)
			require.Len(t, pod.Spec.Containers, 2)
			require.Equal(t, cfg.MCVImage, pod.Spec.Containers[1].Image)
			require.Equal(t, tc.wantContainerPath, pod.Spec.Containers[0].VolumeMounts[0].MountPath)
			require.True(t, strings.HasPrefix(pod.Spec.Containers[1].Env[2].Value, tc.wantTargetImage))
			captureName := containerEnvValue(pod.Spec.Containers[1], "MCV_CAPTURE_NAME")
			if tc.wantGeneratedName {
				require.True(t, strings.HasPrefix(captureName, constants.KernelCacheCaptureName("qwen")+"-"))
			} else {
				require.Equal(t, tc.wantCaptureName, captureName)
			}
			var cachePaths []v1alpha1.KernelCachePath
			require.NoError(t, json.Unmarshal([]byte(containerEnvValue(pod.Spec.Containers[1], "MCV_CACHE_PATHS")), &cachePaths))
			require.Equal(t, tc.wantContainerPath, cachePaths[0].ContainerPath)
			require.NoError(t, mutator.injectSidecar(context.Background(), pod, cfg))
			require.Len(t, pod.Spec.Containers, 2)
		})
	}
}

func TestInjectSidecarUsesNewCaptureNameAfterCompletedCapture(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	fixedName := constants.KernelCacheCaptureName("qwen")
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: fixedName, Namespace: "test"},
		Spec: v1alpha1.KernelCacheCaptureSpec{
			SourceRef:   v1alpha1.KernelCacheSourceRef{Kind: "InferenceService", Name: "qwen"},
			TargetImage: "registry.example:5000/test/custom:v1",
		},
		Status: v1alpha1.KernelCacheCaptureStatus{
			Phase:          v1alpha1.KernelCacheCapturePhaseComplete,
			KernelCacheRef: &v1alpha1.NamespacedName{Namespace: "test", Name: "existing-cache"},
		},
	}
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture).Build()}
	pod := sidecarTestPod()
	cfg := &v1beta1.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: v1beta1.KernelCacheRegistryConfig{Endpoint: "registry.example:5000"}}

	require.NoError(t, mutator.injectSidecar(context.Background(), pod, cfg))
	captureName := containerEnvValue(pod.Spec.Containers[1], "MCV_CAPTURE_NAME")
	require.NotEqual(t, fixedName, captureName)
	require.True(t, strings.HasPrefix(captureName, fixedName+"-"), captureName)
	require.Equal(t, capture.Spec.TargetImage, containerEnvValue(pod.Spec.Containers[1], "MCV_TARGET_IMAGE"))
}

func TestInjectSidecarUsesNewCaptureNameAfterUnchangedCapture(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	fixedName := constants.KernelCacheCaptureName("qwen")
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: fixedName, Namespace: "test"},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: "InferenceService", Name: "qwen",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{Phase: v1alpha1.KernelCacheCapturePhaseUnchanged},
	}
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture).Build()}
	pod := sidecarTestPod()
	cfg := &v1beta1.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: v1beta1.KernelCacheRegistryConfig{Endpoint: "registry.example:5000"}}

	require.NoError(t, mutator.injectSidecar(context.Background(), pod, cfg))
	captureName := containerEnvValue(pod.Spec.Containers[1], "MCV_CAPTURE_NAME")
	require.NotEqual(t, fixedName, captureName)
	require.True(t, strings.HasPrefix(captureName, fixedName+"-"), captureName)
}

func TestInjectSidecarReusesInProgressCapture(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-kernelcache-capture-session", Namespace: "test"},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: "InferenceService", Name: "qwen",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{Phase: v1alpha1.KernelCacheCapturePhaseCapturing},
	}
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture).Build()}
	pod := sidecarTestPod()
	cfg := &v1beta1.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: v1beta1.KernelCacheRegistryConfig{Endpoint: "registry.example:5000"}}

	require.NoError(t, mutator.injectSidecar(context.Background(), pod, cfg))
	require.Equal(t, capture.Name, containerEnvValue(pod.Spec.Containers[1], "MCV_CAPTURE_NAME"))
}

func sidecarTestPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test", Labels: map[string]string{constants.InferenceServicePodLabelKey: "qwen"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "kserve-container",
			Env:  []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/cache/vllm"}},
			ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt(8080)},
			}},
		}}},
	}
}

func containerEnvValue(container corev1.Container, name string) string {
	for _, env := range container.Env {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

func TestReadyCacheMountAndCaptureCoexist(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: constants.KernelCacheCaptureName("qwen"), Namespace: "test"},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: "InferenceService", Name: "qwen",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{KernelCacheRef: &v1alpha1.NamespacedName{Name: "cache", Namespace: "test"}},
	}
	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "test"},
		Spec: v1alpha1.KernelCacheSpec{
			MountType: v1alpha1.KernelCacheMountTypeOCI,
			Artifact: v1alpha1.KernelCacheArtifact{
				ImageReference: "registry.example/cache@sha256:" + strings.Repeat("a", 64),
				CachePaths:     []v1alpha1.KernelCachePath{{ContainerName: "kserve-container", ContainerPath: "/cache/vllm", OCIPath: "io.vllm.cache"}},
			},
		},
		Status: v1alpha1.KernelCacheStatus{State: v1alpha1.KernelCacheStateReady},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
			Labels:    map[string]string{constants.InferenceServicePodLabelKey: "qwen"},
			Annotations: map[string]string{
				constants.StorageInitializerSourceUriInternalAnnotationKey: "hf://Qwen/Qwen3-0.6B",
			},
		},
		Spec: corev1.PodSpec{ServiceAccountName: "runtime", Containers: []corev1.Container{{
			Name: "kserve-container", Image: "registry.example/vllm:v0.10", Env: []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/cache/vllm"}},
			ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt(8080)}}},
		}}},
	}
	cacheIdentity, err := identityForPod(pod)
	require.NoError(t, err)
	cache.Spec.Artifact.Identity = cacheIdentity
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture, cache).Build()}
	cfg := &v1beta1.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: v1beta1.KernelCacheRegistryConfig{Endpoint: "registry.example"}}
	found, err := mutator.injectKernelCacheConsumer(context.Background(), pod, cfg)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, pod.Spec.Containers, 1)
	require.Len(t, pod.Spec.InitContainers, 1)
	require.NoError(t, mutator.injectSidecar(context.Background(), pod, cfg))
	require.Len(t, pod.Spec.Containers, 2)
	require.Equal(t, "runtime", pod.Spec.ServiceAccountName)
	require.Contains(t, pod.Spec.Containers[1].VolumeMounts, corev1.VolumeMount{Name: kernelCacheSourceVolumeName, MountPath: kernelCacheSourceMountPath, ReadOnly: true})
	require.Contains(t, pod.Spec.Containers[1].Env, corev1.EnvVar{Name: "MCV_CACHE_LINK_ROOT", Value: kernelCacheSourceMountPath})
	require.NoError(t, mutator.injectSidecar(context.Background(), pod, cfg))
	require.Len(t, pod.Spec.Containers, 2)
}

func TestSidecarManifestsAndExistingMount(t *testing.T) {
	cfg := &v1beta1.KernelCacheConfig{
		MCVImage: "example/mcv:test", TargetImage: "registry.example/cache:capture-id",
		CachePaths:         []v1alpha1.KernelCachePath{{ContainerName: "main", ContainerPath: "/cache", OCIPath: "vllm"}},
		ReadinessEnv:       []corev1.EnvVar{{Name: "MCV_READINESS_PROBE_PORT", Value: "8080"}},
		ReporterSecretName: "mcv-reporter-capture-id",
	}
	manifests, err := getSidecarManifests(cfg)
	require.NoError(t, err)
	require.Len(t, manifests.Containers, 1)
	require.Len(t, manifests.Volumes, 2)
	require.Equal(t, cfg.TargetImage, manifests.Containers[0].Env[2].Value)
	require.Contains(t, manifests.Containers[0].Env, cfg.ReadinessEnv[0])

	podSpec := corev1.PodSpec{
		Volumes:    []corev1.Volume{{Name: "existing", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
		Containers: []corev1.Container{{Name: "main", VolumeMounts: []corev1.VolumeMount{{Name: "existing", MountPath: "/cache", SubPath: "vllm"}}}},
	}
	require.NoError(t, addCacheMount(&podSpec, &manifests.Containers[0], 0, cfg.CachePaths[0], manifests.Volumes[0]))
	require.Len(t, podSpec.Volumes, 1)
	require.Equal(t, corev1.VolumeMount{Name: "existing", MountPath: "/workspace/cache/0", SubPath: "vllm"}, manifests.Containers[0].VolumeMounts[0])
}

func TestDefaultCachePaths(t *testing.T) {
	for _, tc := range []struct {
		name         string
		container    corev1.Container
		wantVLLMPath string
	}{
		{
			name: "inference service container uses environment values",
			container: corev1.Container{
				Name: constants.InferenceServiceContainerName,
				Env: []corev1.EnvVar{
					{Name: "VLLM_CACHE_ROOT", Value: "/cache/vllm"},
					{Name: "TRITON_CACHE_DIR", Value: "/cache/triton"},
				},
			},
			wantVLLMPath: "/cache/vllm",
		},
		{
			name: "llm inference service container uses fallbacks",
			container: corev1.Container{
				Name: constants.LLMInferenceServiceContainerName,
			},
			wantVLLMPath: "/root/.cache/vllm",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, err := defaultCachePaths(&tc.container)
			require.NoError(t, err)
			require.Equal(t, []v1alpha1.KernelCachePath{
				{ContainerName: tc.container.Name, ContainerPath: tc.wantVLLMPath, OCIPath: "io.vllm.cache"},
			}, paths)
		})
	}
}
