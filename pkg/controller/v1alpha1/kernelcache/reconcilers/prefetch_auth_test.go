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
	"reflect"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	kernelcachelabels "github.com/kserve/kserve/pkg/kernelcache/labels"
)

func TestReconcilePrefetchServiceAccountAccessSplitsJobAndSourceNamespaces(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{
		JobNamespace: "jobs",
		Registry: v1beta1.KernelCacheRegistryConfig{
			Endpoint: "registry.example/source",
			Auth: v1beta1.KernelCacheRegistryAuth{
				Type:        v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken,
				PushRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-pusher"},
				PullRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"},
			},
		},
	}

	ready, err := r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("expected prefetch access to be ready")
	}

	serviceAccount := &corev1.ServiceAccount{}
	if err := cl.Get(t.Context(), client.ObjectKey{Namespace: "jobs", Name: kernelCachePrefetchServiceAccount}, serviceAccount); err != nil {
		t.Fatal(err)
	}
	if serviceAccount.Labels[kernelCachePrefetchManagedLabel] != "true" {
		t.Fatalf("prefetch ServiceAccount is not managed: %#v", serviceAccount.Labels)
	}

	binding := &rbacv1.RoleBinding{}
	if err := cl.Get(t.Context(), client.ObjectKey{Namespace: "source", Name: kernelCachePrefetchRoleBinding}, binding); err != nil {
		t.Fatal(err)
	}
	if binding.RoleRef.Name != "registry-puller" {
		t.Fatalf("unexpected RoleRef: %#v", binding.RoleRef)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Namespace != "jobs" || binding.Subjects[0].Name != kernelCachePrefetchServiceAccount {
		t.Fatalf("unexpected RoleBinding subjects: %#v", binding.Subjects)
	}

	bindings := &rbacv1.RoleBindingList{}
	if err := cl.List(t.Context(), bindings); err != nil {
		t.Fatal(err)
	}
	if len(bindings.Items) != 1 || bindings.Items[0].Namespace != "source" {
		t.Fatalf("expected only the image source namespace binding, got %#v", bindings.Items)
	}
}

func TestReconcilePrefetchServiceAccountAccessRepairsSubjectNamespaceDrift(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, rbacv1.AddToScheme, batchv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	binding := buildPrefetchImagePullRoleBinding("source", "old-jobs", &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"})
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(binding).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{
		JobNamespace: "jobs",
		Registry: v1beta1.KernelCacheRegistryConfig{Auth: v1beta1.KernelCacheRegistryAuth{
			Type:        v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken,
			PullRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"},
		}},
	}

	ready, err := r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("expected prefetch access to repair namespace drift")
	}

	updated := &rbacv1.RoleBinding{}
	if err := cl.Get(t.Context(), client.ObjectKeyFromObject(binding), updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Subjects) != 1 || updated.Subjects[0].Namespace != "jobs" {
		t.Fatalf("expected subject namespace to be repaired, got %#v", updated.Subjects)
	}
}

func TestReconcilePrefetchServiceAccountAccessDefersSubjectNamespaceRepairWhileJobIsNonTerminal(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, rbacv1.AddToScheme, batchv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	binding := buildPrefetchImagePullRoleBinding("source", "old-jobs", &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"})
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name:      "prefetch-job",
		Namespace: "jobs",
		Labels:    kernelcachelabels.ObjectMeta("cache", "source", "node").Labels,
	}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(job).WithObjects(binding, job).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{
		JobNamespace: "jobs",
		Registry: v1beta1.KernelCacheRegistryConfig{Auth: v1beta1.KernelCacheRegistryAuth{
			Type:        v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken,
			PullRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"},
		}},
	}

	ready, err := r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("expected namespace repair to wait for the active prefetch Job")
	}

	updatedBinding := &rbacv1.RoleBinding{}
	if err := cl.Get(t.Context(), client.ObjectKeyFromObject(binding), updatedBinding); err != nil {
		t.Fatal(err)
	}
	if updatedBinding.Subjects[0].Namespace != "old-jobs" {
		t.Fatalf("active Job binding was changed: %#v", updatedBinding.Subjects)
	}

	updatedJob := &batchv1.Job{}
	if err := cl.Get(t.Context(), client.ObjectKeyFromObject(job), updatedJob); err != nil {
		t.Fatal(err)
	}
	updatedJob.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	if err := cl.Status().Update(t.Context(), updatedJob); err != nil {
		t.Fatal(err)
	}

	ready, err = r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("expected namespace repair after the prefetch Job completed")
	}
	if err := cl.Get(t.Context(), client.ObjectKeyFromObject(binding), updatedBinding); err != nil {
		t.Fatal(err)
	}
	if updatedBinding.Subjects[0].Namespace != "jobs" {
		t.Fatalf("expected repaired subject namespace, got %#v", updatedBinding.Subjects)
	}
}

