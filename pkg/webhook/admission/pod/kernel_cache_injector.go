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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/constants"
	kernelcacheutil "github.com/kserve/kserve/pkg/kernelcache"
)

const kernelCacheVolumeName = "kernel-cache"

type KernelCacheInjector struct {
	client client.Client
}

func NewKernelCacheInjector(client client.Client) *KernelCacheInjector {
	return &KernelCacheInjector{client: client}
}

func (i *KernelCacheInjector) Inject(ctx context.Context, pod *corev1.Pod, defaultMountType v1alpha1.KernelCacheMountType) error {
	kernelCacheName, ok := pod.Labels[constants.KernelCacheLabel]
	if !ok || kernelCacheName == "" {
		return nil
	}

	kernelCache := &v1alpha1.KernelCache{}
	if err := i.client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: kernelCacheName}, kernelCache); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("KernelCache %q was not found in namespace %q", kernelCacheName, pod.Namespace)
		}
		return fmt.Errorf("failed to get KernelCache %q: %w", kernelCacheName, err)
	}

	mountType := kernelCache.Spec.MountType
	if mountType == "" {
		mountType = defaultMountType
	}
	if mountType == "" {
		mountType = v1alpha1.KernelCacheMountTypePVC
	}
	if mountType != v1alpha1.KernelCacheMountTypeOCI {
		return nil
	}
	if kernelCache.Spec.Artifact.ImageReference == "" {
		return fmt.Errorf("KernelCache %q has no artifact image reference", kernelCacheName)
	}
	if len(kernelCache.Spec.Artifact.CachePaths) == 0 {
		return fmt.Errorf("KernelCache %q has no cache paths", kernelCacheName)
	}

	if err := ensureKernelCacheVolume(pod, kernelCache.Spec.Artifact.ImageReference); err != nil {
		return err
	}

	for _, cachePath := range kernelCache.Spec.Artifact.CachePaths {
		containerName, err := kernelcacheutil.ResolveRuntimeContainerName(pod.Spec.Containers, cachePath.ContainerName)
		if err != nil {
			return err
		}
		containerIndex := containerIndex(pod.Spec.Containers, containerName)
		if containerIndex < 0 {
			return fmt.Errorf("KernelCache %q targets container %q, which is not present in Pod %q", kernelCacheName, containerName, pod.Name)
		}
		containerPath, err := kernelcacheutil.ResolveContainerPath(&pod.Spec.Containers[containerIndex], cachePath.ContainerPath)
		if err != nil {
			return err
		}
		cachePath.ContainerPath = containerPath
		if cachePath.OCIPath == "" {
			return fmt.Errorf("KernelCache %q contains an incomplete cache path", kernelCacheName)
		}

		mount := corev1.VolumeMount{
			Name:      kernelCacheVolumeName,
			MountPath: cachePath.ContainerPath,
			ReadOnly:  true,
			SubPath:   cachePath.OCIPath,
		}
		if err := addKernelCacheMount(&pod.Spec.Containers[containerIndex], mount); err != nil {
			return fmt.Errorf("failed to mount KernelCache %q: %w", kernelCacheName, err)
		}
	}

	return nil
}

func ensureKernelCacheVolume(pod *corev1.Pod, imageReference string) error {
	for _, volume := range pod.Spec.Volumes {
		if volume.Name != kernelCacheVolumeName {
			continue
		}
		if volume.Image == nil || volume.Image.Reference != imageReference {
			return fmt.Errorf("Pod already contains volume %q with a different source", kernelCacheVolumeName)
		}
		return nil
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: kernelCacheVolumeName,
		VolumeSource: corev1.VolumeSource{
			Image: &corev1.ImageVolumeSource{
				Reference:  imageReference,
				PullPolicy: corev1.PullIfNotPresent,
			},
		},
	})
	return nil
}

func addKernelCacheMount(container *corev1.Container, mount corev1.VolumeMount) error {
	for _, existing := range container.VolumeMounts {
		if existing.Name == mount.Name && existing.MountPath == mount.MountPath && existing.SubPath == mount.SubPath {
			return nil
		}
		if existing.MountPath == mount.MountPath {
			return fmt.Errorf("container %q already has a volume mounted at %q", container.Name, mount.MountPath)
		}
	}

	container.VolumeMounts = append(container.VolumeMounts, mount)
	return nil
}

func containerIndex(containers []corev1.Container, name string) int {
	for index := range containers {
		if containers[index].Name == name {
			return index
		}
	}
	return -1
}
