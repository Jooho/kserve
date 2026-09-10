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

// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcaches,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcachenodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcachenodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcachenodegroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch
package kernelcachenode

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/kernelcache/nodegroup"
)

const (
	kernelCacheNameLabel      = "serving.kserve.io/kernel-cache-name"
	kernelCacheNamespaceLabel = "serving.kserve.io/kernel-cache-namespace"
	kernelCacheNodeLabel      = "serving.kserve.io/kernel-cache-node"
	defaultReconcileInterval  = time.Minute
)

// KernelCacheNodeReconciler reports cache preparation status for one node.
type KernelCacheNodeReconciler struct {
	client.Client
	NodeName string
	Log      logr.Logger
}

func (r *KernelCacheNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.NodeName == "" {
		return ctrl.Result{}, errors.New("node name is required")
	}
	if req.Name != r.NodeName {
		return ctrl.Result{}, nil
	}

	kernelCacheNode := &v1alpha1.KernelCacheNode{}
	if err := r.Get(ctx, client.ObjectKey{Name: r.NodeName}, kernelCacheNode); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	config, err := r.getKernelCacheConfig(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !config.Enabled {
		return ctrl.Result{}, nil
	}

	oldStatus := kernelCacheNode.Status.DeepCopy()
	podsUsing, err := r.discoverCaches(ctx, kernelCacheNode)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.updateCacheStatuses(ctx, kernelCacheNode, config); err != nil {
		return ctrl.Result{}, err
	}
	r.updateCounts(kernelCacheNode, podsUsing)

	if oldStatus == nil || !reflect.DeepEqual(*oldStatus, kernelCacheNode.Status) {
		if err := r.Status().Update(ctx, kernelCacheNode); err != nil {
			return ctrl.Result{}, err
		}
	}

	interval := defaultReconcileInterval
	if config.ReconcileIntervalSeconds != nil && *config.ReconcileIntervalSeconds > 0 {
		interval = time.Duration(*config.ReconcileIntervalSeconds) * time.Second
	}
	return ctrl.Result{RequeueAfter: interval}, nil
}

func (r *KernelCacheNodeReconciler) getKernelCacheConfig(ctx context.Context) (*v1beta1.KernelCacheConfig, error) {
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: constants.KServeNamespace,
		Name:      constants.InferenceServiceConfigMapName,
	}, configMap); err != nil {
		return nil, err
	}
	return v1beta1.NewKernelCacheConfig(configMap)
}

func (r *KernelCacheNodeReconciler) discoverCaches(ctx context.Context, kernelCacheNode *v1alpha1.KernelCacheNode) (int, error) {
	caches := &v1alpha1.KernelCacheList{}
	if err := r.List(ctx, caches); err != nil {
		return 0, err
	}

	if kernelCacheNode.Status.CacheStatus == nil {
		kernelCacheNode.Status.CacheStatus = make(map[string]v1alpha1.KernelCacheNodeCacheInfo)
	}

	activeCaches := make(map[string]struct{}, len(caches.Items))
	podsUsing := make(map[types.UID]struct{})
	for i := range caches.Items {
		kernelCache := &caches.Items[i]
		matches, err := r.cacheMatchesNode(ctx, kernelCache)
		if err != nil {
			return 0, err
		}
		if !matches {
			continue
		}
		if usage := kernelCache.Status.Usage; usage != nil {
			for _, podUsage := range usage.Pods {
				if podUsage.NodeName == r.NodeName && podUsage.PodUID != "" {
					podsUsing[podUsage.PodUID] = struct{}{}
				}
			}
		}

		cacheKey := kernelCacheKey(kernelCache.Namespace, kernelCache.Name)
		activeCaches[cacheKey] = struct{}{}
		cacheInfo, exists := kernelCacheNode.Status.CacheStatus[cacheKey]
		if !exists {
			cacheInfo = v1alpha1.KernelCacheNodeCacheInfo{
				KernelCacheRef: v1alpha1.NamespacedName{
					Namespace: kernelCache.Namespace,
					Name:      kernelCache.Name,
				},
				State:      v1alpha1.KernelCacheNodePreparationStatePending,
				LastUpdate: metav1.Now(),
			}
		}

		cacheInfo.KernelCacheRef = v1alpha1.NamespacedName{
			Namespace: kernelCache.Namespace,
			Name:      kernelCache.Name,
		}
		if cacheInfo.ImageReference != kernelCache.Spec.Artifact.ImageReference || cacheInfo.Footprints != kernelCache.Spec.Artifact.Identity.Footprints {
			cacheInfo.ImageReference = kernelCache.Spec.Artifact.ImageReference
			cacheInfo.Footprints = kernelCache.Spec.Artifact.Identity.Footprints
			cacheInfo.State = v1alpha1.KernelCacheNodePreparationStatePending
			cacheInfo.Message = "waiting for the preparation Job"
			cacheInfo.LastUpdate = metav1.Now()
		}
		kernelCacheNode.Status.CacheStatus[cacheKey] = cacheInfo
	}

	for cacheKey := range kernelCacheNode.Status.CacheStatus {
		if _, exists := activeCaches[cacheKey]; !exists {
			delete(kernelCacheNode.Status.CacheStatus, cacheKey)
		}
	}
	return len(podsUsing), nil
}

