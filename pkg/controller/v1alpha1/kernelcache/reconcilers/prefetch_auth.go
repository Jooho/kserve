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
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
)

const (
	kernelCachePrefetchServiceAccount = "kernel-cache-prefetcher"
	kernelCachePrefetchRoleBinding    = "kernel-cache-prefetcher"
	kernelCachePrefetchManagedLabel   = "internal.serving.kserve.io/kernelcache-prefetcher"
)

func (r *KernelCacheReconciler) ensurePrefetchIdentity(
	ctx context.Context,
	kernelCache *v1alpha1.KernelCache,
	config *v1beta1.KernelCacheConfig,
) error {
	if config.Registry.Auth.Type != v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken {
		return r.ensurePrefetchServiceAccount(ctx, config.JobNamespace)
	}
	if config.Registry.Auth.PullRoleRef == nil {
		return errors.New("registry pull RoleRef is required for serviceAccountToken authentication")
	}
	if err := r.ensurePrefetchServiceAccount(ctx, config.JobNamespace); err != nil {
		return err
	}

	// The ServiceAccount runs Jobs in JobNamespace, while the RoleBinding must
	// grant pull access in the KernelCache's image source namespace.
	return r.ensurePrefetchImagePullBinding(ctx, kernelCache.Namespace, config.JobNamespace, config.Registry.Auth.PullRoleRef)
}

func (r *KernelCacheReconciler) ensurePrefetchServiceAccount(ctx context.Context, namespace string) error {
	automount := false
	desired := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kernelCachePrefetchServiceAccount,
			Namespace: namespace,
			Labels:    map[string]string{kernelCachePrefetchManagedLabel: "true"},
		},
		AutomountServiceAccountToken: &automount,
	}

	current := &corev1.ServiceAccount{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if current.Labels[kernelCachePrefetchManagedLabel] != "true" {
		return fmt.Errorf("reserved ServiceAccount %s/%s already exists and is not managed by KernelCache", namespace, kernelCachePrefetchServiceAccount)
	}
	if current.AutomountServiceAccountToken == nil || *current.AutomountServiceAccountToken {
		return fmt.Errorf("managed ServiceAccount %s/%s must disable token automount", namespace, kernelCachePrefetchServiceAccount)
	}
	return nil
}

func (r *KernelCacheReconciler) ensurePrefetchImagePullBinding(ctx context.Context, sourceNamespace, jobNamespace string, roleRef *v1beta1.KernelCacheRegistryRoleRef) error {
	desired := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kernelCachePrefetchRoleBinding,
			Namespace: sourceNamespace,
			Labels:    map[string]string{kernelCachePrefetchManagedLabel: "true"},
		},
		RoleRef: kernelCacheRegistryRoleRef(roleRef),
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      kernelCachePrefetchServiceAccount,
			Namespace: jobNamespace,
		}},
	}

	current := &rbacv1.RoleBinding{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if current.Labels[kernelCachePrefetchManagedLabel] != "true" {
		return fmt.Errorf("reserved RoleBinding %s/%s already exists and is not managed by KernelCache", sourceNamespace, kernelCachePrefetchRoleBinding)
	}
	if current.RoleRef != desired.RoleRef || len(current.Subjects) != 1 || current.Subjects[0] != desired.Subjects[0] {
		return fmt.Errorf("managed RoleBinding %s/%s has unexpected role or subject", sourceNamespace, kernelCachePrefetchRoleBinding)
	}
	return nil
}
