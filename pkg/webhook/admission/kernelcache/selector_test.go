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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/constants"
	cacheidentity "github.com/kserve/kserve/pkg/kernelcache/identity"
)

func TestSelectKernelCache(t *testing.T) {
	modelHash := "sha256:" + strings.Repeat("a", 64)
	digestImage := "registry.example/vllm@sha256:" + strings.Repeat("b", 64)
	pod := selectionPod(digestImage)
	requestedInput := cacheidentity.Input{
		Namespace:    pod.Namespace,
		WorkloadName: "first",
		RuntimeImage: digestImage,
		ModelURIHash: modelHash,
		CommandHash:  "sha256:" + strings.Repeat("c", 64),
		ArgsHash:     "sha256:" + strings.Repeat("d", 64),
	}
	requested := requireFullIdentity(t, requestedInput)

	workloadCache := readyCache("workload", requested)
	compatibleInput := requestedInput
	compatibleInput.WorkloadName = "other"
	compatibleInput.CommandHash = "sha256:" + strings.Repeat("e", 64)
	compatibleInput.ArgsHash = "sha256:" + strings.Repeat("f", 64)
	compatibleIdentity := requireFullIdentity(t, compatibleInput)
	compatibleCache := readyCache("compatible", compatibleIdentity)

	selection := selectKernelCache(pod, requested, []v1alpha1.KernelCache{compatibleCache, workloadCache}, "")
	require.NotNil(t, selection)
	require.Equal(t, "workload", selection.cache.Name)
	require.Equal(t, kernelCacheMatchWorkload, selection.matchType)

	taggedPod := selectionPod("registry.example/vllm:v0.10")
	taggedIdentity := requireIdentity(t, taggedPod.Namespace, "first", taggedPod.Spec.Containers[0].Image, modelHash)
	taggedCache := readyCache("tagged", taggedIdentity)
	selection = selectKernelCache(taggedPod, taggedIdentity, []v1alpha1.KernelCache{taggedCache}, "")
	require.NotNil(t, selection)
	require.Equal(t, kernelCacheMatchCompatibility, selection.matchType)

	latestPod := selectionPod("registry.example/vllm:latest")
	latestIdentity := requireIdentity(t, latestPod.Namespace, "first", latestPod.Spec.Containers[0].Image, modelHash)
	require.Nil(t, selectKernelCache(latestPod, latestIdentity, []v1alpha1.KernelCache{taggedCache}, ""))
}

func TestSelectKernelCacheUsesEqualWeightScoring(t *testing.T) {
	pod := selectionPod("registry.example/vllm:v0.10")
	requested := requireFullIdentity(t, cacheidentity.Input{
		Namespace:          pod.Namespace,
		WorkloadName:       "first",
		RuntimeImage:       pod.Spec.Containers[0].Image,
		ModelURIHash:       "sha256:" + strings.Repeat("a", 64),
		TensorParallelSize: "2",
		CommandHash:        "sha256:" + strings.Repeat("c", 64),
		ArgsHash:           "sha256:" + strings.Repeat("d", 64),
		RuntimeFactors: map[string]string{
			cacheidentity.DTypeFactor:       "bfloat16",
			cacheidentity.MaxModelLenFactor: "4096",
		},
	})

	modelMismatch := requestedInput(requested)
	modelMismatch.ModelURIHash = "sha256:" + strings.Repeat("e", 64)
	candidate := readyCache("weighted", requireFullIdentity(t, modelMismatch))
	selection := selectKernelCache(pod, requested, []v1alpha1.KernelCache{candidate}, "")
	require.NotNil(t, selection)
	require.Equal(t, kernelCacheMatchWeighted, selection.matchType)
	require.Equal(t, 80, selection.score)

	twoMismatches := modelMismatch
	twoMismatches.RuntimeImage = "registry.example/vllm:v0.11"
	rejected := readyCache("below-threshold", requireFullIdentity(t, twoMismatches))
	require.Nil(t, selectKernelCache(pod, requested, []v1alpha1.KernelCache{rejected}, ""))
}

func TestSelectKernelCacheRequiresCompatibilityForWorkloadMatch(t *testing.T) {
	pod := selectionPod("registry.example/vllm@sha256:" + strings.Repeat("b", 64))
	requested := requireFullIdentity(t, cacheidentity.Input{
		Namespace:    pod.Namespace,
		WorkloadName: "first",
		RuntimeImage: pod.Spec.Containers[0].Image,
		ModelURIHash: "sha256:" + strings.Repeat("a", 64),
		RuntimeFactors: map[string]string{
			cacheidentity.DTypeFactor: "bfloat16",
		},
	})

	changed := requestedInput(requested)
	changed.RuntimeFactors = map[string]string{cacheidentity.DTypeFactor: "float16"}
	cache := readyCache("same-workload-but-incompatible", requireFullIdentity(t, changed))

	selection := selectKernelCache(pod, requested, []v1alpha1.KernelCache{cache}, "")
	require.Nil(t, selection)
}

