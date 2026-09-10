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
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/kernelcache/nodegroup"
)

const (
	// Labels identify KernelCache prefetch Jobs and their target node.
	kernelCacheNameLabel      = "serving.kserve.io/kernel-cache-name"
	kernelCacheNamespaceLabel = "serving.kserve.io/kernel-cache-namespace"
	kernelCacheNodeLabel      = "serving.kserve.io/kernel-cache-node"

	// Reconciliation and image validation intervals.
	defaultReconcileInterval = 5 * time.Minute
	cacheImageCheckInterval  = time.Hour
	missingImageMessage      = "OCI artifact is not present in Node.status.images"
)

// KernelCacheNodeReconciler reports cache preparation status for one node.
type KernelCacheNodeReconciler struct {
	client.Client
	Reader   client.Reader // Reads prefetch Pods outside the filtered manager cache.
	NodeName string
	Log      logr.Logger
}

// Reconciliation and periodic image validation.
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

	if err := r.reconcileStatus(ctx, config, false); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	interval := defaultReconcileInterval
	if config.ReconcileIntervalSeconds != nil && *config.ReconcileIntervalSeconds > 0 {
		interval = time.Duration(*config.ReconcileIntervalSeconds) * time.Second
	}
	return ctrl.Result{RequeueAfter: interval}, nil
}

// RunPeriodicImageValidation performs an image check at startup and then at a fixed interval.
func (r *KernelCacheNodeReconciler) RunPeriodicImageValidation(ctx context.Context) error {
	r.runImageValidation(ctx)

	ticker := time.NewTicker(cacheImageCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.runImageValidation(ctx)
		}
	}
}

func (r *KernelCacheNodeReconciler) runImageValidation(ctx context.Context) {
	if err := r.validateNodeImages(ctx); err != nil {
		r.Log.Error(err, "unable to validate KernelCache images", "node", r.NodeName)
	}
}

func (r *KernelCacheNodeReconciler) validateNodeImages(ctx context.Context) error {
	config, err := r.getKernelCacheConfig(ctx)
	if err != nil {
		return err
	}
	if !config.Enabled {
		return nil
	}
	return r.reconcileStatus(ctx, config, true)
}

// Controller setup and event filtering.
func (r *KernelCacheNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	currentNodePredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == r.NodeName
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KernelCacheNode{}, builder.WithPredicates(currentNodePredicate)).
		Watches(&v1alpha1.KernelCache{}, handler.EnqueueRequestsFromMapFunc(r.enqueueCurrentNode)).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.enqueueCurrentNode), builder.WithPredicates(r.currentNodeReadinessPredicate())).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.enqueueCurrentNodeForPod), builder.WithPredicates(r.currentPodPredicate())).
		Watches(&batchv1.Job{}, handler.EnqueueRequestsFromMapFunc(r.enqueueCurrentNodeForJob), builder.WithPredicates(r.currentJobPredicate())).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.enqueueCurrentNodeForConfigMap), builder.WithPredicates(r.inferenceServiceConfigMapPredicate())).
		Complete(r)
}

// Watch predicates and event handlers for the current node.
func (r *KernelCacheNodeReconciler) currentNodeReadinessPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			node, ok := e.Object.(*corev1.Node)
			return ok && node.Name == r.NodeName
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, oldOK := e.ObjectOld.(*corev1.Node)
			newNode, newOK := e.ObjectNew.(*corev1.Node)
			return oldOK && newOK && newNode.Name == r.NodeName &&
				nodegroup.IsNodeReady(*oldNode) != nodegroup.IsNodeReady(*newNode)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
}

func (r *KernelCacheNodeReconciler) enqueueCurrentNode(context.Context, client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: r.NodeName}}}
}

func (r *KernelCacheNodeReconciler) currentJobPredicate() predicate.Funcs {
	return predicate.NewPredicateFuncs(r.isRelevantJob)
}

func (r *KernelCacheNodeReconciler) isRelevantJob(obj client.Object) bool {
	labels := obj.GetLabels()
	if labels[kernelCacheNameLabel] == "" || labels[kernelCacheNamespaceLabel] == "" {
		return false
	}
	if nodeName, ok := labels[kernelCacheNodeLabel]; ok && nodeName != r.NodeName {
		return false
	}
	return true
}

func (r *KernelCacheNodeReconciler) inferenceServiceConfigMapPredicate() predicate.Funcs {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == constants.KServeNamespace && obj.GetName() == constants.InferenceServiceConfigMapName
	})
}

func (r *KernelCacheNodeReconciler) enqueueCurrentNodeForJob(ctx context.Context, obj client.Object) []reconcile.Request {
	if !r.isRelevantJob(obj) {
		return nil
	}
	config, err := r.getKernelCacheConfig(ctx)
	if err != nil || obj.GetNamespace() != config.JobNamespace {
		return nil
	}
	return r.enqueueCurrentNode(ctx, obj)
}

func (r *KernelCacheNodeReconciler) enqueueCurrentNodeForConfigMap(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.enqueueCurrentNode(ctx, obj)
}

func (r *KernelCacheNodeReconciler) currentPodPredicate() predicate.Funcs {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetLabels()[constants.InferenceServicePodLabelKey] != ""
	})
}