func (r *KernelCacheNodeReconciler) cacheMatchesNode(ctx context.Context, kernelCache *v1alpha1.KernelCache) (bool, error) {
	if kernelCache.Spec.NodeGroupRef == nil || kernelCache.Spec.NodeGroupRef.Name == "" {
		return false, nil
	}

	nodeGroup := &v1alpha1.KernelCacheNodeGroup{}
	if err := r.Get(ctx, client.ObjectKey{Name: kernelCache.Spec.NodeGroupRef.Name}, nodeGroup); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	readyNodes, _, err := nodegroup.GetNodes(ctx, nodeGroup, r.Client)
	if err != nil {
		return false, err
	}
	for i := range readyNodes.Items {
		if readyNodes.Items[i].Name == r.NodeName {
			return true, nil
		}
	}
	return false, nil
}

func (r *KernelCacheNodeReconciler) updateCacheStatuses(
	ctx context.Context,
	kernelCacheNode *v1alpha1.KernelCacheNode,
	config *v1beta1.KernelCacheConfig,
) error {
	for cacheKey, cacheInfo := range kernelCacheNode.Status.CacheStatus {
		kernelCache := &v1alpha1.KernelCache{}
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: cacheInfo.KernelCacheRef.Namespace,
			Name:      cacheInfo.KernelCacheRef.Name,
		}, kernelCache); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}

		mountType := effectiveMountType(kernelCache)
		if kernelCache.Spec.MountType == "" && config.DefaultMountType != "" {
			mountType = v1alpha1.KernelCacheMountType(config.DefaultMountType)
		}
		job, err := r.getPreparationJob(ctx, kernelCache, config.JobNamespace)
		if err != nil {
			return err
		}
		podFailureMessage, err := r.getPreparationPodFailure(ctx, job)
		if err != nil {
			return err
		}
		var pvc *corev1.PersistentVolumeClaim
		if mountType == v1alpha1.KernelCacheMountTypePVC {
			pvc, err = r.getDownloadPVC(ctx, kernelCache, config.JobNamespace)
			if err != nil {
				return err
			}
		}

		state, message := preparationStateForMountType(job, pvc, cacheInfo.State, podFailureMessage, mountType)
		if state != cacheInfo.State || message != cacheInfo.Message {
			cacheInfo.State = state
			cacheInfo.Message = message
			cacheInfo.LastUpdate = metav1.Now()
		}
		kernelCacheNode.Status.CacheStatus[cacheKey] = cacheInfo
	}
	return nil
}

func (r *KernelCacheNodeReconciler) getPreparationJob(
	ctx context.Context,
	kernelCache *v1alpha1.KernelCache,
	jobNamespace string,
) (*batchv1.Job, error) {
	if jobNamespace == "" {
		return nil, errors.New("kernelcache.jobNamespace is required")
	}

	jobs := &batchv1.JobList{}
	if err := r.List(ctx, jobs,
		client.InNamespace(jobNamespace),
		client.MatchingLabels{
			kernelCacheNameLabel:      kernelCache.Name,
			kernelCacheNamespaceLabel: kernelCache.Namespace,
		},
	); err != nil {
		return nil, err
	}

	var latest *batchv1.Job
	for i := range jobs.Items {
		job := &jobs.Items[i]
		// Ignore prefetch results for a previous artifact of the same cache.
		staleArtifact := false
		for _, volume := range job.Spec.Template.Spec.Volumes {
			if volume.Image != nil && volume.Image.Reference != kernelCache.Spec.Artifact.ImageReference {
				staleArtifact = true
				break
			}
		}
		if staleArtifact {
			continue
		}
		if nodeName, ok := job.Labels[kernelCacheNodeLabel]; ok && nodeName != r.NodeName {
			continue
		}
		if latest == nil || job.CreationTimestamp.After(latest.CreationTimestamp.Time) {
			latest = job
		}
	}
	return latest, nil
}