func TestReconcilePrefetchServiceAccountAccessRejectsUnexpectedManagedSubjects(t *testing.T) {
	tests := []struct {
		name     string
		subjects []rbacv1.Subject
	}{
		{
			name:     "different service account name",
			subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "other", Namespace: "jobs"}},
		},
		{
			name:     "different subject kind",
			subjects: []rbacv1.Subject{{Kind: "User", Name: kernelCachePrefetchServiceAccount, Namespace: "jobs"}},
		},
		{
			name: "multiple subjects",
			subjects: []rbacv1.Subject{
				{Kind: "ServiceAccount", Name: kernelCachePrefetchServiceAccount, Namespace: "jobs"},
				{Kind: "ServiceAccount", Name: "other", Namespace: "jobs"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, rbacv1.AddToScheme} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}

			binding := buildPrefetchImagePullRoleBinding("source", "jobs", &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"})
			binding.Subjects = test.subjects
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(binding).Build()
			r := &KernelCacheReconciler{Client: cl}
			kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
			config := &v1beta1.KernelCacheConfig{
				JobNamespace: "jobs",
				Registry: v1beta1.KernelCacheRegistryConfig{Auth: v1beta1.KernelCacheRegistryAuth{
					Type:        v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken,
					PullRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"},
				}},
			}

			ready, err := r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
			if err == nil {
				t.Fatal("expected unexpected managed subject to fail closed")
			}
			if ready {
				t.Fatal("expected prefetch access to remain unavailable")
			}

			updated := &rbacv1.RoleBinding{}
			if err := cl.Get(t.Context(), client.ObjectKeyFromObject(binding), updated); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(updated.Subjects, test.subjects) {
				t.Fatalf("unexpected managed subject was modified: %#v", updated.Subjects)
			}
		})
	}
}

func TestReconcilePrefetchServiceAccountAccessUsesKernelCacheNamespaceForSourceBinding(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{JobNamespace: "jobs"}

	ready, err := r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("expected prefetch access to be ready")
	}

	serviceAccounts := &corev1.ServiceAccountList{}
	if err := cl.List(t.Context(), serviceAccounts); err != nil {
		t.Fatal(err)
	}
	if len(serviceAccounts.Items) != 1 || serviceAccounts.Items[0].Namespace != "jobs" {
		t.Fatalf("expected one prefetch ServiceAccount in jobs, got %#v", serviceAccounts.Items)
	}
}

func TestPrefetchAccessRequestsEnqueueAllKernelCaches(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache-a", Namespace: "team-a"}},
		&v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache-b", Namespace: "team-b"}},
	).Build()
	r := &KernelCacheReconciler{Client: cl}

	requests := r.enqueueKCsOnPrefetchAuthChange(t.Context(), &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
		Name: kernelCachePrefetchRoleBinding, Namespace: "team-a",
	}})
	if len(requests) != 2 {
		t.Fatalf("expected two KernelCache requests, got %#v", requests)
	}
}

