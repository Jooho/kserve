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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/kernelcache/captureconfig"
	"github.com/kserve/kserve/pkg/kernelcache/registryauth"
	"github.com/kserve/kserve/pkg/kernelcache/reporter"
)

func TestRegistryConfigMapPredicate(t *testing.T) {
	pred := predicate.NewPredicateFuncs(isInferenceServiceConfigMap)
	matching := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:      constants.InferenceServiceConfigMapName,
		Namespace: constants.KServeNamespace,
	}}
	if !pred.Create(event.CreateEvent{Object: matching}) {
		t.Fatal("expected the InferenceService ConfigMap to pass the predicate")
	}
	if pred.Create(event.CreateEvent{Object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:      constants.InferenceServiceConfigMapName,
		Namespace: "other",
	}}}) {
		t.Fatal("did not expect a ConfigMap from another namespace to pass the predicate")
	}
	if pred.Create(event.CreateEvent{Object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:      "other",
		Namespace: constants.KServeNamespace,
	}}}) {
		t.Fatal("did not expect another ConfigMap to pass the predicate")
	}
}

func TestCaptureConfigFromPodReadsGroupedConfiguration(t *testing.T) {
	want := newTestCaptureConfig("capture", "session-id")
	value, err := captureconfig.MarshalCaptureConfig(want)
	require.NoError(t, err)
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "mcv",
		Env:  []corev1.EnvVar{{Name: captureconfig.CaptureConfigEnv, Value: value}},
	}}}}

	got, found, err := captureConfigFromPod(pod)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want, got)
}

func TestCaptureConfigFromPodRequiresGroupedConfiguration(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "mcv"}}}}

	_, found, err := captureConfigFromPod(pod)
	require.NoError(t, err)
	require.False(t, found)
}

func captureConfigEnv(t *testing.T, name, sessionID string) []corev1.EnvVar {
	t.Helper()
	value, err := captureconfig.MarshalCaptureConfig(newTestCaptureConfig(name, sessionID))
	require.NoError(t, err)
	return []corev1.EnvVar{{Name: captureconfig.CaptureConfigEnv, Value: value}}
}

func newTestCaptureConfig(name, sessionID string) captureconfig.CaptureConfig {
	return captureconfig.CaptureConfig{
		Version:     captureconfig.CurrentVersion,
		CacheDir:    "/workspace/cache/0",
		TargetImage: "registry.example/team/cache:session",
		Capture: captureconfig.CaptureIdentity{
			Name:      name,
			Namespace: "team",
			SessionID: sessionID,
		},
		CachePaths: []v1alpha1.KernelCachePath{{
			ContainerName: "kserve-container",
			ContainerPath: "/tmp/vllm",
			OCIPath:       "io.vllm.cache",
		}},
	}
}