func (r *KernelCacheNodeReconciler) getPreparationPodFailure(ctx context.Context, job *batchv1.Job) (string, error) {
	if job == nil || jobCompleted(job) || failedJobMessage(job) != "" {
		return "", nil
	}

	pods := &corev1.PodList{}
	if err := r.List(ctx, pods,
		client.InNamespace(job.Namespace),
		client.MatchingLabels{batchv1.JobNameLabel: job.Name},
	); err != nil {
		return "", err
	}

	for i := range pods.Items {
		if message := failedPodMessage(&pods.Items[i]); message != "" {
			return message, nil
		}
	}
	return "", nil
}

func (r *KernelCacheNodeReconciler) getDownloadPVC(
	ctx context.Context,
	kernelCache *v1alpha1.KernelCache,
	jobNamespace string,
) (*corev1.PersistentVolumeClaim, error) {
	if jobNamespace == "" {
		return nil, errors.New("kernelcache.jobNamespace is required")
	}

	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcs,
		client.InNamespace(jobNamespace),
		client.MatchingLabels{
			kernelCacheNameLabel:      kernelCache.Name,
			kernelCacheNamespaceLabel: kernelCache.Namespace,
			kernelCacheNodeLabel:      r.NodeName,
		},
	); err != nil {
		return nil, err
	}
	if len(pvcs.Items) == 0 {
		return nil, nil
	}
	return &pvcs.Items[0], nil
}

func preparationState(
	kernelCache *v1alpha1.KernelCache,
	job *batchv1.Job,
	pvc *corev1.PersistentVolumeClaim,
	current v1alpha1.KernelCacheNodePreparationState,
	podFailureMessage string,
) (v1alpha1.KernelCacheNodePreparationState, string) {
	return preparationStateForMountType(job, pvc, current, podFailureMessage, effectiveMountType(kernelCache))
}

func preparationStateForMountType(
	job *batchv1.Job,
	pvc *corev1.PersistentVolumeClaim,
	current v1alpha1.KernelCacheNodePreparationState,
	podFailureMessage string,
	mountType v1alpha1.KernelCacheMountType,
) (v1alpha1.KernelCacheNodePreparationState, string) {
	if mountType == v1alpha1.KernelCacheMountTypeOCI {
		if job != nil {
			if message := failedJobMessage(job); message != "" {
				return v1alpha1.KernelCacheNodePreparationStateError, message
			}
			if jobCompleted(job) {
				return v1alpha1.KernelCacheNodePreparationStateReady, "OCI artifact was prefetched on the node"
			}
			if podFailureMessage != "" {
				return v1alpha1.KernelCacheNodePreparationStateError, podFailureMessage
			}
			if job.Status.StartTime == nil && job.Status.Active == 0 {
				return v1alpha1.KernelCacheNodePreparationStatePulling, "waiting for the OCI prefetch Job to start"
			}
			return v1alpha1.KernelCacheNodePreparationStatePulling, "OCI artifact is being prefetched"
		}
		if current == v1alpha1.KernelCacheNodePreparationStateReady {
			return current, "OCI artifact was prefetched on the node"
		}
		return v1alpha1.KernelCacheNodePreparationStatePending, "waiting for the OCI prefetch Job"
	}
	if job != nil {
		if message := failedJobMessage(job); message != "" {
			return v1alpha1.KernelCacheNodePreparationStateError, message
		}
		if jobCompleted(job) {
			if pvc != nil && pvc.Status.Phase == corev1.ClaimBound {
				return v1alpha1.KernelCacheNodePreparationStateReady, ""
			}
			return v1alpha1.KernelCacheNodePreparationStatePulling, "waiting for the download PVC to bind"
		}
		if podFailureMessage != "" {
			return v1alpha1.KernelCacheNodePreparationStateError, podFailureMessage
		}
		if job.Status.StartTime == nil && job.Status.Active == 0 {
			return v1alpha1.KernelCacheNodePreparationStatePulling, "waiting for the preparation Job to start"
		}
		return v1alpha1.KernelCacheNodePreparationStateExtracting, "preparation Job is running"
	}

	if pvc == nil {
		return v1alpha1.KernelCacheNodePreparationStatePending, "waiting for the preparation Job"
	}
	if pvc.Status.Phase != corev1.ClaimBound {
		return v1alpha1.KernelCacheNodePreparationStatePending, "waiting for the download PVC to bind"
	}
	if current == v1alpha1.KernelCacheNodePreparationStateReady {
		return current, ""
	}
	return v1alpha1.KernelCacheNodePreparationStatePending, "waiting for the preparation Job"
}

