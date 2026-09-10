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
	"strings"

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
	if err := r.ensurePrefetchServiceAccount(ctx, config.JobNamespace); err != nil {
		return err
	}

	if config.Registry.Auth.Type != "openshift" {
		return nil
	}

	sourceNamespace, err := imageSourceNamespace(kernelCache.Spec.Artifact.ImageReference, config.Registry.Endpoint)
	if err != nil {
		return err
	}
	return r.ensurePrefetchImagePullBinding(ctx, sourceNamespace, config.JobNamespace)
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

func (r *KernelCacheReconciler) ensurePrefetchImagePullBinding(ctx context.Context, sourceNamespace, jobNamespace string) error {
	desired := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kernelCachePrefetchRoleBinding,
			Namespace: sourceNamespace,
			Labels:    map[string]string{kernelCachePrefetchManagedLabel: "true"},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "system:image-puller",
		},
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

func imageSourceNamespace(imageReference, endpoint string) (string, error) {
	prefix := endpoint + "/"
	if endpoint == "" || !strings.HasPrefix(imageReference, prefix) {
		return "", fmt.Errorf("KernelCache artifact image %q must use registry endpoint %q for OpenShift authentication", imageReference, endpoint)
	}

	path := strings.TrimPrefix(imageReference, prefix)
	separator := strings.IndexByte(path, '/')
	if separator <= 0 {
		return "", fmt.Errorf("KernelCache artifact image %q does not contain an OpenShift namespace", imageReference)
	}
	return path[:separator], nil
}
