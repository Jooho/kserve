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
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	kernelcacheutil "github.com/kserve/kserve/pkg/kernelcache"
	"github.com/kserve/kserve/pkg/kernelcache/captureconfig"
	cacheidentity "github.com/kserve/kserve/pkg/kernelcache/identity"
	"github.com/kserve/kserve/pkg/kernelcache/podconfig"
	"github.com/kserve/kserve/pkg/kernelcache/registryauth"
	"github.com/kserve/kserve/pkg/kernelcache/reporter"
)

const (
	sidecarName = "mcv"
)

func (m *PodMutator) injectSidecar(ctx context.Context, pod *corev1.Pod, cfg *v1beta1.KernelCacheConfig) error {
	inferenceServiceName := pod.Labels[constants.InferenceServicePodLabelKey]
	defaultCaptureName := constants.KernelCacheCaptureName(inferenceServiceName)
	if findContainerIndex(pod.Spec.Containers, sidecarName) >= 0 {
		return nil
	}
	captureID := string(uuid.NewUUID())
	captureName := generatedCaptureName(defaultCaptureName, captureID)
	updatedConfig := cfg.DeepCopy()
	var err error
	reader := m.Reader
	if reader == nil {
		reader = m.Client
	}
	capture, err := captureForSidecar(ctx, reader, pod.Namespace, inferenceServiceName)
	if err != nil {
		return err
	}
	if capture != nil {
		if len(capture.Spec.CachePaths) > 0 {
			updatedConfig.CachePaths = append([]v1alpha1.KernelCachePath(nil), capture.Spec.CachePaths...)
		}
		if capture.Spec.TargetImage != "" {
			updatedConfig.TargetImage = capture.Spec.TargetImage
		}
		if capture.Status.ActiveSession == nil && !captureTerminal(capture) {
			captureName = capture.Name
		}
	}
	if len(updatedConfig.CachePaths) > 0 {
		for index := range updatedConfig.CachePaths {
			containerName, err := kernelcacheutil.ResolveRuntimeContainerName(
				pod.Spec.Containers,
				updatedConfig.CachePaths[index].ContainerName,
			)
			if err != nil {
				return err
			}
			updatedConfig.CachePaths[index].ContainerName = containerName
			containerIndex := findContainerIndex(pod.Spec.Containers, containerName)
			containerPath, err := kernelcacheutil.ResolveContainerPath(
				&pod.Spec.Containers[containerIndex],
				updatedConfig.CachePaths[index].ContainerPath,
			)
			if err != nil {
				return err
			}
			updatedConfig.CachePaths[index].ContainerPath = containerPath
		}
	}
	containerName, err := kernelcacheutil.ResolveRuntimeContainerName(pod.Spec.Containers, "")
	if len(updatedConfig.CachePaths) > 0 {
		containerName = updatedConfig.CachePaths[0].ContainerName
	}
	if err != nil && len(updatedConfig.CachePaths) == 0 {
		return nil
	}
	containerIndex := findContainerIndex(pod.Spec.Containers, containerName)
	if containerIndex < 0 {
		return nil
	}
	container := &pod.Spec.Containers[containerIndex]
	if len(updatedConfig.CachePaths) == 0 {
		updatedConfig.CachePaths, err = defaultCachePaths(container)
		if err != nil {
			return err
		}
	}
	readinessConfig, err := readinessProbeConfig(container, updatedConfig.MCVCaptureReadinessTimeoutSeconds)
	if err != nil {
		return err
	}
	runtimeInfo := runtimeInfoConfig(container, pod.Annotations[constants.StorageInitializerSourceUriInternalAnnotationKey])
	if updatedConfig.TargetImage == "" {
		if updatedConfig.Registry.Endpoint == "" {
			return errors.New("kernelcache.registry.endpoint is required when no KernelCacheCapture target is configured")
		}
		updatedConfig.TargetImage = constants.KernelCacheTargetImage(
			updatedConfig.Registry.Endpoint,
			pod.Namespace,
			inferenceServiceName,
			captureID,
		)
	}

	if updatedConfig.Registry.Auth.Type == "openshift" {
		updatedConfig.CredentialSecretName = "mcv-registry-" + captureID
	}
	updatedConfig.ReporterSecretName = "mcv-reporter-" + captureID
	updatedConfig.CaptureName = captureName
	updatedConfig.CaptureNamespace = pod.Namespace
	updatedConfig.CaptureSessionID = captureID
	manifests, err := getSidecarManifestsWithConfigs(updatedConfig, readinessConfig, runtimeInfo)
	if err != nil {
		return err
	}
	mutatedPod := pod.DeepCopy()
	sidecar := &manifests.Containers[0]
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == kernelCacheSourceVolumeName {
			sidecar.VolumeMounts = append(sidecar.VolumeMounts, corev1.VolumeMount{
				Name: kernelCacheSourceVolumeName, MountPath: kernelCacheSourceMountPath, ReadOnly: true,
			})
			sidecar.Env = append(sidecar.Env, corev1.EnvVar{Name: "MCV_CACHE_LINK_ROOT", Value: kernelCacheSourceMountPath})
			break
		}
	}
	for index, cachePath := range updatedConfig.CachePaths {
		if err := addCacheMount(&mutatedPod.Spec, sidecar, index, cachePath, manifests.Volumes[index]); err != nil {
			return err
		}
	}
	for _, volume := range manifests.Volumes[len(updatedConfig.CachePaths):] {
		index := slices.IndexFunc(mutatedPod.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == volume.Name })
		if index < 0 {
			mutatedPod.Spec.Volumes = append(mutatedPod.Spec.Volumes, volume)
		} else if !reflect.DeepEqual(mutatedPod.Spec.Volumes[index], volume) {
			return fmt.Errorf("conflicting volume %q", volume.Name)
		}
	}
	mutatedPod.Spec.Containers = append(mutatedPod.Spec.Containers, *sidecar)
	if mutatedPod.Annotations == nil {
		mutatedPod.Annotations = map[string]string{}
	}
	mutatedPod.Annotations[registryauth.InjectedAnnotation] = "true"
	mutatedPod.Annotations[reporter.AccessSecretAnnotation] = updatedConfig.ReporterSecretName
	if updatedConfig.CredentialSecretName != "" {
		mutatedPod.Annotations[registryauth.AccessSecretAnnotation] = updatedConfig.CredentialSecretName
	}
	*pod = *mutatedPod
	return nil
}