func TestCleanupPrefetchRoleBindingAfterKernelCacheDeletion(t *testing.T) {
	tests := []struct {
		name            string
		objects         []client.Object
		wantReady       bool
		wantRoleBinding bool
	}{
		{
			name:            "deletes an unused binding",
			wantReady:       true,
			wantRoleBinding: false,
		},
		{
			name: "keeps a binding used by another KernelCache",
			objects: []client.Object{
				&v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "other-cache", Namespace: "source"}},
			},
			wantReady:       true,
			wantRoleBinding: true,
		},
		{
			name: "defers deletion while a prefetch Job is active",
			objects: []client.Object{
				&batchv1.Job{ObjectMeta: metav1.ObjectMeta{
					Name:      "prefetch-job",
					Namespace: "jobs",
					Labels:    kernelcachelabels.ObjectMeta("cache", "source", "node").Labels,
				}},
			},
			wantReady:       false,
			wantRoleBinding: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, batchv1.AddToScheme, rbacv1.AddToScheme} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}

			binding := buildPrefetchImagePullRoleBinding("source", "jobs", &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"})
			binding.UID = "binding-uid"
			objects := append([]client.Object{binding}, test.objects...)
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			r := &KernelCacheReconciler{Client: cl}
			config := &v1beta1.KernelCacheConfig{JobNamespace: "jobs"}

			ready, err := r.cleanupPrefetchRoleBindingAfterKernelCacheDeletion(t.Context(), "source", config)
			if err != nil {
				t.Fatal(err)
			}
			if ready != test.wantReady {
				t.Fatalf("expected ready=%t, got %t", test.wantReady, ready)
			}

			remaining := &rbacv1.RoleBinding{}
			err = cl.Get(t.Context(), client.ObjectKey{Namespace: "source", Name: kernelCachePrefetchRoleBinding}, remaining)
			if test.wantRoleBinding && err != nil {
				t.Fatalf("expected RoleBinding to remain: %v", err)
			}
			if !test.wantRoleBinding && !apierrors.IsNotFound(err) {
				t.Fatalf("expected RoleBinding to be deleted, got %v", err)
			}
		})
	}
}

func TestReconcilePrefetchServiceAccountAccessDefersRoleRefChangeWhileJobIsNonTerminal(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	oldBinding := buildPrefetchImagePullRoleBinding("source", "jobs", &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller-v1"})
	oldBinding.UID = "binding-uid"
	oldBinding.ResourceVersion = "1"
	jobMetadata := kernelcachelabels.ObjectMeta("cache", "source", "node")
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name:      "prefetch-job",
		Namespace: "jobs",
		Labels:    jobMetadata.Labels,
	}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Name:      kernelCachePrefetchServiceAccount,
			Namespace: "jobs",
			Labels:    map[string]string{kernelCachePrefetchManagedLabel: "true"},
		}, AutomountServiceAccountToken: boolPointer(false)},
		oldBinding,
		job,
	).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{
		JobNamespace: "jobs",
		Registry: v1beta1.KernelCacheRegistryConfig{Auth: v1beta1.KernelCacheRegistryAuth{
			Type:        v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken,
			PullRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller-v2"},
		}},
	}

	ready, err := r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("expected prefetch access rotation to wait for the active Job")
	}

	binding := &rbacv1.RoleBinding{}
	if err := cl.Get(t.Context(), client.ObjectKey{Namespace: "source", Name: kernelCachePrefetchRoleBinding}, binding); err != nil {
		t.Fatal(err)
	}
	if binding.RoleRef.Name != "registry-puller-v1" {
		t.Fatalf("active Job binding was changed: %#v", binding.RoleRef)
	}
}

func TestReconcilePrefetchServiceAccountAccessRotatesRoleRefAfterJobsFinish(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	oldBinding := buildPrefetchImagePullRoleBinding("source", "jobs", &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller-v1"})
	oldBinding.UID = "binding-uid"
	oldBinding.ResourceVersion = "1"
	jobMetadata := kernelcachelabels.ObjectMeta("cache", "source", "node")
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "prefetch-job", Namespace: "jobs", Labels: jobMetadata.Labels},
		Status:     batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete}}},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Name:      kernelCachePrefetchServiceAccount,
			Namespace: "jobs",
			Labels:    map[string]string{kernelCachePrefetchManagedLabel: "true"},
		}, AutomountServiceAccountToken: boolPointer(false)},
		oldBinding,
		job,
	).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{
		JobNamespace: "jobs",
		Registry: v1beta1.KernelCacheRegistryConfig{Auth: v1beta1.KernelCacheRegistryAuth{
			Type:        v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken,
			PullRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller-v2"},
		}},
	}

	ready, err := r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("expected prefetch access rotation to complete after the Job finished")
	}

	binding := &rbacv1.RoleBinding{}
	if err := cl.Get(t.Context(), client.ObjectKey{Namespace: "source", Name: kernelCachePrefetchRoleBinding}, binding); err != nil {
		t.Fatal(err)
	}
	if binding.RoleRef.Name != "registry-puller-v2" {
		t.Fatalf("unexpected rotated RoleRef: %#v", binding.RoleRef)
	}
	bindings := &rbacv1.RoleBindingList{}
	if err := cl.List(t.Context(), bindings); err != nil {
		t.Fatal(err)
	}
	if len(bindings.Items) != 1 {
		t.Fatalf("expected only the canonical RoleBinding, got %#v", bindings.Items)
	}
}