func effectiveMountType(kernelCache *v1alpha1.KernelCache) v1alpha1.KernelCacheMountType {
	if kernelCache.Spec.MountType == "" {
		return v1alpha1.KernelCacheMountTypePVC
	}
	return kernelCache.Spec.MountType
}

func failedJobMessage(job *batchv1.Job) string {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			if condition.Message != "" {
				return condition.Message
			}
			if condition.Reason != "" {
				return condition.Reason
			}
			return "preparation Job failed"
		}
	}
	return ""
}

func failedPodMessage(pod *corev1.Pod) string {
	statuses := append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
	for _, status := range statuses {
		if waiting := status.State.Waiting; waiting != nil && isPreparationFailureReason(waiting.Reason) {
			if waiting.Message != "" {
				return waiting.Message
			}
			if waiting.Reason != "" {
				return waiting.Reason
			}
		}
		if terminated := status.State.Terminated; terminated != nil && terminated.ExitCode != 0 {
			if terminated.Message != "" {
				return terminated.Message
			}
			if terminated.Reason != "" {
				return terminated.Reason
			}
			return fmt.Sprintf("preparation container exited with code %d", terminated.ExitCode)
		}
	}

	if pod.Status.Message != "" {
		return pod.Status.Message
	}
	if pod.Status.Reason != "" {
		return pod.Status.Reason
	}
	if pod.Status.Phase == corev1.PodFailed {
		return "preparation Pod failed"
	}

	return ""
}

func isPreparationFailureReason(reason string) bool {
	switch reason {
	case "ErrImagePull", "ImagePullBackOff", "ErrImageNeverPull", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError", "RunContainerError", "ContainerCannotRun", "CrashLoopBackOff":
		return true
	default:
		return false
	}
}

func jobCompleted(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (r *KernelCacheNodeReconciler) updateCounts(kernelCacheNode *v1alpha1.KernelCacheNode, podsUsing int) {
	counts := &v1alpha1.KernelCacheNodeCounts{}
	for _, cacheInfo := range kernelCacheNode.Status.CacheStatus {
		switch cacheInfo.State {
		case v1alpha1.KernelCacheNodePreparationStateReady:
			counts.CachesReady++
		case v1alpha1.KernelCacheNodePreparationStateError:
			counts.CachesError++
		default:
			counts.CachesPreparing++
		}
	}
	counts.TotalPodsUsing = podsUsing
	kernelCacheNode.Status.Counts = counts
}

func kernelCacheKey(namespace, name string) string {
	return namespace + "/" + name
}

func (r *KernelCacheNodeReconciler) enqueueCurrentNode(context.Context, client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: r.NodeName}}}
}

func (r *KernelCacheNodeReconciler) enqueueCurrentNodeForPod(_ context.Context, obj client.Object) []reconcile.Request {
	if obj.GetAnnotations()[constants.KernelCacheUsageAnnotationKey] != "" {
		pod, ok := obj.(*corev1.Pod)
		if !ok || pod.Spec.NodeName != r.NodeName {
			return nil
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: r.NodeName}}}
	}

	labels := obj.GetLabels()
	if labels[kernelCacheNameLabel] == "" || labels[kernelCacheNamespaceLabel] == "" {
		return nil
	}
	if nodeName, ok := labels[kernelCacheNodeLabel]; ok && nodeName != r.NodeName {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: r.NodeName}}}
}

func (r *KernelCacheNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	currentNodePredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == r.NodeName
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KernelCacheNode{}, builder.WithPredicates(currentNodePredicate)).
		Watches(&v1alpha1.KernelCache{}, handler.EnqueueRequestsFromMapFunc(r.enqueueCurrentNode)).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.enqueueCurrentNodeForPod)).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(r.enqueueCurrentNode)).
		Watches(&batchv1.Job{}, handler.EnqueueRequestsFromMapFunc(r.enqueueCurrentNode)).
		Complete(r)
}
