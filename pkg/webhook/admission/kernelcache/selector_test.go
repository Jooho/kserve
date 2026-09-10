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

// Selects a workload-specific cache before a compatibility-only cache.
func TestFindKernelCacheWorkloadMatch(t *testing.T) {
	digestImage := "registry.example/vllm@sha256:" + strings.Repeat("b", 64)
	pod := selectionPod(digestImage)
	pod.Annotations = map[string]string{
		constants.StorageInitializerSourceUriInternalAnnotationKey: "hf://Qwen/Qwen3-0.6B",
	}
	requested, err := identityForPod(pod)
	require.NoError(t, err)

	workloadCache := readyCache("workload", requested)
	compatibleFactors := requestedFactors(requested)
	compatibleFactors.WorkloadName = "other"
	compatibleFactors.CommandHash = "sha256:" + strings.Repeat("e", 64)
	compatibleFactors.ArgsHash = "sha256:" + strings.Repeat("f", 64)
	compatibleIdentity := requireFullIdentity(t, compatibleFactors)
	compatibleCache := readyCache("compatible", compatibleIdentity)

	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	node := kernelCacheNode("node-a", workloadCache, v1alpha1.KernelCacheNodePreparationStateReady)
	node.Status.CacheStatus[compatibleCache.Namespace+"/"+compatibleCache.Name] = kernelCacheNodeInfo(compatibleCache, v1alpha1.KernelCacheNodePreparationStateReady)
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&node, &compatibleCache, &workloadCache).Build()}

	selected, found, err := mutator.findKernelCache(context.Background(), pod)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, workloadCache.Name, selected.Name)
}

// Selects a cache when the weighted compatibility score reaches the threshold.
func TestWeightedMatch(t *testing.T) {
	pod := selectionPod("registry.example/vllm:v0.10")
	requested := requireFullIdentity(t, cacheidentity.KernelCacheIdentityFactors{
		Namespace:    pod.Namespace,
		WorkloadKind: "InferenceService",
		WorkloadName: "first",
		RuntimeImage: pod.Spec.Containers[0].Image,
		ModelURIHash: "sha256:" + strings.Repeat("a", 64),
		CommandHash:  "sha256:" + strings.Repeat("c", 64),
		ArgsHash:     "sha256:" + strings.Repeat("d", 64),
		RuntimeConfigFactors: map[string]string{
			cacheidentity.TensorParallelSizeFactor: "2",
			cacheidentity.DTypeFactor:              "bfloat16",
			cacheidentity.MaxModelLenFactor:        "4096",
		},
	})

	modelMismatch := requestedFactors(requested)
	modelMismatch.ModelURIHash = "sha256:" + strings.Repeat("e", 64)
	candidate := readyCache("weighted", requireFullIdentity(t, modelMismatch))
	selection := selectWeightedMatch(requested, []*v1alpha1.KernelCache{&candidate})
	require.NotNil(t, selection)
	require.Equal(t, kernelCacheMatchWeighted, selection.matchType)
	require.Equal(t, 80, selection.score)

	twoMismatches := modelMismatch
	twoMismatches.RuntimeImage = "registry.example/vllm:v0.11"
	rejected := readyCache("below-threshold", requireFullIdentity(t, twoMismatches))
	require.Nil(t, selectWeightedMatch(requested, []*v1alpha1.KernelCache{&rejected}))
}

// Requires matching compatibility settings for a workload footprint match.
func TestWorkloadMatchRequiresCompatibility(t *testing.T) {
	pod := selectionPod("registry.example/vllm@sha256:" + strings.Repeat("b", 64))
	requested := requireFullIdentity(t, cacheidentity.KernelCacheIdentityFactors{
		Namespace:    pod.Namespace,
		WorkloadKind: "InferenceService",
		WorkloadName: "first",
		RuntimeImage: pod.Spec.Containers[0].Image,
		ModelURIHash: "sha256:" + strings.Repeat("a", 64),
		RuntimeConfigFactors: map[string]string{
			cacheidentity.TensorParallelSizeFactor: "2",
			cacheidentity.DTypeFactor:              "bfloat16",
		},
	})

	changed := requestedFactors(requested)
	changed.RuntimeConfigFactors = map[string]string{
		cacheidentity.TensorParallelSizeFactor: "2",
		cacheidentity.DTypeFactor:              "float16",
	}
	cache := readyCache("same-workload-but-incompatible", requireFullIdentity(t, changed))

	candidate := kernelCacheNodeCandidate{
		ref:   v1alpha1.NamespacedName{Namespace: cache.Namespace, Name: cache.Name},
		infos: []v1alpha1.KernelCacheNodeCacheInfo{kernelCacheNodeInfo(cache, v1alpha1.KernelCacheNodePreparationStateReady)},
	}
	require.False(t, candidateMatchesWorkload(requested, candidate))
}