func TestRegistryCreatesDeterministicCaptureAndReporterIdentity(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	const (
		namespace   = "team"
		captureName = "model-kcc-669889f77f"
		sessionID   = "session-id"
		podUID      = "pod-uid"
	)
	controller := true
	captureValue, err := captureconfig.MarshalCaptureConfig(newTestCaptureConfig(captureName, sessionID))
	require.NoError(t, err)
	inferenceService := &v1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: namespace, UID: "isvc-uid"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-pod",
			Namespace: namespace,
			UID:       podUID,
			Labels:    map[string]string{constants.InferenceServicePodLabelKey: inferenceService.Name, "pod-template-hash": "669889f77f"},
			Annotations: map[string]string{
				reporter.AccessSecretAnnotation:             reporter.SecretName(captureName),
				constants.KernelCacheNodeGroupAnnotationKey: "gpu-workers",
			},
			OwnerReferences: []metav1.OwnerReference{{
				UID:        "replicaset-uid",
				Controller: &controller,
			}},
		},
		Spec: corev1.PodSpec{NodeName: "gpu-node", Containers: []corev1.Container{{
			Name: "mcv",
			Env: []corev1.EnvVar{
				{Name: captureconfig.CaptureConfigEnv, Value: captureValue},
			},
		}}},
	}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "model-predictor-rs", Namespace: namespace, UID: "replicaset-uid",
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "model-predictor", Namespace: namespace, UID: "deployment-uid"}},
			appsv1.SchemeGroupVersion.WithKind("Deployment"),
		)},
	}}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: "model-predictor", Namespace: namespace, UID: "deployment-uid",
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(inferenceService, v1beta1.SchemeGroupVersion.WithKind("InferenceService"))},
	}}
	pod.OwnerReferences[0].Name = replicaSet.Name
	pod.OwnerReferences[0].UID = replicaSet.UID
	pod.OwnerReferences[0].APIVersion = appsv1.SchemeGroupVersion.String()
	pod.OwnerReferences[0].Kind = "ReplicaSet"
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"registry":{"endpoint":"registry.example","auth":{"type":"none"}}}`},
	}
	namespaceObject := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(namespaceObject, configMap, inferenceService, deployment, replicaSet, pod).
		WithStatusSubresource(&v1alpha1.KernelCacheCapture{}).
		Build()
	clientset := kubernetesfake.NewSimpleClientset(pod.DeepCopy())
	clientset.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createAction, ok := action.(k8stesting.CreateAction)
		if !ok || action.GetSubresource() != "token" {
			return false, nil, nil
		}
		request := createAction.GetObject().(*authenticationv1.TokenRequest).DeepCopy()
		request.Status.Token = "reporter-token"
		request.Status.ExpirationTimestamp = metav1.NewTime(time.Now().Add(10 * time.Minute))
		return true, request, nil
	})
	reconciler := &KernelCacheRegistryReconciler{
		Client:                 k8sClient,
		Reader:                 k8sClient,
		Clientset:              clientset,
		OperatorNamespace:      "kserve",
		OperatorServiceAccount: "operator",
	}

	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pod)})
	require.NoError(t, err)

	capture := &v1alpha1.KernelCacheCapture{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: captureName}, capture))
	require.Equal(t, "true", capture.Labels[constants.KernelCacheCaptureGeneratedLabelKey])
	require.Empty(t, capture.Annotations)
	require.Equal(t, v1alpha1.KernelCacheSourceRef{Kind: "InferenceService", Name: inferenceService.Name}, capture.Spec.SourceRef)
	require.Equal(t, "registry.example/team/cache:session", capture.Spec.TargetImage)
	require.Equal(t, []v1alpha1.KernelCachePath{{ContainerName: "kserve-container", ContainerPath: "/tmp/vllm", OCIPath: "io.vllm.cache"}}, capture.Spec.CachePaths)
	require.Len(t, capture.OwnerReferences, 1)
	require.Equal(t, replicaSet.UID, capture.OwnerReferences[0].UID)
	require.Equal(t, "ReplicaSet", capture.OwnerReferences[0].Kind)
	require.NotNil(t, capture.OwnerReferences[0].BlockOwnerDeletion)
	require.False(t, *capture.OwnerReferences[0].BlockOwnerDeletion)
	require.Nil(t, capture.Status.ActiveSession)
	serviceAccount := &corev1.ServiceAccount{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: reporter.ServiceAccountName(captureName)}, serviceAccount))
	require.NotNil(t, serviceAccount.OwnerReferences[0].BlockOwnerDeletion)
	require.False(t, *serviceAccount.OwnerReferences[0].BlockOwnerDeletion)
	role := &rbacv1.Role{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: reporter.RoleName(captureName)}, role))
	require.NotNil(t, role.OwnerReferences[0].BlockOwnerDeletion)
	require.False(t, *role.OwnerReferences[0].BlockOwnerDeletion)
	binding := &rbacv1.RoleBinding{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: reporter.RoleBindingName(captureName)}, binding))
	require.NotNil(t, binding.OwnerReferences[0].BlockOwnerDeletion)
	require.False(t, *binding.OwnerReferences[0].BlockOwnerDeletion)
	tokenBinding := &rbacv1.RoleBinding{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: registryauth.TokenRequesterRole}, tokenBinding))
	require.Equal(t, registryauth.TokenRequesterRole, tokenBinding.RoleRef.Name)
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, reporter.SecretName(captureName), metav1.GetOptions{})
	require.NoError(t, err)
	require.Contains(t, secret.Data, reporter.AccessKey)
	require.Len(t, secret.OwnerReferences, 1)
	require.NotNil(t, secret.OwnerReferences[0].BlockOwnerDeletion)
	require.False(t, *secret.OwnerReferences[0].BlockOwnerDeletion)
	require.Empty(t, capture.Status.Phase)
	require.Empty(t, capture.Status.Conditions)

	// A later reconcile must not restore capture-scoped identity resources after
	// the existing KCC becomes terminal.
	capture.Status.Phase = v1alpha1.KernelCacheCapturePhaseComplete
	require.NoError(t, k8sClient.Status().Update(ctx, capture))
	for _, object := range []client.Object{
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: reporter.ServiceAccountName(captureName), Namespace: namespace}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: reporter.RoleName(captureName), Namespace: namespace}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: reporter.RoleBindingName(captureName), Namespace: namespace}},
	} {
		require.NoError(t, k8sClient.Delete(ctx, object))
	}
	require.NoError(t, clientset.CoreV1().Secrets(namespace).Delete(ctx, reporter.SecretName(captureName), metav1.DeleteOptions{}))
	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pod)})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, result)
	for _, object := range []client.Object{
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: reporter.ServiceAccountName(captureName), Namespace: namespace}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: reporter.RoleName(captureName), Namespace: namespace}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: reporter.RoleBindingName(captureName), Namespace: namespace}},
	} {
		require.True(t, apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(object), object)))
	}
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, reporter.SecretName(captureName), metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err))

	// A subsequent reconcile, such as one after a controller restart, must keep
	// terminal capture identities absent.
	result, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pod)})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, result)
}

func TestRegistryReopensProducerGoneCaptureForNewPod(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	const (
		namespace   = "team"
		captureName = "model-kcc-669889f77f"
		revisionID  = "669889f77f"
	)
	controller := true
	inferenceService := &v1beta1.InferenceService{ObjectMeta: metav1.ObjectMeta{
		Name: "model", Namespace: namespace, UID: "isvc-uid",
	}}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: "model-predictor", Namespace: namespace, UID: "deployment-uid",
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
			inferenceService, v1beta1.SchemeGroupVersion.WithKind("InferenceService"),
		)},
	}}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "model-predictor-rs", Namespace: namespace, UID: "replicaset-uid",
		Labels: map[string]string{"pod-template-hash": revisionID},
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
			deployment, appsv1.SchemeGroupVersion.WithKind("Deployment"),
		)},
	}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-pod-new",
			Namespace: namespace,
			UID:       "pod-new-uid",
			Labels: map[string]string{
				constants.InferenceServicePodLabelKey: inferenceService.Name,
				"pod-template-hash":                   revisionID,
			},
			Annotations: map[string]string{
				reporter.AccessSecretAnnotation: reporter.SecretName(captureName),
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet",
				Name: replicaSet.Name, UID: replicaSet.UID, Controller: &controller,
			}},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "mcv", Env: captureConfigEnv(t, captureName, "new-session"),
		}}},
	}
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{
			Name: captureName, Namespace: namespace, UID: "capture-uid",
			Labels: map[string]string{constants.KernelCacheCaptureGeneratedLabelKey: "true"},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
				replicaSet, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"),
			)},
		},
		Spec: v1alpha1.KernelCacheCaptureSpec{
			SourceRef: v1alpha1.KernelCacheSourceRef{Kind: "InferenceService", Name: inferenceService.Name},
		},
		Status: v1alpha1.KernelCacheCaptureStatus{
			Phase:         v1alpha1.KernelCacheCapturePhaseFailed,
			ActiveSession: &v1alpha1.KernelCacheCaptureSession{ID: "old-session", PodName: "old-pod"},
			RuntimeResult: map[string]string{runtimeResultStateKey: runtimeResultSucceededState},
			Conditions: []metav1.Condition{{
				Type: kernelCacheCaptureReadyConditionType, Status: metav1.ConditionFalse,
				Reason: kernelCacheCaptureReasonProducerGone,
			}},
		},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"registry":{"endpoint":"registry.example","auth":{"type":"none"}}}`},
	}
	namespaceObject := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(namespaceObject, configMap, inferenceService, deployment, replicaSet, pod, capture).
		WithStatusSubresource(capture).Build()
	clientset := kubernetesfake.NewSimpleClientset(pod.DeepCopy())
	clientset.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createAction, ok := action.(k8stesting.CreateAction)
		if !ok || action.GetSubresource() != "token" {
			return false, nil, nil
		}
		request := createAction.GetObject().(*authenticationv1.TokenRequest).DeepCopy()
		request.Status.Token = "reporter-token"
		request.Status.ExpirationTimestamp = metav1.NewTime(time.Now().Add(10 * time.Minute))
		return true, request, nil
	})
	reconciler := &KernelCacheRegistryReconciler{
		Client: k8sClient, Reader: k8sClient, Clientset: clientset,
		OperatorNamespace: "kserve", OperatorServiceAccount: "operator",
	}

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pod)})
	require.NoError(t, err)

	updated := &v1alpha1.KernelCacheCapture{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(capture), updated))
	require.Equal(t, v1alpha1.KernelCacheCapturePhasePending, updated.Status.Phase)
	require.Nil(t, updated.Status.ActiveSession)
	require.Nil(t, updated.Status.RuntimeResult)
	require.NotNil(t, updated.Status.Conditions)
	require.Equal(t, kernelCacheCaptureReasonRetryingProducerGone, updated.Status.Conditions[0].Reason)
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: reporter.ServiceAccountName(captureName)}, &corev1.ServiceAccount{}))
}

