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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	kernelcacheutil "github.com/kserve/kserve/pkg/kernelcache"
	cacheidentity "github.com/kserve/kserve/pkg/kernelcache/identity"
	kernelcachesecurity "github.com/kserve/kserve/pkg/kernelcache/security"
	kernelcachetypes "github.com/kserve/kserve/pkg/kernelcache/types"
)

const (
	runtimeResultStateKey                      = "state"
	runtimeResultMessageKey                    = "message"
	runtimeResultReasonKey                     = "reason"
	runtimeResultImageReferenceKey             = "imageReference"
	runtimeResultCachePathsKey                 = "cachePaths"
	runtimeResultRuntimeInfoKey                = "runtimeInfo"
	runtimeResultCapturedAtKey                 = "capturedAt"
	runtimeResultCompletedAtKey                = "completedAt"
	runtimeResultCacheSizeBytesKey             = "cacheSizeBytes"
	runtimeResultSourcePodNameKey              = "sourcePodName"
	runtimeResultCaptureSessionIDKey           = "captureSessionID"
	runtimeResultSucceededState                = "Succeeded"
	runtimeResultUnchangedState                = "Unchanged"
	runtimeResultFailedState                   = "Failed"
	runtimeResultWaitingForWorkloadState       = "WaitingForWorkload"
	runtimeResultCapturingState                = "Capturing"
	runtimeResultPushingState                  = "Pushing"
	kernelCacheCaptureReadyConditionType       = "Ready"
	kernelCacheCaptureReasonPending            = "Pending"
	kernelCacheCaptureReasonWaitingForWorkload = "WaitingForWorkload"
	kernelCacheCaptureReasonCapturing          = "Capturing"
	kernelCacheCaptureReasonPushing            = "Pushing"
	kernelCacheCaptureReasonComplete           = "CaptureComplete"
	kernelCacheCaptureReasonUnchanged          = "CacheUnchanged"
	kernelCacheCaptureReasonIdentityPending    = "IdentityPending"
	kernelCacheCaptureReasonFailed             = "CaptureFailed"
	kernelCacheCaptureReasonProducerGone       = "ProducerGone"
	kernelCacheCaptureReasonInvalidResult      = "InvalidRuntimeResult"
	runtimeInfoCommandHashKey                  = cacheidentity.CommandHashFactor
	runtimeInfoArgsHashKey                     = cacheidentity.ArgsHashFactor
	runtimeInfoModelURIHashKey                 = cacheidentity.ModelURIHashFactor
	runtimeInfoTensorParallelSizeKey           = cacheidentity.TensorParallelSizeFactor
)

// KernelCacheCaptureReconciler processes capture results for enabled InferenceServices.
type KernelCacheCaptureReconciler struct {
	client.Client
	Reader client.Reader
	Scheme *runtime.Scheme
}

