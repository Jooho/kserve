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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/kernelcache/nodegroup"
	kernelcachesecurity "github.com/kserve/kserve/pkg/kernelcache/security"
	kernelcachetypes "github.com/kserve/kserve/pkg/kernelcache/types"
)

// KernelCacheReconciler reconciles KernelCache resources.
type KernelCacheReconciler struct {
	client.Client
	Reader   client.Reader
	Log      logr.Logger
	Recorder events.EventRecorder
}

const (
	kernelCacheFinalizerName            = "serving.kserve.io/kernelcache-finalizer"
	kernelCacheReadyConditionType       = "Ready"
	reasonNodeGroupNotFound             = "NodeGroupNotFound"
	reasonNoMatchingNodes               = "NoMatchingNodes"
	reasonNoReadyNodes                  = "NoReadyNodes"
	reasonConfigError                   = "ConfigError"
	reasonFeatureDisabled               = "FeatureDisabled"
	reasonVerificationFailed            = "VerificationFailed"
	reasonStorageError                  = "StorageError"
	reasonWaitingForPreparation         = "WaitingForPreparation"
	reasonPreparing                     = "Preparing"
	reasonCacheReady                    = "CacheReady"
	reasonPreparationFailed             = "PreparationFailed"
	defaultKernelCacheJobTTL      int32 = 3600
	kernelCacheNameLabel                = "serving.kserve.io/kernel-cache-name"
	kernelCacheNamespaceLabel           = "serving.kserve.io/kernel-cache-namespace"
	kernelCacheNodeLabel                = "serving.kserve.io/kernel-cache-node"
)

func (r *KernelCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	kernelCache := &v1alpha1.KernelCache{}
	if err := r.Get(ctx, req.NamespacedName, kernelCache); err != nil {
		if apierrors.IsNotFound(err) {
			return r.reconcileCaptureKernelCache(ctx, req)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !kernelCache.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.removeFinalizer(ctx, kernelCache)
	}
	if err := r.reconcileCaptureKernelCacheRef(ctx, kernelCache); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileKernelCacheUsage(ctx, kernelCache); err != nil {
		return ctrl.Result{}, err
	}

	mountType := effectiveMountType(kernelCache)
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: constants.KServeNamespace,
		Name:      constants.InferenceServiceConfigMapName,
	}, configMap); err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonConfigError, "inferenceservice-config was not found", mountType)
	}
	kernelCacheConfig, err := v1beta1.NewKernelCacheConfig(configMap)
	if err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonConfigError, err.Error(), mountType)
	}
	if kernelCache.Spec.MountType == "" && kernelCacheConfig.DefaultMountType != "" {
		mountType = v1alpha1.KernelCacheMountType(kernelCacheConfig.DefaultMountType)
	}
	if mountType != v1alpha1.KernelCacheMountTypeOCI {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonConfigError, fmt.Sprintf("unsupported KernelCache mount type %q", mountType), mountType)
	}
	if !kernelCacheConfig.Enabled {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStatePending, 0, reasonFeatureDisabled, "kernel cache is disabled in inferenceservice-config", mountType)
	}
	verified, err := r.reconcileArtifactVerification(ctx, kernelCache, kernelCacheConfig)
	if err != nil {
		if statusErr := r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonVerificationFailed, err.Error(), mountType); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}
	if !verified {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonVerificationFailed, "kernel cache artifact verification failed", mountType)
	}
	if kernelCache.Spec.NodeGroupRef == nil || kernelCache.Spec.NodeGroupRef.Name == "" {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonNodeGroupNotFound, "nodeGroupRef.name is required", mountType)
	}

	nodeGroup := &v1alpha1.KernelCacheNodeGroup{}
	if err := r.Get(ctx, client.ObjectKey{Name: kernelCache.Spec.NodeGroupRef.Name}, nodeGroup); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonNodeGroupNotFound, "referenced KernelCacheNodeGroup was not found", mountType)
		}
		return ctrl.Result{}, err
	}
	readyNodes, notReadyNodes, err := nodegroup.GetNodes(ctx, nodeGroup, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	nodeCount := len(readyNodes.Items) + len(notReadyNodes.Items)
	if nodeCount == 0 {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStatePending, 0, reasonNoMatchingNodes, "no nodes match the referenced KernelCacheNodeGroup", mountType)
	}
	if len(readyNodes.Items) == 0 {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStatePending, nodeCount, reasonNoReadyNodes, "matching nodes are not ready", mountType)
	}

	if kernelCacheConfig.JobNamespace == "" {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, nodeCount, reasonConfigError, "kernelcache.jobNamespace is required for cache preparation", mountType)
	}
	if err := r.checkNamespace(ctx, kernelCacheConfig.JobNamespace); err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, nodeCount, reasonStorageError, err.Error(), mountType)
	}
	if err := r.ensurePrefetchIdentity(ctx, kernelCache, kernelCacheConfig); err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, nodeCount, reasonStorageError, err.Error(), mountType)
	}

	if err := r.ensureOCIPrefetchJobs(ctx, kernelCache, nodeGroup, readyNodes, kernelCacheConfig); err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, nodeCount, reasonStorageError, err.Error(), mountType)
	}

	aggregate, err := r.aggregateKernelCacheStatus(ctx, kernelCache, readyNodes, notReadyNodes)
	if err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, nodeCount, reasonStorageError, err.Error(), mountType)
	}
	return ctrl.Result{}, r.updateStatusWithCounts(ctx, kernelCache, aggregate.State, aggregate.NodeCount, aggregate.NodesReady, aggregate.NodesPreparing, aggregate.NodesError, aggregate.Reason, aggregate.Message, mountType)
}