// User-controlled Pod metadata must not authorize capture credentials.
func TestRegistryIgnoresCapturePodWithoutVerifiedInferenceServiceOwner(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	const namespace = "team"
	configValue, err := captureconfig.MarshalCaptureConfig(newTestCaptureConfig("model-capture", "session"))
	require.NoError(t, err)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-created-pod",
			Namespace: namespace,
			Labels:    map[string]string{constants.InferenceServicePodLabelKey: "model"},
			Annotations: map[string]string{
				"internal.serving.kserve.io/mcv-injected": "true",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "mcv",
			Env:  []corev1.EnvVar{{Name: captureconfig.CaptureConfigEnv, Value: configValue}},
		}}},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"registry":{"endpoint":"registry.example","auth":{"type":"none"}}}`},
	}
	namespaceObject := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespaceObject, configMap, pod).Build()
	r := &KernelCacheRegistryReconciler{Client: c, Reader: c}

	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pod)})
	require.NoError(t, err)
	require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "model-capture"}, &v1alpha1.KernelCacheCapture{})))
	require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: reporter.ServiceAccountName("model-capture")}, &corev1.ServiceAccount{})))
}

func TestKernelCacheRegistryBootstrap(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"defaultSidecarInjection":true,"registry":{"endpoint":"registry.example:5000","auth":{"type":"serviceAccountToken","pushRoleRef":{"kind":"ClusterRole","name":"registry-pusher"},"pullRoleRef":{"kind":"ClusterRole","name":"registry-puller"}}}}`},
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team"}}
	svc := &v1beta1.InferenceService{ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "team"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm, ns, svc).Build()
	r := &KernelCacheRegistryReconciler{Client: c, Reader: c, OperatorNamespace: "kserve", OperatorServiceAccount: "operator"}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(svc)}
	for range 2 {
		_, err := r.Reconcile(ctx, req)
		require.NoError(t, err)
	}
	sa := &corev1.ServiceAccount{}
	require.Error(t, c.Get(ctx, client.ObjectKey{Namespace: "team", Name: "kernel-cache-pusher"}, sa))
	binding := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "team", Name: registryauth.TokenRequesterRole}, binding))
	require.Equal(t, registryauth.TokenRequesterRole, binding.RoleRef.Name)
	require.Equal(t, []rbacv1.Subject{{Kind: "ServiceAccount", Name: "operator", Namespace: "kserve"}}, binding.Subjects)
	bindings := &rbacv1.RoleBindingList{}
	require.NoError(t, c.List(ctx, bindings, client.InNamespace("team")))
	require.Len(t, bindings.Items, 1)

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(cm), cm))
	cm.Data["kernelcache"] = `{"enabled":false}`
	require.NoError(t, c.Update(ctx, cm))
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, c.List(ctx, bindings, client.InNamespace("team")))
	require.Empty(t, bindings.Items)
}

