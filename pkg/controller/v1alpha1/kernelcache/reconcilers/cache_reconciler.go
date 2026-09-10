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
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kernelcacheconfig "github.com/kserve/kserve/pkg/kernelcache/config"
	"github.com/kserve/kserve/pkg/kernelcache/nodegroup"
)

// KernelCacheReconciler reconciles KernelCache resources.
type KernelCacheReconciler struct {
	client.Client
	Reader                     client.Reader
	Log                        logr.Logger
	Recorder                   events.EventRecorder
	prefetchAuthorizationMutex sync.Mutex
}

const (
	kernelCacheReadyConditionType = "Ready"
	reasonNodeGroupNotFound       = "NodeGroupNotFound"
	reasonNoMatchingNodes         = "NoMatchingNodes"
	reasonNoReadyNodes            = "NoReadyNodes"
	reasonConfigError             = "ConfigError"
	reasonFeatureDisabled         = "FeatureDisabled"
	reasonVerificationFailed      = "VerificationFailed"
	reasonStorageError            = "StorageError"
	reasonWaitingForPreparation   = "WaitingForPreparation"
	reasonPreparing               = "Preparing"
	reasonCacheReady              = "CacheReady"
	reasonPreparationFailed       = "PreparationFailed"
)

func (r *KernelCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	kernelCache := &v1alpha1.KernelCache{}
	if err := r.Get(ctx, req.NamespacedName, kernelCache); err != nil {
		if apierrors.IsNotFound(err) {
			completedCapture, captureErr := r.findCompletedCapture(ctx, req.NamespacedName)
			if captureErr != nil {
				return ctrl.Result{}, captureErr
			}
			if completedCapture == nil {
				config, configErr := kernelcacheconfig.Load(ctx, r.Client)
				if configErr != nil {
					return ctrl.Result{}, configErr
				}
				cleanupReady, cleanupErr := r.cleanupPrefetchRoleBindingAfterKernelCacheDeletion(ctx, req.Namespace, config)
				if cleanupErr != nil {
					return ctrl.Result{}, cleanupErr
				}
				if !cleanupReady {
					return ctrl.Result{RequeueAfter: time.Minute}, nil
				}
			}
			if err := r.reconcileCaptureKernelCache(ctx, req); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !kernelCache.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if err := r.reconcileCaptureKernelCacheRef(ctx, kernelCache); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileKernelCacheUsage(ctx, kernelCache); err != nil {
		return ctrl.Result{}, err
	}

	mountType := kernelCache.Spec.MountType
	kernelCacheConfig, err := kernelcacheconfig.Load(ctx, r.Client)
	if err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonConfigError, err.Error(), mountType)
	}
	if kernelCache.Spec.MountType == "" && kernelCacheConfig.DefaultMountType != "" {
		mountType = v1alpha1.KernelCacheMountTypeOCI
	}
	if mountType != v1alpha1.KernelCacheMountTypeOCI {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonConfigError, fmt.Sprintf("unsupported KernelCache mount type %q", mountType), mountType)
	}
	if !kernelCacheConfig.Enabled {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStatePending, 0, reasonFeatureDisabled, "kernel cache is disabled in inferenceservice-config", mountType)
	}

	// Verify the artifact is valid
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

	// Verify the node group is valid
	// If the node group is not specified, use the default node group
	nodeGroupName := nodegroup.ResolveKernelCacheNodeGroupName(kernelCache, kernelCacheConfig.DefaultNodeGroup)
	if nodeGroupName == "" {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonNodeGroupNotFound, "nodeGroupRef.name is required and default node group is not specified", mountType)
	}

	nodeGroup := &v1alpha1.KernelCacheNodeGroup{}
	if err := r.Get(ctx, client.ObjectKey{Name: nodeGroupName}, nodeGroup); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, 0, reasonNodeGroupNotFound, "referenced KernelCacheNodeGroup was not found", mountType)
		}
		return ctrl.Result{}, err
	}
	readyNodes, notReadyNodes, err := nodegroup.GetNodes(ctx, nodeGroup, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	matchingNodeCount := len(readyNodes.Items) + len(notReadyNodes.Items)
	if matchingNodeCount == 0 {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStatePending, 0, reasonNoMatchingNodes, "no nodes match the referenced KernelCacheNodeGroup", mountType)
	}
	if len(readyNodes.Items) == 0 {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStatePending, 0, reasonNoReadyNodes, "matching nodes are not ready", mountType)
	}

	nodeCount := len(readyNodes.Items)

	if kernelCacheConfig.JobNamespace == "" {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, nodeCount, reasonConfigError, "kernelcache.jobNamespace is required for cache preparation", mountType)
	}
	if err := r.checkNamespace(ctx, kernelCacheConfig.JobNamespace); err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, nodeCount, reasonStorageError, err.Error(), mountType)
	}
	r.prefetchAuthorizationMutex.Lock()
	defer r.prefetchAuthorizationMutex.Unlock()
	prefetchAccessReady, err := r.reconcilePrefetchServiceAccountAccess(ctx, kernelCache, kernelCacheConfig)
	if err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, nodeCount, reasonStorageError, err.Error(), mountType)
	}
	if !prefetchAccessReady {
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	if err := r.ensureOCIPrefetchJobs(ctx, kernelCache, nodeGroup, readyNodes, kernelCacheConfig); err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, nodeCount, reasonStorageError, err.Error(), mountType)
	}

	aggregate, err := r.aggregateKernelCacheStatus(ctx, kernelCache, readyNodes)
	if err != nil {
		return ctrl.Result{}, r.updateStatus(ctx, kernelCache, v1alpha1.KernelCacheStateError, nodeCount, reasonStorageError, err.Error(), mountType)
	}
	return ctrl.Result{}, r.updateStatusWithCounts(ctx, kernelCache, aggregate.State, aggregate.NodeCount, aggregate.NodesReady, aggregate.NodesPreparing, aggregate.NodesError, aggregate.Reason, aggregate.Message, mountType)
}