func (r *KernelCacheCaptureReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	inferenceService := &v1beta1.InferenceService{}
	if err := r.Get(ctx, req.NamespacedName, inferenceService); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !inferenceService.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: constants.KServeNamespace,
		Name:      constants.InferenceServiceConfigMapName,
	}, configMap); err != nil {
		return ctrl.Result{}, err
	}
	kernelCacheConfig, err := v1beta1.NewKernelCacheConfig(configMap)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !kernelCacheConfig.Enabled {
		return ctrl.Result{}, nil
	}

	captures := &v1alpha1.KernelCacheCaptureList{}
	if err := r.List(ctx, captures, client.InNamespace(inferenceService.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	for i := range captures.Items {
		if !captureReferencesInferenceService(&captures.Items[i], inferenceService) {
			continue
		}
		current, err := r.reconcileRuntimeResult(ctx, &captures.Items[i])
		if err != nil {
			return ctrl.Result{}, err
		}
		if current == nil {
			continue
		}
		if err := r.recordTerminalCaptureState(ctx, current); err != nil {
			return ctrl.Result{}, err
		}
		deleteCapture, err := r.shouldDeleteGeneratedCapture(ctx, current, kernelCacheConfig)
		if err != nil {
			return ctrl.Result{}, err
		}
		if deleteCapture {
			if err := r.Delete(ctx, current); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
			continue
		}
		if err := r.reconcileArtifactSigning(ctx, current, kernelCacheConfig); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *KernelCacheCaptureReconciler) recordTerminalCaptureState(ctx context.Context, capture *v1alpha1.KernelCacheCapture) error {
	var state string
	switch capture.Status.Phase {
	case v1alpha1.KernelCacheCapturePhaseComplete:
		state = constants.KernelCacheCaptureStateComplete
	case v1alpha1.KernelCacheCapturePhaseUnchanged:
		state = constants.KernelCacheCaptureStateUnchanged
	default:
		return nil
	}
	if capture.Status.ActiveSession == nil || capture.Status.ActiveSession.PodName == "" {
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod := &corev1.Pod{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: capture.Namespace, Name: capture.Status.ActiveSession.PodName}, pod); err != nil {
			return client.IgnoreNotFound(err)
		}
		if pod.Annotations[constants.KernelCacheCaptureStateAnnotationKey] == state {
			return nil
		}
		base := pod.DeepCopy()
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[constants.KernelCacheCaptureStateAnnotationKey] = state
		return r.Patch(ctx, pod, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

func (r *KernelCacheCaptureReconciler) shouldDeleteGeneratedCapture(
	ctx context.Context,
	capture *v1alpha1.KernelCacheCapture,
	config *v1beta1.KernelCacheConfig,
) (bool, error) {
	if capture.Labels[constants.KernelCacheCaptureGeneratedLabelKey] != "true" {
		return false, nil
	}
	if capture.Status.ActiveSession == nil || capture.Status.ActiveSession.PodName == "" {
		return capture.Spec.SourceRef.Name != "" &&
			capture.Name == constants.KernelCacheCaptureName(capture.Spec.SourceRef.Name), nil
	}
	active := capture.Status.ActiveSession
	if active != nil && capture.Status.RuntimeResult != nil &&
		(capture.Status.RuntimeResult[runtimeResultCaptureSessionIDKey] != active.ID ||
			capture.Status.RuntimeResult[runtimeResultSourcePodNameKey] != active.PodName) {
		return false, nil
	}
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	pod := &corev1.Pod{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: capture.Namespace, Name: active.PodName}, pod); err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	if pod.Name != "" && pod.DeletionTimestamp == nil && pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
		return false, nil
	}

	if capture.Status.Phase == v1alpha1.KernelCacheCapturePhaseUnchanged &&
		capture.Status.RuntimeResult[runtimeResultStateKey] == runtimeResultUnchangedState &&
		capture.Status.Artifact == nil && capture.Status.KernelCacheRef == nil {
		return true, nil
	}
	if !captureInProgress(capture.Status.Phase) {
		return false, nil
	}
	if err := r.markCaptureProducerGone(ctx, capture); err != nil {
		return false, err
	}
	return config.AbandonedCapturePolicy == "delete", nil
}

func captureInProgress(phase v1alpha1.KernelCacheCapturePhase) bool {
	return phase == v1alpha1.KernelCacheCapturePhasePending ||
		phase == v1alpha1.KernelCacheCapturePhaseWaitingForWorkload ||
		phase == v1alpha1.KernelCacheCapturePhaseCapturing ||
		phase == v1alpha1.KernelCacheCapturePhasePushing
}

func (r *KernelCacheCaptureReconciler) markCaptureProducerGone(ctx context.Context, capture *v1alpha1.KernelCacheCapture) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &v1alpha1.KernelCacheCapture{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(capture), current); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !captureInProgress(current.Status.Phase) {
			return nil
		}
		desired := current.Status.DeepCopy()
		desired.Phase = v1alpha1.KernelCacheCapturePhaseFailed
		setCaptureCondition(desired, metav1.ConditionFalse, kernelCacheCaptureReasonProducerGone, "capture producer Pod no longer exists", current.Generation)
		current.Status = *desired
		return r.Status().Update(ctx, current)
	})
}