func TestRegistryCreatesScopedPusherIdentity(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	capture := &v1alpha1.KernelCacheCapture{ObjectMeta: metav1.ObjectMeta{
		Name: "model-kcc-revision", Namespace: "team", UID: "capture-uid",
	}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture).Build()
	r := &KernelCacheRegistryReconciler{Client: c, Reader: c}
	config := v1beta1.KernelCacheRegistryConfig{
		Endpoint: "registry.example:5000",
		Auth: v1beta1.KernelCacheRegistryAuth{
			Type:        v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken,
			PushRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-pusher"},
			PullRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"},
		},
	}
	require.NoError(t, r.ensurePusherIdentityForCapture(ctx, capture, config))

	sa := &corev1.ServiceAccount{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: capture.Namespace, Name: registryauth.PusherServiceAccountName(capture.Name)}, sa))
	require.Equal(t, capture.UID, metav1.GetControllerOf(sa).UID)
	require.NotNil(t, sa.OwnerReferences[0].BlockOwnerDeletion)
	require.False(t, *sa.OwnerReferences[0].BlockOwnerDeletion)

	binding := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: capture.Namespace, Name: registryauth.PusherRoleBindingName(capture.Name)}, binding))
	require.Equal(t, "registry-pusher", binding.RoleRef.Name)
	require.Equal(t, registryauth.PusherServiceAccountName(capture.Name), binding.Subjects[0].Name)
	require.Equal(t, capture.UID, metav1.GetControllerOf(binding).UID)
	require.NotNil(t, binding.OwnerReferences[0].BlockOwnerDeletion)
	require.False(t, *binding.OwnerReferences[0].BlockOwnerDeletion)
}