func (r *KernelCacheReconciler) reconcileArtifactVerification(
	ctx context.Context,
	kernelCache *v1alpha1.KernelCache,
	config *v1beta1.KernelCacheConfig,
) (bool, error) {
	mode := config.ArtifactSecurity.Mode
	if verification := kernelCache.Status.Verification; verification != nil && verification.Mode == mode {
		if verification.State == v1alpha1.KernelCacheArtifactSecurityStateSkipped {
			return mode == string(kernelcachetypes.ModeNone), nil
		}
	}

	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	verifier, err := kernelcachesecurity.NewVerifier(ctx, config.ArtifactSecurity.ToSecurityConfig(), kernelcachesecurity.NewKubernetesSecretSource(reader))
	if err != nil {
		statusErr := r.updateVerificationStatus(ctx, kernelCache, v1alpha1.KernelCacheVerificationStatus{
			Mode:    mode,
			State:   v1alpha1.KernelCacheArtifactSecurityStateFailed,
			Reason:  "VerifierUnavailable",
			Message: err.Error(),
		})
		if statusErr != nil {
			return false, statusErr
		}
		return false, err
	}

	result, err := verifier.Verify(ctx, kernelcachetypes.VerifyRequest{ImageRef: kernelCache.Spec.Artifact.ImageReference})
	if err != nil {
		statusErr := r.updateVerificationStatus(ctx, kernelCache, v1alpha1.KernelCacheVerificationStatus{
			Mode:    mode,
			State:   v1alpha1.KernelCacheArtifactSecurityStateFailed,
			Reason:  "VerificationError",
			Message: err.Error(),
		})
		if statusErr != nil {
			return false, statusErr
		}
		return false, err
	}

	if result.Mode == kernelcachetypes.ModeNone {
		return true, r.updateVerificationStatus(ctx, kernelCache, v1alpha1.KernelCacheVerificationStatus{
			Mode:    string(result.Mode),
			State:   v1alpha1.KernelCacheArtifactSecurityStateSkipped,
			Reason:  "VerificationNotConfigured",
			Message: "artifact verification is not configured",
		})
	}
	parts := strings.SplitN(kernelCache.Spec.Artifact.ImageReference, "@", 2)
	if len(parts) != 2 || result.Digest != parts[1] {
		return false, r.updateVerificationStatus(ctx, kernelCache, v1alpha1.KernelCacheVerificationStatus{
			Mode:    string(result.Mode),
			State:   v1alpha1.KernelCacheArtifactSecurityStateFailed,
			Reason:  "VerifiedDigestMismatch",
			Message: fmt.Sprintf("verifier returned digest %q for artifact %q", result.Digest, kernelCache.Spec.Artifact.ImageReference),
		})
	}
	if !result.Verified {
		return false, r.updateVerificationStatus(ctx, kernelCache, v1alpha1.KernelCacheVerificationStatus{
			Mode:    string(result.Mode),
			State:   v1alpha1.KernelCacheArtifactSecurityStateFailed,
			Reason:  "VerificationFailed",
			Message: result.Reason,
		})
	}

	now := metav1.Now()
	verifiedAt := &now
	if previous := kernelCache.Status.Verification; previous != nil && previous.Mode == string(result.Mode) &&
		previous.State == v1alpha1.KernelCacheArtifactSecurityStateSucceeded && previous.VerifiedAt != nil {
		verifiedAt = previous.VerifiedAt.DeepCopy()
	}
	return true, r.updateVerificationStatus(ctx, kernelCache, v1alpha1.KernelCacheVerificationStatus{
		Mode:       string(result.Mode),
		State:      v1alpha1.KernelCacheArtifactSecurityStateSucceeded,
		Verified:   true,
		Reason:     "VerificationSucceeded",
		Message:    "artifact verification completed",
		VerifiedAt: verifiedAt,
	})
}