func (r *KernelCacheCaptureReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.InferenceService{}).
		Watches(&v1alpha1.KernelCacheCapture{}, handler.EnqueueRequestsFromMapFunc(r.captureSourceRequests)).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.capturePodSourceRequests)).
		Complete(r)
}

func (r *KernelCacheCaptureReconciler) capturePodSourceRequests(_ context.Context, object client.Object) []reconcile.Request {
	pod, ok := object.(*corev1.Pod)
	if !ok {
		return nil
	}
	inferenceServiceName := pod.Labels[constants.InferenceServicePodLabelKey]
	if inferenceServiceName == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: pod.Namespace, Name: inferenceServiceName}}}
}

func captureSigningSpec(config *v1beta1.KernelCacheConfig) *v1alpha1.KernelCacheSigningSpec {
	if config.ArtifactSecurity.Mode != string(kernelcachetypes.ModeCert) || config.ArtifactSecurity.Cert.SigningProfileRef == "" {
		return nil
	}
	return &v1alpha1.KernelCacheSigningSpec{
		ProfileRef: &corev1.LocalObjectReference{Name: config.ArtifactSecurity.Cert.SigningProfileRef},
	}
}

func (r *KernelCacheCaptureReconciler) captureSourceRequests(_ context.Context, object client.Object) []reconcile.Request {
	capture, ok := object.(*v1alpha1.KernelCacheCapture)
	if !ok || capture.Spec.SourceRef.Kind != "InferenceService" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Namespace: capture.Namespace,
		Name:      capture.Spec.SourceRef.Name,
	}}}
}

func captureReferencesInferenceService(capture *v1alpha1.KernelCacheCapture, inferenceService *v1beta1.InferenceService) bool {
	return capture.Namespace == inferenceService.Namespace &&
		capture.Spec.SourceRef.Kind == "InferenceService" &&
		capture.Spec.SourceRef.Name == inferenceService.Name
}

func (r *KernelCacheCaptureReconciler) reconcileRuntimeResult(ctx context.Context, capture *v1alpha1.KernelCacheCapture) (*v1alpha1.KernelCacheCapture, error) {
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}

	key := client.ObjectKeyFromObject(capture)
	var current *v1alpha1.KernelCacheCapture
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current = &v1alpha1.KernelCacheCapture{}
		if err := reader.Get(ctx, key, current); err != nil {
			if apierrors.IsNotFound(err) {
				current = nil
				return nil
			}
			return err
		}
		return r.reconcileCurrentRuntimeResult(ctx, current)
	})
	if err != nil {
		return nil, err
	}
	return current, nil
}