func TestRegistryKeepsActivePusherRoleBindingWhenRoleRefChanges(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	capture := &v1alpha1.KernelCacheCapture{ObjectMeta: metav1.ObjectMeta{
		Name: "model-kcc-revision", Namespace: "team", UID: "capture-uid",
	}, Status: v1alpha1.KernelCacheCaptureStatus{Phase: v1alpha1.KernelCacheCapturePhaseCapturing}}
	owner := captureOwnerReference(capture)
	current := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            registryauth.PusherRoleBindingName(capture.Name),
			Namespace:       capture.Namespace,
			Labels:          map[string]string{registryauth.ManagedLabel: "true"},
			OwnerReferences: []metav1.OwnerReference{*owner},
		},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "registry-pusher-v1"},
		Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: registryauth.PusherServiceAccountName(capture.Name), Namespace: capture.Namespace}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture, current).Build()
	r := &KernelCacheRegistryReconciler{Client: c, Reader: c}
	config := v1beta1.KernelCacheRegistryConfig{Auth: v1beta1.KernelCacheRegistryAuth{
		Type:        v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken,
		PushRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-pusher-v2"},
	}}

	require.NoError(t, r.ensurePusherIdentityForCapture(ctx, capture, config))

	binding := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(current), binding))
	require.Equal(t, "registry-pusher-v1", binding.RoleRef.Name)
}

