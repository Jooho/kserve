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
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/kernelcache/registryauth"
	"github.com/kserve/kserve/pkg/kernelcache/reporter"
)

// KernelCacheRegistryReconciler prepares publishing identities in workload namespaces.
type KernelCacheRegistryReconciler struct {
	client.Client
	Clientset              kubernetes.Interface
	Reader                 client.Reader
	OperatorNamespace      string
	OperatorServiceAccount string
	hasLLM                 bool
}

func (r *KernelCacheRegistryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ns := req.Namespace
	if ns == "" {
		return ctrl.Result{}, nil
	}
	namespace := &corev1.Namespace{}
	if err := r.Reader.Get(ctx, client.ObjectKey{Name: ns}, namespace); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if namespace.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}
	cm := &corev1.ConfigMap{}
	if err := r.Reader.Get(ctx, client.ObjectKey{Namespace: constants.KServeNamespace, Name: constants.InferenceServiceConfigMapName}, cm); err != nil {
		return ctrl.Result{}, err
	}
	cfg, err := v1beta1.NewKernelCacheConfig(cm)
	if err != nil {
		return ctrl.Result{}, err
	}
	capturePod := &corev1.Pod{}
	isCapturePod := false
	if err := r.Reader.Get(ctx, client.ObjectKey{Namespace: ns, Name: req.Name}, capturePod); err == nil {
		if capturePod.DeletionTimestamp != nil {
			return ctrl.Result{}, nil
		}
		isCapturePod = capturePod.Annotations[registryauth.InjectedAnnotation] == "true"
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	captureEnabled := cfg.Enabled
	if captureEnabled {
		if !isCapturePod {
			captureEnabled, err = r.hasCaptureWorkload(ctx, ns, cfg)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	if !captureEnabled {
		bindings := map[string]string{
			registryauth.PusherServiceAccount: registryauth.ManagedLabel,
			registryauth.TokenRequesterRole:   registryauth.ManagedLabel,
			reporter.RoleBinding:              reporter.ManagedLabel,
		}
		for name, managedLabel := range bindings {
			binding := &rbacv1.RoleBinding{}
			if err := r.Reader.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, binding); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return ctrl.Result{}, err
			}
			if binding.Labels[managedLabel] == "true" {
				if err := r.Delete(ctx, binding, client.Preconditions{UID: &binding.UID, ResourceVersion: &binding.ResourceVersion}); client.IgnoreNotFound(err) != nil {
					return ctrl.Result{}, err
				}
			}
		}
		return ctrl.Result{}, nil
	}
	if err := r.ensureReporterIdentity(ctx, ns); err != nil {
		return ctrl.Result{}, err
	}
	if isCapturePod {
		captureAvailable, err := r.ensureCaptureForPod(ctx, capturePod, cfg)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !captureAvailable {
			return ctrl.Result{}, nil
		}
		selected, terminal, err := r.activateCaptureSession(ctx, capturePod)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !selected {
			if terminal {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		if r.Clientset == nil {
			return ctrl.Result{}, errors.New("kernelcache reporter access requires a Kubernetes clientset")
		}
		if _, err := (&reporter.Credentials{Client: r.Clientset}).Issue(ctx, capturePod); err != nil {
			if apierrors.IsNotFound(err) || apierrors.IsGone(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, fmt.Errorf("issue capture reporter access: %w", err)
		}
	}
	if cfg.Registry.Auth.Type != "openshift" {
		if isCapturePod {
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		}
		return ctrl.Result{}, nil
	}
	if err := r.ensureManagedServiceAccount(ctx, ns, registryauth.PusherServiceAccount, registryauth.ManagedLabel); err != nil {
		return ctrl.Result{}, err
	}
	bindings := []*rbacv1.RoleBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: registryauth.PusherServiceAccount, Namespace: ns},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "system:image-builder"},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: registryauth.PusherServiceAccount, Namespace: ns}},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: registryauth.TokenRequesterRole, Namespace: ns},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: registryauth.TokenRequesterRole},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: r.OperatorServiceAccount, Namespace: r.OperatorNamespace}},
		},
	}
	for _, binding := range bindings {
		if err := r.ensureRoleBinding(ctx, binding, registryauth.ManagedLabel); err != nil {
			return ctrl.Result{}, err
		}
	}
	if isCapturePod {
		if r.Clientset == nil {
			return ctrl.Result{}, errors.New("kernelcache registry access requires a Kubernetes clientset")
		}
		if _, err := (&registryauth.Credentials{Client: r.Clientset}).Issue(ctx, capturePod, cfg.Registry); err != nil {
			if apierrors.IsNotFound(err) || apierrors.IsGone(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, fmt.Errorf("issue capture registry access: %w", err)
		}
	}
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *KernelCacheRegistryReconciler) ensureCaptureForPod(ctx context.Context, pod *corev1.Pod, cfg *v1beta1.KernelCacheConfig) (bool, error) {
	var name, targetImage, cachePathsJSON string
	for _, container := range pod.Spec.Containers {
		if container.Name != "mcv" {
			continue
		}
		for _, env := range container.Env {
			switch env.Name {
			case "MCV_CAPTURE_NAME":
				name = env.Value
			case "MCV_TARGET_IMAGE":
				targetImage = env.Value
			case "MCV_CACHE_PATHS":
				cachePathsJSON = env.Value
			}
		}
	}
	if name == "" {
		return false, nil
	}

	inferenceServiceName := pod.Labels[constants.InferenceServicePodLabelKey]
	if inferenceServiceName == "" {
		return false, errors.New("capture Pod is missing the InferenceService label")
	}
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	inferenceService := &v1beta1.InferenceService{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: inferenceServiceName}, inferenceService); err != nil {
		return false, err
	}
	current := &v1alpha1.KernelCacheCapture{}
	getErr := reader.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: name}, current)
	if pod.Annotations[constants.KernelCacheCaptureStateAnnotationKey] != "" {
		if apierrors.IsNotFound(getErr) {
			return false, nil
		}
		if getErr != nil {
			return false, getErr
		}
		return true, nil
	}
	if getErr == nil {
		return true, r.setPodCaptureState(ctx, pod, constants.KernelCacheCaptureStateStarted)
	}
	if !apierrors.IsNotFound(getErr) {
		return false, getErr
	}

	cachePaths := []v1alpha1.KernelCachePath{}
	if cachePathsJSON != "" {
		if err := json.Unmarshal([]byte(cachePathsJSON), &cachePaths); err != nil {
			return false, fmt.Errorf("parse capture cache paths: %w", err)
		}
	}
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: pod.Namespace,
			Labels:    map[string]string{constants.KernelCacheCaptureGeneratedLabelKey: "true"},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(inferenceService, v1beta1.SchemeGroupVersion.WithKind("InferenceService")),
			},
		},
		Spec: v1alpha1.KernelCacheCaptureSpec{
			SourceRef:   v1alpha1.KernelCacheSourceRef{Kind: "InferenceService", Name: inferenceService.Name},
			TargetImage: targetImage,
			CachePaths:  cachePaths,
		},
	}
	capture.Spec.Signing = captureSigningSpec(cfg)
	if err := r.Create(ctx, capture); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, err
	}
	if err := r.setPodCaptureState(ctx, pod, constants.KernelCacheCaptureStateStarted); err != nil {
		return false, err
	}
	return true, nil
}