func (r *KernelCacheReconciler) updateVerificationStatus(
	ctx context.Context,
	kernelCache *v1alpha1.KernelCache,
	verification v1alpha1.KernelCacheVerificationStatus,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &v1alpha1.KernelCache{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(kernelCache), current); err != nil {
			return err
		}
		if reflect.DeepEqual(current.Status.Verification, &verification) {
			return nil
		}
		current.Status.Verification = &verification
		return r.Status().Update(ctx, current)
	})
}

func effectiveMountType(kernelCache *v1alpha1.KernelCache) v1alpha1.KernelCacheMountType {
	if kernelCache.Spec.MountType == "" {
		return v1alpha1.KernelCacheMountTypeOCI
	}
	return kernelCache.Spec.MountType
}

func (r *KernelCacheReconciler) checkNamespace(ctx context.Context, name string) error {
	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, client.ObjectKey{Name: name}, namespace); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("job namespace %q was not found; create it before using KernelCache", name)
		}
		return err
	}
	return nil
}

func (r *KernelCacheReconciler) ensureOCIPrefetchJobs(
	ctx context.Context,
	kernelCache *v1alpha1.KernelCache,
	nodeGroup *v1alpha1.KernelCacheNodeGroup,
	readyNodes *corev1.NodeList,
	config *v1beta1.KernelCacheConfig,
) error {
	for i := range readyNodes.Items {
		if err := r.ensureOCIPrefetchJob(ctx, kernelCache, nodeGroup, &readyNodes.Items[i], config); err != nil {
			return err
		}
	}
	return nil
}