func TestRegistryRejectsPusherRoleRefChangeForInactiveCapture(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	capture := &v1alpha1.KernelCacheCapture{ObjectMeta: metav1.ObjectMeta{
		Name: "model-kcc-revision", Namespace: "team", UID: "capture-uid",
	}, Status: v1alpha1.KernelCacheCaptureStatus{Phase: v1alpha1.KernelCacheCapturePhaseComplete}}
	owner := captureOwnerReference(capture)
	current := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            registryauth.PusherRoleBindingName(capture.Name),
			Namespace:       capture.Namespace,
			Labels:          map[string]string{registryauth.ManagedLabel: "true"},
			OwnerReferences: []metav1.OwnerReference{*owner},
		},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "registry-pusher-v1"},
		Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: registryauth.PusherServiceAccountName(capture.Name), Namespace: capture.Namespace}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture, current).Build()
	r := &KernelCacheRegistryReconciler{Client: c, Reader: c}
	config := v1beta1.KernelCacheRegistryConfig{Auth: v1beta1.KernelCacheRegistryAuth{
		Type:        v1beta1.KernelCacheRegistryAuthTypeServiceAccountToken,
		PushRoleRef: &v1beta1.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-pusher-v2"},
	}}

	require.Error(t, r.ensurePusherIdentityForCapture(ctx, capture, config))
	binding := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(current), binding))
	require.Equal(t, "registry-pusher-v1", binding.RoleRef.Name)
}

func TestRegistryRepairsManagedReporterRBACDrift(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	capture := &v1alpha1.KernelCacheCapture{ObjectMeta: metav1.ObjectMeta{
		Name: "model-kcc-revision", Namespace: "team", UID: "capture-uid",
	}}
	owner := captureOwnerReference(capture)
	automount := true
	serviceAccount := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name: reporter.ServiceAccountName(capture.Name), Namespace: capture.Namespace,
		Labels: map[string]string{reporter.ManagedLabel: "true"}, OwnerReferences: []metav1.OwnerReference{*owner},
	}, AutomountServiceAccountToken: &automount}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
		Name: reporter.RoleName(capture.Name), Namespace: capture.Namespace,
		Labels: map[string]string{reporter.ManagedLabel: "true"}, OwnerReferences: []metav1.OwnerReference{*owner},
	}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture, serviceAccount, role).Build()
	r := &KernelCacheRegistryReconciler{Client: c, Reader: c}

	require.NoError(t, r.ensureReporterIdentityForCapture(ctx, capture))

	updatedServiceAccount := &corev1.ServiceAccount{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(serviceAccount), updatedServiceAccount))
	require.NotNil(t, updatedServiceAccount.AutomountServiceAccountToken)
	require.False(t, *updatedServiceAccount.AutomountServiceAccountToken)

	updatedRole := &rbacv1.Role{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(role), updatedRole))
	require.Equal(t, []rbacv1.PolicyRule{
		{APIGroups: []string{"serving.kserve.io"}, Resources: []string{"kernelcachecaptures"}, ResourceNames: []string{capture.Name}, Verbs: []string{"get"}},
		{APIGroups: []string{"serving.kserve.io"}, Resources: []string{"kernelcachecaptures/status"}, ResourceNames: []string{capture.Name}, Verbs: []string{"get", "update"}},
	}, updatedRole.Rules)
}

func TestTokenRequesterBindingRequestsEnqueueWorkloadsInNamespace(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1beta1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1beta1.InferenceService{ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "team"}},
		&v1beta1.InferenceService{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "other"}},
	).Build()
	r := &KernelCacheRegistryReconciler{Client: c, Reader: c}

	requests := r.tokenRequesterBindingRequests(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
		Name: registryauth.TokenRequesterRole, Namespace: "team",
	}})
	require.Equal(t, []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: "team", Name: "model"}}}, requests)
}