func (r *KernelCacheRegistryReconciler) setPodCaptureState(ctx context.Context, pod *corev1.Pod, state string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &corev1.Pod{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(pod), current); err != nil {
			return client.IgnoreNotFound(err)
		}
		if current.Annotations[constants.KernelCacheCaptureStateAnnotationKey] == state {
			return nil
		}
		base := current.DeepCopy()
		if current.Annotations == nil {
			current.Annotations = map[string]string{}
		}
		current.Annotations[constants.KernelCacheCaptureStateAnnotationKey] = state
		return r.Patch(ctx, current, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

func (r *KernelCacheRegistryReconciler) activateCaptureSession(ctx context.Context, pod *corev1.Pod) (bool, bool, error) {
	var name, sessionID string
	for _, container := range pod.Spec.Containers {
		if container.Name != "mcv" {
			continue
		}
		for _, env := range container.Env {
			switch env.Name {
			case "MCV_CAPTURE_NAME":
				name = env.Value
			case "MCV_CAPTURE_SESSION_ID":
				sessionID = env.Value
			}
		}
	}
	if name == "" || sessionID == "" || pod.DeletionTimestamp != nil {
		return false, false, nil
	}
	selected := false
	terminal := false
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		selected = false
		terminal = false
		capture := &v1alpha1.KernelCacheCapture{}
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: name}, capture); err != nil {
			return client.IgnoreNotFound(err)
		}
		ref := capture.Spec.SourceRef
		if ref.Kind != "InferenceService" || ref.Name != pod.Labels[constants.InferenceServicePodLabelKey] {
			return errors.New("capture source does not match producer Pod")
		}
		active := capture.Status.ActiveSession
		if active != nil {
			if active.ID == sessionID && active.PodName == pod.Name {
				changed := false
				if active.NodeName == "" && pod.Spec.NodeName != "" {
					active.NodeName = pod.Spec.NodeName
					changed = true
				}
				if active.RequestedNodeGroup == "" {
					if requested := pod.Annotations[constants.KernelCacheNodeGroupAnnotationKey]; requested != "" {
						active.RequestedNodeGroup = requested
						changed = true
					}
				}
				if changed {
					if err := r.Status().Update(ctx, capture); err != nil {
						return err
					}
				}
				if captureSessionTerminal(capture) {
					terminal = true
					return nil
				}
				selected = true
				return nil
			}
		}
		if captureSessionTerminal(capture) {
			terminal = true
			return nil
		}
		if active != nil {
			return errors.New("capture session is already assigned to another Pod")
		}
		capture.Status.ActiveSession = &v1alpha1.KernelCacheCaptureSession{
			ID:                 sessionID,
			PodName:            pod.Name,
			NodeName:           pod.Spec.NodeName,
			RequestedNodeGroup: pod.Annotations[constants.KernelCacheNodeGroupAnnotationKey],
		}
		capture.Status.RuntimeResult = nil
		if err := r.Status().Update(ctx, capture); err != nil {
			return err
		}
		selected = true
		return nil
	})
	return selected, terminal, err
}