func (r *KernelCacheReconciler) ensureOCIPrefetchJob(
	ctx context.Context,
	kernelCache *v1alpha1.KernelCache,
	nodeGroup *v1alpha1.KernelCacheNodeGroup,
	node *corev1.Node,
	config *v1beta1.KernelCacheConfig,
) error {
	kernelCacheNode := &v1alpha1.KernelCacheNode{}
	cacheInfo := v1alpha1.KernelCacheNodeCacheInfo{}
	cacheInfoExists := false
	if err := r.Get(ctx, client.ObjectKey{Name: node.Name}, kernelCacheNode); err == nil {
		cacheInfo, cacheInfoExists = kernelCacheNode.Status.CacheStatus[kernelCache.Namespace+"/"+kernelCache.Name]
		if cacheInfoExists && cacheInfo.State == v1alpha1.KernelCacheNodePreparationStateReady &&
			cacheInfo.ImageReference == kernelCache.Spec.Artifact.ImageReference &&
			cacheInfo.Footprints == kernelCache.Spec.Artifact.Identity.Footprints {
			return nil
		}
	} else if apierrors.IsNotFound(err) {
		// Wait for the KCN watch to trigger reconciliation after creation.
		return nil
	} else {
		return err
	}
	jobName := preparationJobName(kernelCache, node, true)
	job := &batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: config.JobNamespace}, job); err == nil {
		// Keep active and terminal Jobs until TTL cleanup. A successful Job can
		// finish before Node.status.images reflects the pulled OCI artifact.
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	labels := kernelCacheLabels(kernelCache)
	labels[kernelCacheNodeLabel] = node.Name
	job = &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: config.JobNamespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: kernelCacheJobTTL(config),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: kernelCachePrefetchServiceAccount,
					NodeSelector:       map[string]string{"kubernetes.io/hostname": nodeHostname(node)},
					Tolerations:        append([]corev1.Toleration(nil), nodeGroup.Spec.Tolerations...),
					Containers: []corev1.Container{{
						Name:            "kernel-cache-prefetch",
						Image:           config.PrefetchImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"/bin/sh", "-c", "test -d /mnt/kernel-cache"},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "kernel-cache",
							MountPath: "/mnt/kernel-cache",
							ReadOnly:  true,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "kernel-cache",
						VolumeSource: corev1.VolumeSource{
							Image: &corev1.ImageVolumeSource{
								Reference:  kernelCache.Spec.Artifact.ImageReference,
								PullPolicy: corev1.PullIfNotPresent,
							},
						},
					}},
				},
			},
		},
	}
	automount := false
	job.Spec.Template.Spec.AutomountServiceAccountToken = &automount

	if err := r.Create(ctx, job); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func kernelCacheLabels(kernelCache *v1alpha1.KernelCache) map[string]string {
	return map[string]string{
		kernelCacheNameLabel:      kernelCache.Name,
		kernelCacheNamespaceLabel: kernelCache.Namespace,
	}
}

func preparationJobName(kernelCache *v1alpha1.KernelCache, node *corev1.Node, nodeSpecific bool) string {
	key := kernelCache.Namespace + "/" + kernelCache.Name + "/" + kernelCache.Spec.Artifact.ImageReference
	if nodeSpecific && node != nil {
		key += "/" + node.Name
	}
	hash := sha256.Sum256([]byte(key))
	return "kc-" + hex.EncodeToString(hash[:])[:24]
}

func kernelCacheJobTTL(config *v1beta1.KernelCacheConfig) *int32 {
	if config.JobTTLSecondsAfterFinished != nil {
		return config.JobTTLSecondsAfterFinished
	}
	ttl := defaultKernelCacheJobTTL
	return &ttl
}

func nodeHostname(node *corev1.Node) string {
	if hostname := node.Labels["kubernetes.io/hostname"]; hostname != "" {
		return hostname
	}
	return node.Name
}

func (r *KernelCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorder("kernelcache-controller")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KernelCache{}).
		Watches(&v1alpha1.KernelCacheCapture{}, handler.EnqueueRequestsFromMapFunc(r.captureToKernelCacheRequests)).
		Watches(&v1beta1.InferenceService{}, handler.EnqueueRequestsFromMapFunc(r.inferenceServiceToKernelCacheRequests)).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.podToKernelCacheRequests), builder.WithPredicates(consumerPodPredicate())).
		Watches(&v1alpha1.KernelCacheNodeGroup{}, handler.EnqueueRequestsFromMapFunc(r.nodeGroupToKernelCaches)).
		Watches(&v1alpha1.KernelCacheNode{}, handler.EnqueueRequestsFromMapFunc(r.kernelCacheNodeToKernelCaches)).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.nodeToKernelCaches)).
		Complete(r)
}

func (r *KernelCacheReconciler) nodeGroupToKernelCaches(ctx context.Context, obj client.Object) []reconcile.Request {
	return mergeReconcileRequests(
		r.kernelCacheRequests(ctx, obj.GetName()),
		r.captureKernelCacheRequests(ctx, ""),
	)
}

func (r *KernelCacheReconciler) nodeToKernelCaches(ctx context.Context, obj client.Object) []reconcile.Request {
	return mergeReconcileRequests(
		r.kernelCacheRequests(ctx, ""),
		r.captureKernelCacheRequests(ctx, obj.GetName()),
	)
}

