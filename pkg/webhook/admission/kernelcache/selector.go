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
	"errors"
	"sort"

	corev1 "k8s.io/api/core/v1"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/constants"
	kernelcacheutil "github.com/kserve/kserve/pkg/kernelcache"
	cacheidentity "github.com/kserve/kserve/pkg/kernelcache/identity"
)

type kernelCacheMatch string

const (
	kernelCacheMatchWorkload      kernelCacheMatch = "Workload"
	kernelCacheMatchCompatibility kernelCacheMatch = "Compatibility"
	kernelCacheMatchWeighted      kernelCacheMatch = "Weighted"
	minimumWeightedMatchPercent                    = 80
)

type kernelCacheSelection struct {
	cache     *v1alpha1.KernelCache
	matchType kernelCacheMatch
	score     int
}

func identityForPod(pod *corev1.Pod) (v1alpha1.KernelCacheIdentity, error) {
	workloadName := pod.Labels[constants.InferenceServicePodLabelKey]
	if workloadName == "" {
		return v1alpha1.KernelCacheIdentity{}, errors.New("InferenceService Pod label is required")
	}
	containerIndex := findContainerIndex(pod.Spec.Containers, constants.InferenceServiceContainerName)
	if containerIndex < 0 {
		containerIndex = findContainerIndex(pod.Spec.Containers, constants.LLMInferenceServiceContainerName)
	}
	if containerIndex < 0 {
		return v1alpha1.KernelCacheIdentity{}, errors.New("runtime container was not found")
	}
	container := &pod.Spec.Containers[containerIndex]
	input := runtimeIdentityInput(container, pod.Annotations[constants.StorageInitializerSourceUriInternalAnnotationKey])
	input.Namespace = pod.Namespace
	input.WorkloadName = workloadName
	return cacheidentity.Build(input)
}

func selectKernelCache(
	pod *corev1.Pod,
	requested v1alpha1.KernelCacheIdentity,
	caches []v1alpha1.KernelCache,
	preferredName string,
) *kernelCacheSelection {
	if !cacheidentity.IsStableRuntimeImage(requested.Factors[cacheidentity.RuntimeImageFactor]) {
		return nil
	}
	candidates := make([]*v1alpha1.KernelCache, 0, len(caches))
	for index := range caches {
		cache := &caches[index]
		if cache.Status.State != v1alpha1.KernelCacheStateReady || !cacheCanMountToPod(cache, pod) {
			continue
		}
		candidates = append(candidates, cache)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Name == preferredName {
			return true
		}
		if candidates[j].Name == preferredName {
			return false
		}
		return candidates[i].Name < candidates[j].Name
	})

	if requested.Footprints.WorkloadFootprint != "" {
		for _, cache := range candidates {
			if cache.Spec.Artifact.Identity.Footprints.WorkloadFootprint == requested.Footprints.WorkloadFootprint && compatibilityMatches(requested, cache.Spec.Artifact.Identity) {
				return &kernelCacheSelection{cache: cache, matchType: kernelCacheMatchWorkload, score: 100}
			}
		}
	}
	if requested.Footprints.CompatibilityFootprint != "" {
		for _, cache := range candidates {
			if cache.Spec.Artifact.Identity.Footprints.CompatibilityFootprint == requested.Footprints.CompatibilityFootprint {
				return &kernelCacheSelection{cache: cache, matchType: kernelCacheMatchCompatibility, score: 100}
			}
		}
	}

	var selected *kernelCacheSelection
	for _, cache := range candidates {
		score, acceptable := weightedFactorScore(requested.Factors, cache.Spec.Artifact.Identity.Factors)
		if !acceptable || (selected != nil && score <= selected.score) {
			continue
		}
		selected = &kernelCacheSelection{cache: cache, matchType: kernelCacheMatchWeighted, score: score}
	}
	return selected
}

func weightedFactorScore(requested, candidate map[string]string) (int, bool) {
	matches := 0
	compared := 0
	for _, key := range cacheidentity.ScoringFactorKeys() {
		requestedValue, requestedExists := requested[key]
		candidateValue, candidateExists := candidate[key]
		if !requestedExists && !candidateExists {
			continue
		}
		compared++
		if requestedExists && candidateExists && requestedValue == candidateValue {
			matches++
		}
	}
	if compared == 0 {
		return 0, false
	}
	score := matches * 100 / compared
	if score < minimumWeightedMatchPercent {
		return score, false
	}
	return score, true
}

func compatibilityMatches(requested, candidate v1alpha1.KernelCacheIdentity) bool {
	return requested.Footprints.CompatibilityFootprint != "" &&
		requested.Footprints.CompatibilityFootprint == candidate.Footprints.CompatibilityFootprint
}

func cacheCanMountToPod(cache *v1alpha1.KernelCache, pod *corev1.Pod) bool {
	if cache.Namespace != pod.Namespace || cache.Spec.Artifact.ImageReference == "" || len(cache.Spec.Artifact.CachePaths) == 0 {
		return false
	}
	if cache.Spec.MountType != "" && cache.Spec.MountType != v1alpha1.KernelCacheMountTypeOCI && cache.Spec.MountType != v1alpha1.KernelCacheMountTypePVC {
		return false
	}
	for _, cachePath := range cache.Spec.Artifact.CachePaths {
		containerName, err := kernelcacheutil.ResolveRuntimeContainerName(pod.Spec.Containers, cachePath.ContainerName)
		if err != nil {
			return false
		}
		containerIndex := findContainerIndex(pod.Spec.Containers, containerName)
		if containerIndex < 0 {
			return false
		}
		if _, err := kernelcacheutil.ResolveContainerPath(&pod.Spec.Containers[containerIndex], cachePath.ContainerPath); err != nil {
			return false
		}
	}
	return true
}