func captureSessionTerminal(capture *v1alpha1.KernelCacheCapture) bool {
	return capture.Status.Phase == v1alpha1.KernelCacheCapturePhaseComplete ||
		capture.Status.Phase == v1alpha1.KernelCacheCapturePhaseUnchanged ||
		capture.Status.Phase == v1alpha1.KernelCacheCapturePhaseFailed ||
		capture.Status.Artifact != nil || capture.Status.KernelCacheRef != nil
}

func (r *KernelCacheRegistryReconciler) ensureReporterIdentity(ctx context.Context, namespace string) error {
	if err := r.ensureManagedServiceAccount(ctx, namespace, reporter.ServiceAccount, reporter.ManagedLabel); err != nil {
		return err
	}
	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: reporter.RoleBinding, Namespace: namespace},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: reporter.ClusterRole},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: reporter.ServiceAccount, Namespace: namespace}},
	}
	return r.ensureRoleBinding(ctx, binding, reporter.ManagedLabel)
}

func (r *KernelCacheRegistryReconciler) ensureManagedServiceAccount(ctx context.Context, namespace, name, label string) error {
	automount := false
	desired := &corev1.ServiceAccount{
		ObjectMeta:                   metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{label: "true"}},
		AutomountServiceAccountToken: &automount,
	}
	current := &corev1.ServiceAccount{}
	err := r.Reader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if current.Labels[label] != "true" {
		return fmt.Errorf("reserved ServiceAccount %s/%s already exists and is not managed by kernelcache", namespace, name)
	}
	return nil
}