func (r *KernelCacheReconciler) captureKernelCacheRequests(ctx context.Context, nodeName string) []reconcile.Request {
	captures := &v1alpha1.KernelCacheCaptureList{}
	if err := r.List(ctx, captures); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(captures.Items))
	for index := range captures.Items {
		capture := &captures.Items[index]
		if !isCaptureComplete(capture) || capture.Status.KernelCacheRef != nil {
			continue
		}
		if nodeName != "" && (capture.Status.ActiveSession == nil || capture.Status.ActiveSession.NodeName != nodeName) {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: capture.Namespace,
			Name:      generatedKernelCacheName(capture.Name, capture.Status.Artifact.ImageReference),
		}})
	}
	return requests
}

func mergeReconcileRequests(groups ...[]reconcile.Request) []reconcile.Request {
	seen := map[types.NamespacedName]struct{}{}
	requests := []reconcile.Request{}
	for _, group := range groups {
		for _, request := range group {
			if _, exists := seen[request.NamespacedName]; exists {
				continue
			}
			seen[request.NamespacedName] = struct{}{}
			requests = append(requests, request)
		}
	}
	return requests
}

func (r *KernelCacheReconciler) kernelCacheNodeToKernelCaches(_ context.Context, obj client.Object) []reconcile.Request {
	kernelCacheNode, ok := obj.(*v1alpha1.KernelCacheNode)
	if !ok {
		return nil
	}

	seen := make(map[types.NamespacedName]struct{})
	requests := make([]reconcile.Request, 0, len(kernelCacheNode.Status.CacheStatus))
	for _, cacheInfo := range kernelCacheNode.Status.CacheStatus {
		if cacheInfo.KernelCacheRef.Namespace == "" || cacheInfo.KernelCacheRef.Name == "" {
			continue
		}
		key := types.NamespacedName{
			Namespace: cacheInfo.KernelCacheRef.Namespace,
			Name:      cacheInfo.KernelCacheRef.Name,
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		requests = append(requests, reconcile.Request{NamespacedName: key})
	}
	return requests
}

func (r *KernelCacheReconciler) kernelCacheRequests(ctx context.Context, nodeGroupName string) []reconcile.Request {
	kernelCaches := &v1alpha1.KernelCacheList{}
	if err := r.List(ctx, kernelCaches); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(kernelCaches.Items))
	for i := range kernelCaches.Items {
		kernelCache := &kernelCaches.Items[i]
		if nodeGroupName != "" &&
			(kernelCache.Spec.NodeGroupRef == nil || kernelCache.Spec.NodeGroupRef.Name != nodeGroupName) {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(kernelCache)})
	}
	return requests
}

func (r *KernelCacheReconciler) updateStatus(
	ctx context.Context,
	kernelCache *v1alpha1.KernelCache,
	state v1alpha1.KernelCacheState,
	nodeCount int,
	reason string,
	message string,
	mountType v1alpha1.KernelCacheMountType,
) error {
	return r.updateStatusWithCounts(ctx, kernelCache, state, nodeCount, 0, 0, 0, reason, message, mountType)
}

func (r *KernelCacheReconciler) updateStatusWithCounts(
	ctx context.Context,
	kernelCache *v1alpha1.KernelCache,
	state v1alpha1.KernelCacheState,
	nodeCount int,
	nodesReady int,
	nodesPreparing int,
	nodesError int,
	reason string,
	message string,
	mountType v1alpha1.KernelCacheMountType,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &v1alpha1.KernelCache{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(kernelCache), current); err != nil {
			return err
		}

		desiredStatus := current.Status.DeepCopy()
		if desiredStatus == nil {
			desiredStatus = &v1alpha1.KernelCacheStatus{}
		}
		desiredStatus.State = state
		desiredStatus.MountType = mountType
		desiredStatus.Counts = &v1alpha1.KernelCacheCounts{
			NodeCount:      nodeCount,
			NodesReady:     nodesReady,
			NodesPreparing: nodesPreparing,
			NodesError:     nodesError,
		}
		meta.SetStatusCondition(&desiredStatus.Conditions, metav1.Condition{
			Type:               kernelCacheReadyConditionType,
			Status:             conditionStatusForKernelCacheState(state),
			ObservedGeneration: current.Generation,
			Reason:             reason,
			Message:            message,
		})

		if reflect.DeepEqual(current.Status, *desiredStatus) {
			return nil
		}
		current.Status = *desiredStatus
		return r.Status().Update(ctx, current)
	})
}