func (r *KernelCacheCaptureReconciler) reconcileCurrentRuntimeResult(ctx context.Context, capture *v1alpha1.KernelCacheCapture) error {
	// A failed capture is terminal for the selected producer. A terminating Pod
	// may still send an in-flight runtime report, but it must not regress the
	// capture back to an in-progress phase.
	if capture.Status.Phase == v1alpha1.KernelCacheCapturePhaseFailed {
		return nil
	}

	state := capture.Status.RuntimeResult[runtimeResultStateKey]
	if state == "" {
		if capture.Status.ActiveSession == nil || capture.Status.Phase == v1alpha1.KernelCacheCapturePhasePending {
			return nil
		}
		desired := capture.Status.DeepCopy()
		desired.Phase = v1alpha1.KernelCacheCapturePhasePending
		setCaptureCondition(desired, metav1.ConditionFalse, kernelCacheCaptureReasonPending, "waiting for the selected capture session", capture.Generation)
		capture.Status = *desired
		return r.Status().Update(ctx, capture)
	}
	if active := capture.Status.ActiveSession; active != nil {
		if capture.Status.RuntimeResult[runtimeResultCaptureSessionIDKey] != active.ID ||
			capture.Status.RuntimeResult[runtimeResultSourcePodNameKey] != active.PodName {
			return nil
		}
	}
	if captureRuntimeResultComplete(capture) {
		return nil
	}

	desired := capture.Status.DeepCopy()
	switch state {
	case runtimeResultWaitingForWorkloadState:
		desired.Phase = v1alpha1.KernelCacheCapturePhaseWaitingForWorkload
		setCaptureCondition(desired, metav1.ConditionFalse, kernelCacheCaptureReasonWaitingForWorkload, runtimeMessage(capture.Status.RuntimeResult, "waiting for workload readiness"), capture.Generation)
	case runtimeResultCapturingState:
		desired.Phase = v1alpha1.KernelCacheCapturePhaseCapturing
		setCaptureCondition(desired, metav1.ConditionFalse, kernelCacheCaptureReasonCapturing, runtimeMessage(capture.Status.RuntimeResult, "capture is in progress"), capture.Generation)
	case runtimeResultPushingState:
		desired.Phase = v1alpha1.KernelCacheCapturePhasePushing
		setCaptureCondition(desired, metav1.ConditionFalse, kernelCacheCaptureReasonPushing, runtimeMessage(capture.Status.RuntimeResult, "cache image is being pushed"), capture.Generation)
	case runtimeResultSucceededState:
		identityReady, err := r.completeCaptureStatus(ctx, desired, capture)
		if err != nil {
			desired.Phase = v1alpha1.KernelCacheCapturePhaseFailed
			setCaptureCondition(desired, metav1.ConditionFalse, kernelCacheCaptureReasonInvalidResult, err.Error(), capture.Generation)
		} else {
			desired.Phase = v1alpha1.KernelCacheCapturePhaseComplete
			if identityReady {
				setCaptureCondition(desired, metav1.ConditionTrue, kernelCacheCaptureReasonComplete, "kernel cache capture completed", capture.Generation)
			} else {
				setCaptureCondition(desired, metav1.ConditionFalse, kernelCacheCaptureReasonIdentityPending, "capture completed; runtime identity is not available yet", capture.Generation)
			}
		}
	case runtimeResultUnchangedState:
		desired.Phase = v1alpha1.KernelCacheCapturePhaseUnchanged
		setCaptureCondition(desired, metav1.ConditionTrue, kernelCacheCaptureReasonUnchanged, "no new kernel cache directories were created", capture.Generation)
	case runtimeResultFailedState:
		desired.Phase = v1alpha1.KernelCacheCapturePhaseFailed
		setCaptureCondition(desired, metav1.ConditionFalse, kernelCacheCaptureReasonFailed, runtimeMessage(capture.Status.RuntimeResult, "kernel cache capture failed"), capture.Generation)
	default:
		return nil
	}

	if reflect.DeepEqual(capture.Status, *desired) {
		return nil
	}
	capture.Status = *desired
	return r.Status().Update(ctx, capture)
}

func captureRuntimeResultComplete(capture *v1alpha1.KernelCacheCapture) bool {
	if capture.Status.Phase != v1alpha1.KernelCacheCapturePhaseComplete || capture.Status.Artifact == nil {
		return false
	}
	switch capture.Status.RuntimeResult[runtimeResultStateKey] {
	case runtimeResultSucceededState:
		return capture.Status.Artifact.ImageReference == capture.Status.RuntimeResult[runtimeResultImageReferenceKey]
	case runtimeResultUnchangedState:
		return capture.Status.KernelCacheRef != nil
	default:
		return false
	}
}