func (r *KernelCacheNodeReconciler) enqueueCurrentNodeForPod(_ context.Context, obj client.Object) []reconcile.Request {
	if obj.GetLabels()[constants.InferenceServicePodLabelKey] == "" {
		return nil
	}

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

// Status reconciliation and conflict-safe updates.
func (r *KernelCacheNodeReconciler) reconcileStatus(
	ctx context.Context,
	config *v1beta1.KernelCacheConfig,
	validateImages bool,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kernelCacheNode := &v1alpha1.KernelCacheNode{}
		if err := r.Get(ctx, client.ObjectKey{Name: r.NodeName}, kernelCacheNode); err != nil {
			return err
		}

		oldStatus := kernelCacheNode.Status.DeepCopy()
		podsUsing, needsImageValidation, err := r.discoverCaches(ctx, kernelCacheNode)
		if err != nil {
			return err
		}
		if err := r.updateCacheStatuses(ctx, kernelCacheNode, config, validateImages || needsImageValidation); err != nil {
			return err
		}
		r.updateCounts(kernelCacheNode, podsUsing)

		if oldStatus == nil || !reflect.DeepEqual(*oldStatus, kernelCacheNode.Status) {
			return r.Status().Update(ctx, kernelCacheNode)
		}
		return nil
	})
}

// Configuration and KernelCache discovery.
func (r *KernelCacheNodeReconciler) getKernelCacheConfig(ctx context.Context) (*v1beta1.KernelCacheConfig, error) {
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: constants.KServeNamespace,
		Name:      constants.InferenceServiceConfigMapName,
	}, configMap); err != nil {
		return nil, err
	}
	config, err := v1beta1.NewKernelCacheConfig(configMap)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func (r *KernelCacheNodeReconciler) discoverCaches(ctx context.Context, kernelCacheNode *v1alpha1.KernelCacheNode) (int, bool, error) {
	caches := &v1alpha1.KernelCacheList{}
	if err := r.List(ctx, caches); err != nil {
		return 0, false, err
	}

	if kernelCacheNode.Status.CacheStatus == nil {
		kernelCacheNode.Status.CacheStatus = make(map[string]v1alpha1.KernelCacheNodeCacheInfo)
	}

	activeCaches := make(map[string]struct{}, len(caches.Items))
	podsUsing := make(map[types.UID]struct{})
	needsImageValidation := false
	for i := range caches.Items {
		kernelCache := &caches.Items[i]
		matches, err := r.cacheMatchesNode(ctx, kernelCache)
		if err != nil {
			return 0, false, err
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
			needsImageValidation = true
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
			needsImageValidation = true
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
	return len(podsUsing), needsImageValidation, nil
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

func kernelCacheKey(namespace, name string) string {
	return namespace + "/" + name
}

// Preparation status is derived from Jobs, Pods, and node image inventory.
func (r *KernelCacheNodeReconciler) updateCacheStatuses(
	ctx context.Context,
	kernelCacheNode *v1alpha1.KernelCacheNode,
	config *v1beta1.KernelCacheConfig,
	validateImages bool,
) error {
	var node *corev1.Node
	var nodeLoaded bool
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

		job, err := r.getPreparationJob(ctx, kernelCache, config.JobNamespace)
		if err != nil {
			return err
		}
		podFailureMessage, err := r.getPreparationPodFailure(ctx, job)
		if err != nil {
			return err
		}
		if validateImages && (job == nil || cacheInfo.State == v1alpha1.KernelCacheNodePreparationStateReady) && !nodeLoaded {
			node = &corev1.Node{}
			if err := r.Get(ctx, client.ObjectKey{Name: r.NodeName}, node); err != nil {
				return err
			}
			nodeLoaded = true
		}
		state, message := cachePreparationState(job, cacheInfo, podFailureMessage)
		if validateImages && (job == nil || cacheInfo.State == v1alpha1.KernelCacheNodePreparationStateReady) {
			state, message = cacheImageValidationState(cacheInfo, node)
		}
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
	// Prefetch Pods are not in the ISVC-filtered manager cache.
	reader := client.Reader(r.Client)
	if r.Reader != nil {
		reader = r.Reader
	}
	if err := reader.List(ctx, pods,
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

// Preparation state and status aggregation helpers.
func cachePreparationState(
	job *batchv1.Job,
	cacheInfo v1alpha1.KernelCacheNodeCacheInfo,
	podFailureMessage string,
) (v1alpha1.KernelCacheNodePreparationState, string) {
	return preparationState(job, cacheInfo.State, podFailureMessage)
}

func cacheImageValidationState(
	cacheInfo v1alpha1.KernelCacheNodeCacheInfo,
	node *corev1.Node,
) (v1alpha1.KernelCacheNodePreparationState, string) {
	if nodeContainsImage(node, cacheInfo.ImageReference) {
		return v1alpha1.KernelCacheNodePreparationStateReady, "OCI artifact is present on the node"
	}
	return v1alpha1.KernelCacheNodePreparationStatePending, missingImageMessage
}

func preparationState(
	job *batchv1.Job,
	current v1alpha1.KernelCacheNodePreparationState,
	podFailureMessage string,
) (v1alpha1.KernelCacheNodePreparationState, string) {
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

func nodeContainsImage(node *corev1.Node, imageReference string) bool {
	if node == nil || imageReference == "" {
		return false
	}

	for _, image := range node.Status.Images {
		for _, name := range image.Names {
			if name == imageReference {
				return true
			}
		}
	}
	return false
}

func jobCompleted(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
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