func TestReconcilePrefetchServiceAccountAccessPreservesBindingWhenReplacementValidationFails(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	oldBinding := buildPrefetchImagePullRoleBinding("source", "jobs", &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller-v1"})
	oldBinding.UID = "binding-uid"
	oldBinding.ResourceVersion = "1"
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Name:      kernelCachePrefetchServiceAccount,
			Namespace: "jobs",
			Labels:    map[string]string{kernelCachePrefetchManagedLabel: "true"},
		}, AutomountServiceAccountToken: boolPointer(false)},
		oldBinding,
	).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, object client.Object, options ...client.CreateOption) error {
			if _, ok := object.(*rbacv1.RoleBinding); ok {
				createOptions := client.CreateOptions{}
				for _, option := range options {
					option.ApplyToCreate(&createOptions)
				}
				if len(createOptions.DryRun) > 0 {
					return errors.New("replacement RoleBinding is forbidden")
				}
			}
			return c.Create(ctx, object, options...)
		},
	}).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{
		JobNamespace: "jobs",
		Registry: v1beta1.KernelCacheRegistryConfig{Auth: v1beta1.KernelCacheRegistryAuth{
			Type:        v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken,
			PullRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller-v2"},
		}},
	}

	ready, err := r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
	if err == nil {
		t.Fatal("expected replacement validation to fail")
	}
	if ready {
		t.Fatal("expected prefetch access to remain unavailable after validation failure")
	}

	binding := &rbacv1.RoleBinding{}
	if err := cl.Get(t.Context(), client.ObjectKey{Namespace: "source", Name: kernelCachePrefetchRoleBinding}, binding); err != nil {
		t.Fatal(err)
	}
	if binding.RoleRef.Name != "registry-puller-v1" {
		t.Fatalf("old RoleBinding was removed after validation failure: %#v", binding.RoleRef)
	}
}

func TestReconcilePrefetchServiceAccountAccessRemovesBindingAfterAuthDisabled(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	binding := buildPrefetchImagePullRoleBinding("source", "jobs", &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"})
	binding.UID = "binding-uid"
	binding.ResourceVersion = "1"
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Name:      kernelCachePrefetchServiceAccount,
			Namespace: "jobs",
			Labels:    map[string]string{kernelCachePrefetchManagedLabel: "true"},
		}, AutomountServiceAccountToken: boolPointer(false)},
		binding,
	).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{JobNamespace: "jobs"}

	ready, err := r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("expected cleanup to complete without active Jobs")
	}
	if err := cl.Get(t.Context(), client.ObjectKey{Namespace: "source", Name: kernelCachePrefetchRoleBinding}, &rbacv1.RoleBinding{}); err == nil {
		t.Fatal("expected the managed pull RoleBinding to be removed")
	}
}

func TestReconcilePrefetchServiceAccountAccessDefersBindingRemovalWhileJobIsNonTerminal(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	binding := buildPrefetchImagePullRoleBinding("source", "jobs", &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"})
	binding.UID = "binding-uid"
	binding.ResourceVersion = "1"
	jobMetadata := kernelcachelabels.ObjectMeta("cache", "source", "node")
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name:      "prefetch-job",
		Namespace: "jobs",
		Labels:    jobMetadata.Labels,
	}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Name:      kernelCachePrefetchServiceAccount,
			Namespace: "jobs",
			Labels:    map[string]string{kernelCachePrefetchManagedLabel: "true"},
		}, AutomountServiceAccountToken: boolPointer(false)},
		binding,
		job,
	).Build()
	r := &KernelCacheReconciler{Client: cl}
	kernelCache := &v1alpha1.KernelCache{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "source"}}
	config := &v1beta1.KernelCacheConfig{JobNamespace: "jobs"}

	ready, err := r.reconcilePrefetchServiceAccountAccess(t.Context(), kernelCache, config)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("expected binding removal to wait for the active Job")
	}

	current := &rbacv1.RoleBinding{}
	if err := cl.Get(t.Context(), client.ObjectKey{Namespace: "source", Name: kernelCachePrefetchRoleBinding}, current); err != nil {
		t.Fatal(err)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