func (r *KernelCacheCaptureReconciler) reconcileArtifactSigning(
	ctx context.Context,
	capture *v1alpha1.KernelCacheCapture,
	config *v1beta1.KernelCacheConfig,
) error {
	if !captureRuntimeResultComplete(capture) {
		return nil
	}
	mode := config.ArtifactSecurity.Mode
	if signing := capture.Status.Signing; signing != nil && signing.Mode == mode &&
		(signing.State == v1alpha1.KernelCacheArtifactSecurityStateSkipped ||
			signing.State == v1alpha1.KernelCacheArtifactSecurityStateSucceeded) {
		return nil
	}
	if config.ArtifactSecurity.Mode == string(kernelcachetypes.ModeCert) &&
		(capture.Spec.Signing == nil || capture.Spec.Signing.ProfileRef == nil || capture.Spec.Signing.ProfileRef.Name == "") {
		return r.updateCaptureSigningStatus(ctx, capture, v1alpha1.KernelCacheSigningStatus{
			Mode:    mode,
			State:   v1alpha1.KernelCacheArtifactSecurityStateFailed,
			Reason:  "SigningProfileNotFound",
			Message: "spec.signing.profileRef.name is required for cert signing",
		})
	}

	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	signer, err := kernelcachesecurity.NewSigner(ctx, config.ArtifactSecurity.ToSecurityConfig(), kernelcachesecurity.NewKubernetesSecretSource(reader))
	if err != nil {
		return r.updateCaptureSigningStatus(ctx, capture, v1alpha1.KernelCacheSigningStatus{
			Mode:    mode,
			State:   v1alpha1.KernelCacheArtifactSecurityStateFailed,
			Reason:  "SignerUnavailable",
			Message: err.Error(),
		})
	}

	profileRef := ""
	if capture.Spec.Signing != nil && capture.Spec.Signing.ProfileRef != nil {
		profileRef = capture.Namespace + "/" + capture.Spec.Signing.ProfileRef.Name
	}
	result, err := signer.Sign(ctx, kernelcachetypes.SignRequest{
		ImageRef:   capture.Status.Artifact.ImageReference,
		ProfileRef: profileRef,
	})
	if err != nil {
		statusErr := r.updateCaptureSigningStatus(ctx, capture, v1alpha1.KernelCacheSigningStatus{
			Mode:    mode,
			State:   v1alpha1.KernelCacheArtifactSecurityStateFailed,
			Reason:  "SigningFailed",
			Message: err.Error(),
		})
		if statusErr != nil {
			return statusErr
		}
		return err
	}

	if result.Mode == kernelcachetypes.ModeNone {
		return r.updateCaptureSigningStatus(ctx, capture, v1alpha1.KernelCacheSigningStatus{
			Mode:    string(result.Mode),
			State:   v1alpha1.KernelCacheArtifactSecurityStateSkipped,
			Reason:  "SigningNotConfigured",
			Message: "artifact signing is not configured",
		})
	}
	parts := strings.SplitN(capture.Status.Artifact.ImageReference, "@", 2)
	if len(parts) != 2 {
		return r.updateCaptureSigningStatus(ctx, capture, v1alpha1.KernelCacheSigningStatus{
			Mode:    string(result.Mode),
			State:   v1alpha1.KernelCacheArtifactSecurityStateFailed,
			Reason:  "InvalidArtifactReference",
			Message: "artifact imageReference must be digest-pinned",
		})
	}
	expectedDigest := parts[1]
	if result.Digest != expectedDigest {
		return r.updateCaptureSigningStatus(ctx, capture, v1alpha1.KernelCacheSigningStatus{
			Mode:    string(result.Mode),
			State:   v1alpha1.KernelCacheArtifactSecurityStateFailed,
			Reason:  "SignedDigestMismatch",
			Message: fmt.Sprintf("signer returned digest %q for artifact digest %q", result.Digest, expectedDigest),
		})
	}

	now := metav1.Now()
	return r.updateCaptureSigningStatus(ctx, capture, v1alpha1.KernelCacheSigningStatus{
		Mode:     string(result.Mode),
		State:    v1alpha1.KernelCacheArtifactSecurityStateSucceeded,
		Signed:   true,
		Reason:   "SigningSucceeded",
		Message:  "artifact signing completed",
		SignedAt: &now,
	})
}

func (r *KernelCacheCaptureReconciler) updateCaptureSigningStatus(
	ctx context.Context,
	capture *v1alpha1.KernelCacheCapture,
	signing v1alpha1.KernelCacheSigningStatus,
) error {
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &v1alpha1.KernelCacheCapture{}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(capture), current); err != nil {
			return err
		}
		desired := current.Status.DeepCopy()
		desired.Signing = &signing
		switch signing.State {
		case v1alpha1.KernelCacheArtifactSecurityStateFailed:
			setCaptureCondition(desired, metav1.ConditionFalse, signing.Reason, signing.Message, current.Generation)
		case v1alpha1.KernelCacheArtifactSecurityStateSkipped:
			setCaptureCondition(desired, metav1.ConditionTrue, kernelCacheCaptureReasonComplete, "kernel cache capture completed", current.Generation)
		case v1alpha1.KernelCacheArtifactSecurityStateSucceeded:
			setCaptureCondition(desired, metav1.ConditionTrue, kernelCacheCaptureReasonComplete, "kernel cache capture completed and artifact signing succeeded", current.Generation)
		}
		if reflect.DeepEqual(current.Status, *desired) {
			return nil
		}
		current.Status = *desired
		return r.Status().Update(ctx, current)
	})
}

