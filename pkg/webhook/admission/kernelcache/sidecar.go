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
	cacheidentity "github.com/kserve/kserve/pkg/kernelcache/identity"
	"github.com/kserve/kserve/pkg/kernelcache/podconfig"
	"github.com/kserve/kserve/pkg/kernelcache/registryauth"
	"github.com/kserve/kserve/pkg/kernelcache/reporter"
)

const (
	sidecarName = "mcv"

	mcvCaptureModeEnv        = "MCV_CAPTURE_MODE"
	mcvCacheDirEnv           = "MCV_CACHE_DIR"
	mcvTargetImageEnv        = "MCV_TARGET_IMAGE"
	mcvResultPathEnv         = "MCV_RESULT_PATH"
	mcvCommandHashEnv        = "MCV_RUNTIME_COMMAND_HASH"
	mcvArgsHashEnv           = "MCV_RUNTIME_ARGS_HASH"
	mcvModelURIHashEnv       = "MCV_RUNTIME_MODEL_URI_HASH"
	mcvTensorParallelSizeEnv = "MCV_TENSOR_PARALLEL_SIZE"
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
	updatedConfig.ReadinessEnv, err = readinessProbeEnv(container)
	if err != nil {
		return err
	}
	runtimeInfoEnv := runtimeInfoEnv(container, pod.Annotations[constants.StorageInitializerSourceUriInternalAnnotationKey])
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
	manifests, err := getSidecarManifests(updatedConfig)
	if err != nil {
		return err
	}
	mutatedPod := pod.DeepCopy()
	sidecar := &manifests.Containers[0]
	sidecar.Env = append(sidecar.Env, runtimeInfoEnv...)
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

func getSidecarManifests(cfg *v1beta1.KernelCacheConfig) (corev1.PodSpec, error) {
	cachePaths, err := json.Marshal(cfg.CachePaths)
	if err != nil {
		return corev1.PodSpec{}, err
	}
	manifests := corev1.PodSpec{}
	sidecar := corev1.Container{
		Name:            sidecarName,
		Image:           cfg.MCVImage,
		ImagePullPolicy: corev1.PullAlways,
		Env: []corev1.EnvVar{
			{Name: mcvCaptureModeEnv, Value: "true"},
			{Name: mcvCacheDirEnv, Value: "/workspace/cache/0"},
			{Name: mcvTargetImageEnv, Value: cfg.TargetImage},
			{Name: mcvResultPathEnv, Value: "/tmp/mcv/result.json"},
			{Name: "MCV_CAPTURE_NAME", Value: cfg.CaptureName},
			{Name: "MCV_CAPTURE_NAMESPACE", Value: cfg.CaptureNamespace},
			{Name: "MCV_CAPTURE_SESSION_ID", Value: cfg.CaptureSessionID},
			{Name: "MCV_CACHE_PATHS", Value: string(cachePaths)},
			{Name: "MCV_SOURCE_POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		},
	}

	sidecar.Env = append(sidecar.Env, cfg.ReadinessEnv...)
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

func runtimeInfoEnv(container *corev1.Container, modelURI string) []corev1.EnvVar {
	runtime := runtimeIdentityInput(container, modelURI)
	env := make([]corev1.EnvVar, 0, 4)
	if runtime.CommandHash != "" {
		env = append(env, corev1.EnvVar{Name: mcvCommandHashEnv, Value: runtime.CommandHash})
	}
	if runtime.ArgsHash != "" {
		env = append(env, corev1.EnvVar{Name: mcvArgsHashEnv, Value: runtime.ArgsHash})
	}
	if runtime.ModelURIHash != "" {
		env = append(env, corev1.EnvVar{Name: mcvModelURIHashEnv, Value: runtime.ModelURIHash})
	}
	if runtime.TensorParallelSize != "" {
		env = append(env, corev1.EnvVar{Name: mcvTensorParallelSizeEnv, Value: runtime.TensorParallelSize})
	}
	return env
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

func readinessProbeEnv(container *corev1.Container) ([]corev1.EnvVar, error) {
	probe := container.ReadinessProbe
	if probe == nil || probe.HTTPGet == nil {
		return nil, fmt.Errorf("container %q requires an HTTP readinessProbe for MCV capture", container.Name)
	}
	port, err := readinessProbePort(container, probe.HTTPGet.Port)
	if err != nil {
		return nil, err
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
	return []corev1.EnvVar{
		{Name: "MCV_READINESS_PROBE_TYPE", Value: "httpGet"},
		{Name: "MCV_READINESS_PROBE_SCHEME", Value: strings.ToLower(scheme)},
		{Name: "MCV_READINESS_PROBE_HOST", Value: host},
		{Name: "MCV_READINESS_PROBE_PORT", Value: port},
		{Name: "MCV_READINESS_PROBE_PATH", Value: pathValue},
		{Name: "MCV_READINESS_PROBE_INITIAL_DELAY_SECONDS", Value: strconv.Itoa(int(probe.InitialDelaySeconds))},
		{Name: "MCV_READINESS_PROBE_PERIOD_SECONDS", Value: strconv.Itoa(int(probe.PeriodSeconds))},
		{Name: "MCV_READINESS_PROBE_TIMEOUT_SECONDS", Value: strconv.Itoa(int(probe.TimeoutSeconds))},
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