func (r *KernelCacheReconciler) removeFinalizer(ctx context.Context, kernelCache *v1alpha1.KernelCache) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &v1alpha1.KernelCache{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(kernelCache), current); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !controllerutil.ContainsFinalizer(current, kernelCacheFinalizerName) {
			return nil
		}
		controllerutil.RemoveFinalizer(current, kernelCacheFinalizerName)
		return r.Update(ctx, current)
	})
}

type kernelCacheAggregate struct {
	State          v1alpha1.KernelCacheState
	NodeCount      int
	NodesReady     int
	NodesPreparing int
	NodesError     int
	Reason         string
	Message        string
}

func (r *KernelCacheReconciler) aggregateKernelCacheStatus(
	ctx context.Context,
	kernelCache *v1alpha1.KernelCache,
	readyNodes *corev1.NodeList,
	notReadyNodes *corev1.NodeList,
) (kernelCacheAggregate, error) {
	expectedNodes := make([]string, 0, len(readyNodes.Items)+len(notReadyNodes.Items))
	for i := range readyNodes.Items {
		expectedNodes = append(expectedNodes, readyNodes.Items[i].Name)
	}
	for i := range notReadyNodes.Items {
		expectedNodes = append(expectedNodes, notReadyNodes.Items[i].Name)
	}

	kernelCacheNodes := &v1alpha1.KernelCacheNodeList{}
	if err := r.List(ctx, kernelCacheNodes); err != nil {
		return kernelCacheAggregate{}, err
	}
	return aggregateKernelCacheNodeStatuses(kernelCache, expectedNodes, kernelCacheNodes), nil
}

func aggregateKernelCacheNodeStatuses(
	kernelCache *v1alpha1.KernelCache,
	expectedNodes []string,
	kernelCacheNodes *v1alpha1.KernelCacheNodeList,
) kernelCacheAggregate {
	cacheKey := kernelCache.Namespace + "/" + kernelCache.Name
	nodeStatuses := make(map[string]v1alpha1.KernelCacheNodeCacheInfo, len(kernelCacheNodes.Items))
	for i := range kernelCacheNodes.Items {
		if cacheInfo, ok := kernelCacheNodes.Items[i].Status.CacheStatus[cacheKey]; ok {
			nodeStatuses[kernelCacheNodes.Items[i].Name] = cacheInfo
		}
	}

	aggregate := kernelCacheAggregate{
		State:     v1alpha1.KernelCacheStatePending,
		NodeCount: len(expectedNodes),
		Reason:    reasonWaitingForPreparation,
		Message:   "waiting for node cache preparation status",
	}
	for _, nodeName := range expectedNodes {
		cacheInfo, exists := nodeStatuses[nodeName]
		if !exists {
			aggregate.NodesPreparing++
			continue
		}

		switch cacheInfo.State {
		case v1alpha1.KernelCacheNodePreparationStateReady:
			aggregate.NodesReady++
		case v1alpha1.KernelCacheNodePreparationStateError:
			aggregate.NodesError++
			if aggregate.Message == "waiting for node cache preparation status" && cacheInfo.Message != "" {
				aggregate.Message = cacheInfo.Message
			}
		default:
			aggregate.NodesPreparing++
		}
	}

	switch {
	case aggregate.NodesError > 0:
		aggregate.State = v1alpha1.KernelCacheStateError
		aggregate.Reason = reasonPreparationFailed
	case aggregate.NodeCount > 0 && aggregate.NodesReady == aggregate.NodeCount:
		aggregate.State = v1alpha1.KernelCacheStateReady
		aggregate.Reason = reasonCacheReady
		aggregate.Message = "cache is ready on all selected nodes"
	case aggregate.NodesReady > 0 || aggregate.NodesPreparing < aggregate.NodeCount:
		aggregate.State = v1alpha1.KernelCacheStatePreparing
		aggregate.Reason = reasonPreparing
		aggregate.Message = "cache preparation is in progress"
	}
	return aggregate
}

func conditionStatusForKernelCacheState(state v1alpha1.KernelCacheState) metav1.ConditionStatus {
	if state == v1alpha1.KernelCacheStateReady {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}