func (r *KernelCacheCaptureReconciler) completeCaptureStatus(ctx context.Context, status *v1alpha1.KernelCacheCaptureStatus, capture *v1alpha1.KernelCacheCapture) (bool, error) {
	result := capture.Status.RuntimeResult
	imageReference := result[runtimeResultImageReferenceKey]
	if !isDigestReference(imageReference) {
		return false, errors.New("runtime result imageReference must be a digest-pinned OCI reference")
	}

	cachePaths, err := captureCachePaths(capture, result)
	if err != nil {
		return false, err
	}
	if capturedAt := runtimeTime(result); capturedAt != nil {
		status.CapturedAt = capturedAt
	} else if status.CapturedAt == nil {
		now := metav1.Now()
		status.CapturedAt = &now
	}
	if value := result[runtimeResultCacheSizeBytesKey]; value != "" {
		size, err := strconv.ParseInt(value, 10, 64)
		if err != nil || size < 0 {
			return false, errors.New("runtime result cacheSizeBytes must be a non-negative integer")
		}
		status.CapturedCacheSizeBytes = &size
	}

	runtimeInfo, err := parseRuntimeInfo(result)
	if err != nil {
		return false, err
	}
	capturePod, err := r.capturePod(ctx, capture, result)
	if err != nil {
		return false, err
	}
	if capturePod == nil {
		return false, nil
	}
	for index := range cachePaths {
		containerName, err := kernelcacheutil.ResolveRuntimeContainerName(capturePod.Spec.Containers, cachePaths[index].ContainerName)
		if err != nil {
			return false, err
		}
		cachePaths[index].ContainerName = containerName
		container := findContainer(capturePod.Spec.Containers, containerName)
		containerPath, err := kernelcacheutil.ResolveContainerPath(container, cachePaths[index].ContainerPath)
		if err != nil {
			return false, err
		}
		cachePaths[index].ContainerPath = containerPath
	}
	runtimeImage, err := runtimeImage(capturePod, cachePaths[0].ContainerName)
	if err != nil {
		return false, err
	}
	if runtimeImage == "" {
		return false, nil
	}
	runtimeFactors := cacheidentity.ExtractContainerRuntimeFactors(findContainer(capturePod.Spec.Containers, cachePaths[0].ContainerName))
	cacheIdentity, err := cacheidentity.Build(cacheidentity.Input{
		Namespace:          capture.Namespace,
		WorkloadName:       capture.Spec.SourceRef.Name,
		RuntimeImage:       runtimeImage,
		ModelURIHash:       runtimeInfo[runtimeInfoModelURIHashKey],
		TensorParallelSize: runtimeFactors[cacheidentity.TensorParallelSizeFactor],
		CommandHash:        runtimeInfo[runtimeInfoCommandHashKey],
		ArgsHash:           runtimeInfo[runtimeInfoArgsHashKey],
		RuntimeFactors:     runtimeFactors,
	})
	if err != nil {
		return false, err
	}
	artifact := &v1alpha1.KernelCacheArtifact{
		ImageReference: imageReference,
		CachePaths:     cachePaths,
		Identity:       cacheIdentity,
	}
	if !reflect.DeepEqual(status.Artifact, artifact) {
		status.KernelCacheRef = nil
		status.Signing = nil
	}
	status.Artifact = artifact
	return true, nil
}