// Treats workload kind differences as compatibility-only differences.
func TestWorkloadKindIsNotExactMatch(t *testing.T) {
	pod := selectionPod("registry.example/vllm@sha256:" + strings.Repeat("b", 64))
	requested := requireFullIdentity(t, cacheidentity.KernelCacheIdentityFactors{
		Namespace:    pod.Namespace,
		WorkloadKind: "InferenceService",
		WorkloadName: "first",
		RuntimeImage: pod.Spec.Containers[0].Image,
		ModelURIHash: "sha256:" + strings.Repeat("a", 64),
		RuntimeConfigFactors: map[string]string{
			cacheidentity.TensorParallelSizeFactor: "2",
		},
	})

	otherKind := requestedFactors(requested)
	otherKind.WorkloadKind = "LLMInferenceService"
	candidate := readyCache("other-kind", requireFullIdentity(t, otherKind))
	nodeCandidate := kernelCacheNodeCandidate{
		ref:   v1alpha1.NamespacedName{Namespace: candidate.Namespace, Name: candidate.Name},
		infos: []v1alpha1.KernelCacheNodeCacheInfo{kernelCacheNodeInfo(candidate, v1alpha1.KernelCacheNodePreparationStateReady)},
	}
	require.False(t, candidateMatchesWorkload(requested, nodeCandidate))
	require.True(t, candidateMatchesCompatibility(requested, nodeCandidate))
}

// Scores only factors that affect runtime compatibility.
func TestWeightedScoreUsesCompatibilityFactors(t *testing.T) {
	requested := map[string]string{
		cacheidentity.NamespaceFactor:    "team-a",
		cacheidentity.WorkloadKindFactor: "InferenceService",
		cacheidentity.WorkloadNameFactor: "model-a",
		cacheidentity.CommandHashFactor:  "sha256:command-a",
		cacheidentity.ArgsHashFactor:     "sha256:args-a",
		cacheidentity.RuntimeImageFactor: "registry.example/vllm:v0.28.0",
		cacheidentity.ModelURIHashFactor: "sha256:model-a",
		cacheidentity.DTypeFactor:        "bfloat16",
	}
	candidate := map[string]string{
		cacheidentity.NamespaceFactor:    "team-b",
		cacheidentity.WorkloadKindFactor: "LLMInferenceService",
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

// Uses a Ready KCN footprint without consulting KernelCacheCapture.
func TestFindKernelCacheUsesReadyNode(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	pod := selectionPod("registry.example/vllm:v0.10")
	pod.Annotations = map[string]string{
		constants.StorageInitializerSourceUriInternalAnnotationKey: "hf://Qwen/Qwen3-0.6B",
	}
	requested, err := identityForPod(pod)
	require.NoError(t, err)
	cache := readyCache("matching", requested)
	readyNode := kernelCacheNode("node-a", cache, v1alpha1.KernelCacheNodePreparationStateReady)
	preparingNode := kernelCacheNode("node-b", cache, v1alpha1.KernelCacheNodePreparationStatePending)
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&readyNode, &preparingNode, &cache).Build()}

	selected, found, err := mutator.findKernelCache(context.Background(), pod)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, cache.Name, selected.Name)
}

// Falls back to weighted selection among caches reported Ready by KCN.
func TestFindKernelCacheWeightedFallback(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	pod := selectionPod("registry.example/vllm@sha256:" + strings.Repeat("b", 64))
	pod.Annotations = map[string]string{
		constants.StorageInitializerSourceUriInternalAnnotationKey: "hf://Qwen/Qwen3-0.6B",
	}
	pod.Spec.Containers[0].Command = []string{"vllm", "--dtype", "bfloat16", "--max-model-len", "4096", "--tensor-parallel-size", "2"}
	requested, err := identityForPod(pod)
	require.NoError(t, err)
	candidateFactors := requestedFactors(requested)
	candidateFactors.ModelURIHash = "sha256:" + strings.Repeat("c", 64)
	candidate := readyCache("weighted", requireFullIdentity(t, candidateFactors))
	node := kernelCacheNode("node-a", candidate, v1alpha1.KernelCacheNodePreparationStateReady)
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&node, &candidate).Build()}

	selected, found, err := mutator.findKernelCache(context.Background(), pod)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, candidate.Name, selected.Name)
}

// Ignores node-local cache entries that are not Ready.
func TestFindKernelCacheIgnoresUnreadyNode(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	pod := selectionPod("registry.example/vllm:v0.10")
	pod.Annotations = map[string]string{
		constants.StorageInitializerSourceUriInternalAnnotationKey: "hf://Qwen/Qwen3-0.6B",
	}
	requested, err := identityForPod(pod)
	require.NoError(t, err)
	cache := readyCache("preparing", requested)
	node := kernelCacheNode("node-a", cache, v1alpha1.KernelCacheNodePreparationStatePending)
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&node, &cache).Build()}

	selected, found, err := mutator.findKernelCache(context.Background(), pod)
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, selected)
}