func captureForSidecar(
	ctx context.Context,
	reader client.Reader,
	namespace string,
	inferenceServiceName string,
) (*v1alpha1.KernelCacheCapture, error) {
	captures := &v1alpha1.KernelCacheCaptureList{}
	if err := reader.List(ctx, captures, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	matching := make([]*v1alpha1.KernelCacheCapture, 0, len(captures.Items))
	for index := range captures.Items {
		capture := &captures.Items[index]
		if capture.Labels[constants.KernelCacheCaptureGeneratedLabelKey] == "true" {
			continue
		}
		sourceMatches := capture.Spec.SourceRef.Kind == "InferenceService" && capture.Spec.SourceRef.Name == inferenceServiceName
		if sourceMatches {
			matching = append(matching, capture)
		}
	}
	sort.Slice(matching, func(i, j int) bool { return matching[i].Name < matching[j].Name })
	// Prefer a non-terminal capture (so we can reuse its captureName), but fall
	// back to any user-declared capture so its Spec (TargetImage, CachePaths)
	// is still propagated to the new session.
	for _, capture := range matching {
		if capture.Status.ActiveSession == nil && !captureTerminal(capture) {
			return capture, nil
		}
	}
	if len(matching) > 0 {
		return matching[0], nil
	}
	return nil, nil
}

func captureTerminal(capture *v1alpha1.KernelCacheCapture) bool {
	return capture.Status.Phase == v1alpha1.KernelCacheCapturePhaseComplete ||
		capture.Status.Phase == v1alpha1.KernelCacheCapturePhaseUnchanged ||
		capture.Status.Phase == v1alpha1.KernelCacheCapturePhaseFailed ||
		capture.Status.Artifact != nil || capture.Status.KernelCacheRef != nil
}

func generatedCaptureName(base, captureID string) string {
	suffix := "-" + captureID
	if len(base)+len(suffix) > 63 {
		base = strings.TrimRight(base[:63-len(suffix)], "-.")
	}
	return base + suffix
}

func getSidecarManifestsWithConfigs(cfg *v1beta1.KernelCacheConfig, readiness captureconfig.ReadinessConfig, runtimeInfo captureconfig.RuntimeInfo) (corev1.PodSpec, error) {
	captureValue, err := captureconfig.MarshalCaptureConfig(captureconfig.CaptureConfig{
		Version:     captureconfig.CurrentVersion,
		CacheDir:    "/workspace/cache/0",
		TargetImage: cfg.TargetImage,
		Capture: captureconfig.CaptureIdentity{
			Name:      cfg.CaptureName,
			Namespace: cfg.CaptureNamespace,
			SessionID: cfg.CaptureSessionID,
		},
		CachePaths: cfg.CachePaths,
	})
	if err != nil {
		return corev1.PodSpec{}, err
	}
	readinessValue, err := captureconfig.MarshalReadinessConfig(readiness)
	if err != nil {
		return corev1.PodSpec{}, err
	}
	runtimeValue, err := captureconfig.MarshalRuntimeInfo(runtimeInfo)
	if err != nil {
		return corev1.PodSpec{}, err
	}
	manifests := corev1.PodSpec{}
	sidecar := corev1.Container{
		Name:            sidecarName,
		Image:           cfg.MCVImage,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"python3", "/capture-entrypoint.py"},
		Env: []corev1.EnvVar{
			{Name: captureconfig.CaptureConfigEnv, Value: captureValue},
			{Name: captureconfig.ReadinessConfigEnv, Value: readinessValue},
			{Name: captureconfig.RuntimeInfoEnv, Value: runtimeValue},
			{Name: "MCV_SOURCE_POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		},
	}

	for index := range cfg.CachePaths {
		name := fmt.Sprintf("mcv-cache-%d", index)
		manifests.Volumes = append(manifests.Volumes, corev1.Volume{
			Name: name, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		sidecar.VolumeMounts = append(sidecar.VolumeMounts, corev1.VolumeMount{
			Name: name, MountPath: fmt.Sprintf("/workspace/cache/%d", index),
		})
	}
	if err := podconfig.ApplyCaptureRegistry(&manifests, &sidecar, cfg.Registry, cfg.CredentialSecretName); err != nil {
		return corev1.PodSpec{}, err
	}
	if err := podconfig.ApplyCaptureReporter(&manifests, &sidecar, cfg.ReporterSecretName); err != nil {
		return corev1.PodSpec{}, err
	}
	manifests.Containers = []corev1.Container{sidecar}
	return manifests, nil
}

func runtimeInfoConfig(container *corev1.Container, modelURI string) captureconfig.RuntimeInfo {
	runtime := runtimeIdentityInput(container, modelURI)
	return captureconfig.RuntimeInfo{
		CommandHash:  runtime.CommandHash,
		ArgsHash:     runtime.ArgsHash,
		ModelURIHash: runtime.ModelURIHash,
	}
}

func runtimeIdentityInput(container *corev1.Container, modelURI string) cacheidentity.Input {
	runtimeFactors := cacheidentity.ExtractContainerRuntimeFactors(container)
	input := cacheidentity.Input{
		RuntimeImage:       container.Image,
		ModelURIHash:       cacheidentity.ModelURIHash(modelURI),
		TensorParallelSize: runtimeFactors[cacheidentity.TensorParallelSizeFactor],
		RuntimeFactors:     runtimeFactors,
	}
	if len(container.Command) > 0 {
		input.CommandHash = cacheidentity.HashStrings(container.Command)
	}
	if len(container.Args) > 0 {
		input.ArgsHash = cacheidentity.HashStrings(container.Args)
	}
	return input
}

// TODO: Add TRITON_CACHE_DIR to the default cache paths
func defaultCachePaths(container *corev1.Container) ([]v1alpha1.KernelCachePath, error) {
	paths := []v1alpha1.KernelCachePath{
		{ContainerName: container.Name, ContainerPath: "/root/.cache/vllm", OCIPath: "io.vllm.cache"},
		// {ContainerName: container.Name, ContainerPath: "/root/.triton/cache", OCIPath: "triton"},
	}
	for _, env := range container.Env {
		// index := slices.Index([]string{"VLLM_CACHE_ROOT", "TRITON_CACHE_DIR"}, env.Name)
		index := slices.Index([]string{"VLLM_CACHE_ROOT"}, env.Name)
		if index < 0 {
			continue
		}
		if env.ValueFrom != nil {
			return nil, fmt.Errorf("%s uses valueFrom; provide KernelCacheCapture cachePaths for this workload", env.Name)
		}
		paths[index].ContainerPath = env.Value
	}
	return paths, nil
}

func addCacheMount(podSpec *corev1.PodSpec, sidecar *corev1.Container, index int, cachePath v1alpha1.KernelCachePath, volume corev1.Volume) error {
	if err := validateCachePath(cachePath); err != nil {
		return err
	}
	containerIndex := findContainerIndex(podSpec.Containers, cachePath.ContainerName)
	if containerIndex < 0 {
		return fmt.Errorf("cache container %q was not found", cachePath.ContainerName)
	}

	container := &podSpec.Containers[containerIndex]
	mount := corev1.VolumeMount{Name: volume.Name, MountPath: cachePath.ContainerPath}
	reusedMount := false

	for _, existing := range container.VolumeMounts {
		if existing.MountPath == mount.MountPath {
			if existing.ReadOnly {
				return fmt.Errorf("capture path %q is read-only", existing.MountPath)
			}
			mount = existing
			reusedMount = true
			break
		}
		if strings.HasPrefix(mount.MountPath, strings.TrimSuffix(existing.MountPath, "/")+"/") {
			return fmt.Errorf("capture path %q overlaps mount %q; configure an exact cache mount", mount.MountPath, existing.MountPath)
		}
	}

	if !reusedMount {
		if slices.ContainsFunc(podSpec.Volumes, func(v corev1.Volume) bool { return v.Name == volume.Name }) {
			return fmt.Errorf("conflicting volume %q", volume.Name)
		}
		podSpec.Volumes = append(podSpec.Volumes, volume)
		container.VolumeMounts = append(container.VolumeMounts, mount)
	}

	mount.MountPath = sidecar.VolumeMounts[index].MountPath
	sidecar.VolumeMounts[index] = mount
	return nil
}

func validateCachePath(cachePath v1alpha1.KernelCachePath) error {
	if !path.IsAbs(cachePath.ContainerPath) || path.Clean(cachePath.ContainerPath) == "/" || strings.ContainsAny(cachePath.ContainerPath, "$\x00") {
		return fmt.Errorf("invalid cache containerPath %q", cachePath.ContainerPath)
	}
	cleanOCIPath := path.Clean(cachePath.OCIPath)
	if cachePath.OCIPath == "" || path.IsAbs(cachePath.OCIPath) || cleanOCIPath == "." || cleanOCIPath == ".." || strings.HasPrefix(cleanOCIPath, "../") || strings.ContainsRune(cachePath.OCIPath, '\x00') {
		return fmt.Errorf("invalid cache ociPath %q", cachePath.OCIPath)
	}
	return nil
}

func findContainerIndex(containers []corev1.Container, name string) int {
	return slices.IndexFunc(containers, func(c corev1.Container) bool { return c.Name == name })
}

func readinessProbeConfig(container *corev1.Container, timeoutSeconds int64) (captureconfig.ReadinessConfig, error) {
	probe := container.ReadinessProbe
	if probe == nil || probe.HTTPGet == nil {
		return captureconfig.ReadinessConfig{}, fmt.Errorf("container %q requires an HTTP readinessProbe for MCV capture", container.Name)
	}
	port, err := readinessProbePort(container, probe.HTTPGet.Port)
	if err != nil {
		return captureconfig.ReadinessConfig{}, err
	}
	scheme := string(probe.HTTPGet.Scheme)
	if scheme == "" {
		scheme = string(corev1.URISchemeHTTP)
	}
	host := probe.HTTPGet.Host
	if host == "" {
		host = "127.0.0.1"
	}
	pathValue := probe.HTTPGet.Path
	if pathValue == "" {
		pathValue = "/"
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = v1beta1.DefaultKernelCacheMCVCaptureReadinessTimeoutSeconds
	}
	return captureconfig.ReadinessConfig{
		URL:                               fmt.Sprintf("%s://%s:%s%s", strings.ToLower(scheme), host, port, pathValue),
		MCVCaptureReadinessTimeoutSeconds: timeoutSeconds,
	}, nil
}

func readinessProbePort(container *corev1.Container, port intstr.IntOrString) (string, error) {
	if port.Type == intstr.Int {
		if port.IntValue() <= 0 {
			return "", fmt.Errorf("container %q readinessProbe requires a valid port", container.Name)
		}
		return strconv.Itoa(port.IntValue()), nil
	}
	if port.StrVal == "" {
		return "", fmt.Errorf("container %q readinessProbe requires a port", container.Name)
	}
	for _, declared := range container.Ports {
		if declared.Name == port.StrVal && declared.ContainerPort > 0 {
			return strconv.Itoa(int(declared.ContainerPort)), nil
		}
	}
	return "", fmt.Errorf("container %q readinessProbe references unknown named port %q", container.Name, port.StrVal)
}