func (r *KernelCacheCaptureReconciler) capturePod(ctx context.Context, capture *v1alpha1.KernelCacheCapture, result map[string]string) (*corev1.Pod, error) {
	name := result[runtimeResultSourcePodNameKey]
	if name == "" {
		return nil, nil
	}
	pod := &corev1.Pod{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: capture.Namespace, Name: name}, pod); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if pod.DeletionTimestamp != nil {
		return nil, nil
	}
	if pod.Labels[constants.InferenceServicePodLabelKey] != capture.Spec.SourceRef.Name {
		return nil, errors.New("capture Pod does not belong to sourceRef")
	}
	return pod, nil
}

func runtimeImage(pod *corev1.Pod, containerName string) (string, error) {
	container := findContainer(pod.Spec.Containers, containerName)
	if container == nil {
		return "", fmt.Errorf("capture container %q was not found in source Pod", containerName)
	}
	return container.Image, nil
}

func findContainer(containers []corev1.Container, name string) *corev1.Container {
	for index := range containers {
		if containers[index].Name == name {
			return &containers[index]
		}
	}
	return nil
}

func parseRuntimeInfo(result map[string]string) (map[string]string, error) {
	value := result[runtimeResultRuntimeInfoKey]
	if value == "" {
		return nil, errors.New("runtime result runtimeInfo with modelURIHash is required")
	}
	info := map[string]string{}
	if err := json.Unmarshal([]byte(value), &info); err != nil {
		return nil, fmt.Errorf("runtime result runtimeInfo is invalid: %w", err)
	}
	if info[runtimeInfoModelURIHashKey] == "" {
		return nil, errors.New("runtimeInfo.modelURIHash is required")
	}
	for _, key := range []string{runtimeInfoCommandHashKey, runtimeInfoArgsHashKey, runtimeInfoModelURIHashKey} {
		if value := info[key]; value != "" && !cacheidentity.IsSHA256(value) {
			return nil, fmt.Errorf("runtimeInfo.%s must be a SHA-256 value", key)
		}
	}
	if value := info[runtimeInfoTensorParallelSizeKey]; value != "" && !isPositiveInteger(value) {
		return nil, fmt.Errorf("runtimeInfo.%s must be a positive integer", runtimeInfoTensorParallelSizeKey)
	}
	return info, nil
}

func isPositiveInteger(value string) bool {
	parsed, err := strconv.Atoi(value)
	return err == nil && parsed > 0
}

func captureCachePaths(capture *v1alpha1.KernelCacheCapture, result map[string]string) ([]v1alpha1.KernelCachePath, error) {
	if len(capture.Spec.CachePaths) > 0 {
		return append([]v1alpha1.KernelCachePath(nil), capture.Spec.CachePaths...), nil
	}
	value := result[runtimeResultCachePathsKey]
	if value == "" {
		return nil, errors.New("runtime result cachePaths is required when capture spec cachePaths is empty")
	}
	var paths []v1alpha1.KernelCachePath
	if err := json.Unmarshal([]byte(value), &paths); err != nil {
		return nil, fmt.Errorf("runtime result cachePaths is invalid: %w", err)
	}
	if len(paths) == 0 {
		return nil, errors.New("runtime result cachePaths must not be empty")
	}
	return paths, nil
}

func runtimeMessage(result map[string]string, fallback string) string {
	if message := result[runtimeResultMessageKey]; message != "" {
		return message
	}
	if reason := result[runtimeResultReasonKey]; reason != "" {
		return reason
	}
	return fallback
}

func runtimeTime(result map[string]string) *metav1.Time {
	value := result[runtimeResultCapturedAtKey]
	if value == "" {
		value = result[runtimeResultCompletedAtKey]
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	observed := metav1.NewTime(parsed)
	return &observed
}

func setCaptureCondition(status *v1alpha1.KernelCacheCaptureStatus, conditionStatus metav1.ConditionStatus, reason, message string, generation int64) {
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               kernelCacheCaptureReadyConditionType,
		Status:             conditionStatus,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            message,
	})
}

func isDigestReference(value string) bool {
	if !strings.Contains(value, "@sha256:") {
		return false
	}
	parts := strings.Split(value, "@sha256:")
	return len(parts) == 2 && parts[0] != "" && len(parts[1]) == 64 && isLowerHex(parts[1])
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return value != ""
}