func TestWeightedFactorScoreUsesOnlyCompatibilityFactors(t *testing.T) {
	requested := map[string]string{
		cacheidentity.NamespaceFactor:    "team-a",
		cacheidentity.WorkloadNameFactor: "model-a",
		cacheidentity.CommandHashFactor:  "sha256:command-a",
		cacheidentity.ArgsHashFactor:     "sha256:args-a",
		cacheidentity.RuntimeImageFactor: "registry.example/vllm:v0.28.0",
		cacheidentity.ModelURIHashFactor: "sha256:model-a",
		cacheidentity.DTypeFactor:        "bfloat16",
	}
	candidate := map[string]string{
		cacheidentity.NamespaceFactor:    "team-b",
		cacheidentity.WorkloadNameFactor: "model-b",
		cacheidentity.CommandHashFactor:  "sha256:command-b",
		cacheidentity.ArgsHashFactor:     "sha256:args-b",
		cacheidentity.RuntimeImageFactor: "registry.example/vllm:v0.28.0",
		cacheidentity.ModelURIHashFactor: "sha256:model-a",
		cacheidentity.DTypeFactor:        "bfloat16",
	}

	score, acceptable := weightedFactorScore(requested, candidate)
	require.True(t, acceptable)
	require.Equal(t, 100, score)
}

func TestFindKernelCacheUsesExactMatchWhenCaptureReferenceIsStale(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	pod := selectionPod("registry.example/vllm:v0.10")
	pod.Annotations = map[string]string{
		constants.StorageInitializerSourceUriInternalAnnotationKey: "hf://Qwen/Qwen3-0.6B",
	}
	requested, err := identityForPod(pod)
	require.NoError(t, err)
	cache := readyCache("matching", requested)
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: "first-capture", Namespace: pod.Namespace},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: "InferenceService",
			Name: "first",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{
			KernelCacheRef: &v1alpha1.NamespacedName{Name: "missing", Namespace: pod.Namespace},
		},
	}
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture, &cache).Build()}

	selected, found, err := mutator.findKernelCache(context.Background(), pod)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, cache.Name, selected.Name)
}

func TestCacheCanMountToPodResolvesOptionalContainerPath(t *testing.T) {
	pod := selectionPod("registry.example/vllm:v0.10")
	pod.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/runtime/cache"}}
	cache := readyCache("manual", v1alpha1.KernelCacheIdentity{})
	cache.Spec.Artifact.CachePaths[0].ContainerName = ""
	cache.Spec.Artifact.CachePaths[0].ContainerPath = ""

	require.True(t, cacheCanMountToPod(&cache, pod))

	pod.Spec.Containers[0].Env[0] = corev1.EnvVar{
		Name:      "VLLM_CACHE_ROOT",
		ValueFrom: &corev1.EnvVarSource{},
	}
	require.False(t, cacheCanMountToPod(&cache, pod))
}

func selectionPod(image string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "team", Labels: map[string]string{constants.InferenceServicePodLabelKey: "first"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: constants.InferenceServiceContainerName, Image: image}}},
	}
}

func requireIdentity(t *testing.T, namespace, workloadName, image, modelHash string) v1alpha1.KernelCacheIdentity {
	t.Helper()
	return requireFullIdentity(t, cacheidentity.Input{
		Namespace: namespace, WorkloadName: workloadName, RuntimeImage: image, ModelURIHash: modelHash,
	})
}

func requireFullIdentity(t *testing.T, input cacheidentity.Input) v1alpha1.KernelCacheIdentity {
	t.Helper()
	result, err := cacheidentity.Build(input)
	require.NoError(t, err)
	return result
}

func requestedInput(identity v1alpha1.KernelCacheIdentity) cacheidentity.Input {
	return cacheidentity.Input{
		Namespace:          identity.Factors[cacheidentity.NamespaceFactor],
		WorkloadName:       identity.Factors[cacheidentity.WorkloadNameFactor],
		RuntimeImage:       identity.Factors[cacheidentity.RuntimeImageFactor],
		ModelURIHash:       identity.Factors[cacheidentity.ModelURIHashFactor],
		TensorParallelSize: identity.Factors[cacheidentity.TensorParallelSizeFactor],
		CommandHash:        identity.Factors[cacheidentity.CommandHashFactor],
		ArgsHash:           identity.Factors[cacheidentity.ArgsHashFactor],
		RuntimeFactors:     runtimeFactors(identity.Factors),
	}
}

func runtimeFactors(factors map[string]string) map[string]string {
	runtime := make(map[string]string)
	for key, value := range factors {
		if cacheidentity.IsCompatibilityFactor(key) && key != cacheidentity.RuntimeImageFactor && key != cacheidentity.ModelURIHashFactor && key != cacheidentity.TensorParallelSizeFactor {
			runtime[key] = value
		}
	}
	return runtime
}

func readyCache(name string, cacheIdentity v1alpha1.KernelCacheIdentity) v1alpha1.KernelCache {
	return v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{
			MountType: v1alpha1.KernelCacheMountTypeOCI,
			Artifact: v1alpha1.KernelCacheArtifact{
				ImageReference: "registry.example/cache@sha256:" + strings.Repeat("d", 64),
				CachePaths:     []v1alpha1.KernelCachePath{{ContainerName: constants.InferenceServiceContainerName, ContainerPath: "/cache", OCIPath: "io.vllm.cache"}},
				Identity:       cacheIdentity,
			},
		},
		Status: v1alpha1.KernelCacheStatus{State: v1alpha1.KernelCacheStateReady},
	}
}
