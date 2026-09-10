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

// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts;secrets,verbs=create
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;update;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=create
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;patch;update;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=create;get;patch;update;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,resourceNames="kserve-kernelcache-token-requester",verbs=bind
// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcachecaptures,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcachecaptures/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcaches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcachenodes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcachenodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=kernelcachenodegroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments;replicasets,verbs=get
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get
package kernelcache

import "github.com/kserve/kserve/pkg/controller/v1alpha1/kernelcache/reconcilers"

type (
	KernelCacheCaptureReconciler  = reconcilers.KernelCacheCaptureReconciler
	KernelCacheReconciler         = reconcilers.KernelCacheReconciler
	KernelCacheNodeReconciler     = reconcilers.KernelCacheNodeReconciler
	KernelCacheRegistryReconciler = reconcilers.KernelCacheRegistryReconciler
)