func TestIdentityForPodIncludesWorkloadKind(t *testing.T) {
	isvcPod := selectionPod("registry.example/vllm@sha256:" + strings.Repeat("b", 64))
	isvcPod.Annotations = map[string]string{
		constants.StorageInitializerSourceUriInternalAnnotationKey: "hf://Qwen/Qwen3-0.6B",
	}
	isvcIdentity, err := identityForPod(isvcPod)
	require.NoError(t, err)

	llmPod := isvcPod.DeepCopy()
	llmPod.Labels = map[string]string{
		constants.KubernetesAppNameLabelKey:   "first",
		constants.KubernetesPartOfLabelKey:    constants.LLMInferenceServicePartOfValue,
		constants.KubernetesComponentLabelKey: constants.LLMComponentWorkload,
	}
	llmPod.Spec.Containers[0].Name = constants.LLMInferenceServiceContainerName
	llmPod.Spec.Containers = append([]corev1.Container{{
		Name:  constants.InferenceServiceContainerName,
		Image: "registry.example/sidecar:latest",
	}}, llmPod.Spec.Containers...)
	llmIdentity, err := identityForPod(llmPod)
	require.NoError(t, err)

	require.Equal(t, "InferenceService", isvcIdentity.Factors[cacheidentity.WorkloadKindFactor])
	require.Equal(t, "LLMInferenceService", llmIdentity.Factors[cacheidentity.WorkloadKindFactor])
	require.Equal(t, "registry.example/vllm@sha256:"+strings.Repeat("b", 64), llmIdentity.Factors[cacheidentity.RuntimeImageFactor])
	require.NotEqual(t, isvcIdentity.Footprints.WorkloadFootprint, llmIdentity.Footprints.WorkloadFootprint)
	require.Equal(t, isvcIdentity.Footprints.CompatibilityFootprint, llmIdentity.Footprints.CompatibilityFootprint)
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
	return requireFullIdentity(t, cacheidentity.KernelCacheIdentityFactors{
		Namespace: namespace, WorkloadKind: "InferenceService", WorkloadName: workloadName,
		RuntimeImage: image, ModelURIHash: modelHash,
	})
}

func requireFullIdentity(t *testing.T, factors cacheidentity.KernelCacheIdentityFactors) v1alpha1.KernelCacheIdentity {
	t.Helper()
	result, err := cacheidentity.Build(factors)
	require.NoError(t, err)
	return result
}

func requestedFactors(identity v1alpha1.KernelCacheIdentity) cacheidentity.KernelCacheIdentityFactors {
	return cacheidentity.KernelCacheIdentityFactors{
		Namespace:            identity.Factors[cacheidentity.NamespaceFactor],
		WorkloadKind:         identity.Factors[cacheidentity.WorkloadKindFactor],
		WorkloadName:         identity.Factors[cacheidentity.WorkloadNameFactor],
		RuntimeImage:         identity.Factors[cacheidentity.RuntimeImageFactor],
		ModelURIHash:         identity.Factors[cacheidentity.ModelURIHashFactor],
		CommandHash:          identity.Factors[cacheidentity.CommandHashFactor],
		ArgsHash:             identity.Factors[cacheidentity.ArgsHashFactor],
		RuntimeConfigFactors: runtimeConfigFactors(identity.Factors),
	}
}

func runtimeConfigFactors(factors map[string]string) map[string]string {
	runtime := make(map[string]string)
	for key, value := range factors {
		if cacheidentity.IsCompatibilityFactor(key) && key != cacheidentity.RuntimeImageFactor && key != cacheidentity.ModelURIHashFactor {
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

func kernelCacheNode(name string, cache v1alpha1.KernelCache, state v1alpha1.KernelCacheNodePreparationState) v1alpha1.KernelCacheNode {
	return v1alpha1.KernelCacheNode{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: v1alpha1.KernelCacheNodeStatus{CacheStatus: map[string]v1alpha1.KernelCacheNodeCacheInfo{
			cache.Namespace + "/" + cache.Name: kernelCacheNodeInfo(cache, state),
		}},
	}
}

func kernelCacheNodeInfo(cache v1alpha1.KernelCache, state v1alpha1.KernelCacheNodePreparationState) v1alpha1.KernelCacheNodeCacheInfo {
	return v1alpha1.KernelCacheNodeCacheInfo{
		KernelCacheRef: v1alpha1.NamespacedName{Namespace: cache.Namespace, Name: cache.Name},
		ImageReference: cache.Spec.Artifact.ImageReference,
		Footprints:     cache.Spec.Artifact.Identity.Footprints,
		State:          state,
	}
}