func (r *KernelCacheRegistryReconciler) ensureRoleBinding(ctx context.Context, desired *rbacv1.RoleBinding, label string) error {
	desired.Labels = map[string]string{label: "true"}
	current := &rbacv1.RoleBinding{}
	if err := r.Reader.Get(ctx, client.ObjectKeyFromObject(desired), current); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		err = r.Create(ctx, desired)
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	if current.Labels[label] != "true" || current.RoleRef != desired.RoleRef {
		return fmt.Errorf("reserved RoleBinding %s/%s conflicts with registry bootstrap", desired.Namespace, desired.Name)
	}
	if reflect.DeepEqual(current.Subjects, desired.Subjects) {
		return nil
	}
	base := current.DeepCopy()
	current.Subjects = desired.Subjects
	return r.Patch(ctx, current, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func captureEnabled(obj client.Object, cfg *v1beta1.KernelCacheConfig) bool {
	if obj.GetDeletionTimestamp() != nil {
		return false
	}
	value, set := obj.GetAnnotations()[constants.KernelCacheSidecarInjectionAnnotationKey]
	if set {
		return value == "true"
	}
	return cfg.DefaultSidecarInjection
}

func (r *KernelCacheRegistryReconciler) hasCaptureWorkload(ctx context.Context, ns string, cfg *v1beta1.KernelCacheConfig) (bool, error) {
	services := &v1beta1.InferenceServiceList{}
	if err := r.List(ctx, services, client.InNamespace(ns)); err != nil {
		return false, err
	}
	for i := range services.Items {
		if captureEnabled(&services.Items[i], cfg) {
			return true, nil
		}
	}
	if r.hasLLM {
		services := &v1alpha2.LLMInferenceServiceList{}
		if err := r.List(ctx, services, client.InNamespace(ns)); err != nil {
			return false, err
		}
		for i := range services.Items {
			if captureEnabled(&services.Items[i], cfg) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (r *KernelCacheRegistryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var err error
	r.hasLLM, err = hasLLMInferenceServiceCRD(mgr)
	if err != nil {
		return err
	}
	if r.OperatorNamespace == "" || r.OperatorServiceAccount == "" {
		return errors.New("kernelcache registry bootstrap requires operator identity")
	}
	if r.Reader == nil {
		r.Reader = mgr.GetAPIReader()
	}
	b := ctrl.NewControllerManagedBy(mgr).Named("kernelcache-registry").
		For(&v1beta1.InferenceService{}).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.registryConfigRequests)).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
			if obj.GetAnnotations()[registryauth.InjectedAnnotation] != "true" {
				return nil
			}
			return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(obj)}}
		}))
	if r.hasLLM {
		b = b.Watches(&v1alpha2.LLMInferenceService{}, handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
			return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}}}
		}))
	}
	return b.Complete(r)
}

func hasLLMInferenceServiceCRD(mgr ctrl.Manager) (bool, error) {
	_, err := mgr.GetRESTMapper().RESTMapping(schema.GroupKind{
		Group: v1alpha2.SchemeGroupVersion.Group,
		Kind:  "LLMInferenceService",
	}, v1alpha2.SchemeGroupVersion.Version)
	if meta.IsNoMatchError(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *KernelCacheRegistryReconciler) registryConfigRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	if obj.GetName() != constants.InferenceServiceConfigMapName || obj.GetNamespace() != constants.KServeNamespace {
		return nil
	}
	namespaces := map[string]struct{}{}
	services := &v1beta1.InferenceServiceList{}
	if err := r.List(ctx, services); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "List registry bootstrap workloads")
		return nil
	}
	for _, svc := range services.Items {
		namespaces[svc.Namespace] = struct{}{}
	}
	if r.hasLLM {
		llms := &v1alpha2.LLMInferenceServiceList{}
		if err := r.List(ctx, llms); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "List registry bootstrap LLM workloads")
			return nil
		}
		for _, svc := range llms.Items {
			namespaces[svc.Namespace] = struct{}{}
		}
	}
	requests := make([]reconcile.Request, 0, len(namespaces))
	for ns := range namespaces {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: ns, Name: "registry-config"}})
	}
	return requests
}
