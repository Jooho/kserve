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

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/kernelcache/registryauth"
	"github.com/kserve/kserve/pkg/kernelcache/reporter"
)

func TestRegistryCreatesGeneratedCaptureBeforeActivatingSession(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	const (
		namespace   = "team"
		captureName = "model-kernelcache-capture-session"
		sessionID   = "session-id"
		podUID      = "pod-uid"
	)
	controller := true
	inferenceService := &v1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: namespace, UID: "isvc-uid"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-pod",
			Namespace: namespace,
			UID:       podUID,
			Labels:    map[string]string{constants.InferenceServicePodLabelKey: inferenceService.Name},
			Annotations: map[string]string{
				registryauth.InjectedAnnotation:             "true",
				reporter.AccessSecretAnnotation:             "mcv-reporter-session",
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
				{Name: "MCV_CAPTURE_NAME", Value: captureName},
				{Name: "MCV_CAPTURE_SESSION_ID", Value: sessionID},
				{Name: "MCV_TARGET_IMAGE", Value: "registry.example/team/cache:session"},
				{Name: "MCV_CACHE_PATHS", Value: `[{"containerName":"kserve-container","containerPath":"/tmp/vllm","ociPath":"io.vllm.cache"}]`},
			},
		}}},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"registry":{"endpoint":"registry.example","auth":{"type":"none"}}}`},
	}
	namespaceObject := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(namespaceObject, configMap, inferenceService, pod).
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

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pod)})
	require.NoError(t, err)

	capture := &v1alpha1.KernelCacheCapture{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: captureName}, capture))
	require.Equal(t, "true", capture.Labels[constants.KernelCacheCaptureGeneratedLabelKey])
	require.Empty(t, capture.Annotations)
	require.Equal(t, v1alpha1.KernelCacheSourceRef{Kind: "InferenceService", Name: inferenceService.Name}, capture.Spec.SourceRef)
	require.Equal(t, "registry.example/team/cache:session", capture.Spec.TargetImage)
	require.Equal(t, []v1alpha1.KernelCachePath{{ContainerName: "kserve-container", ContainerPath: "/tmp/vllm", OCIPath: "io.vllm.cache"}}, capture.Spec.CachePaths)
	require.Len(t, capture.OwnerReferences, 1)
	require.Equal(t, inferenceService.UID, capture.OwnerReferences[0].UID)
	require.NotNil(t, capture.Status.ActiveSession)
	require.Equal(t, sessionID, capture.Status.ActiveSession.ID)
	require.Equal(t, pod.Name, capture.Status.ActiveSession.PodName)
	require.Equal(t, "gpu-node", capture.Status.ActiveSession.NodeName)
	require.Equal(t, "gpu-workers", capture.Status.ActiveSession.RequestedNodeGroup)
	require.Empty(t, capture.Status.Phase)
	require.Empty(t, capture.Status.Conditions)
	updatedPod := &corev1.Pod{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), updatedPod))
	require.Equal(t, constants.KernelCacheCaptureStateStarted, updatedPod.Annotations[constants.KernelCacheCaptureStateAnnotationKey])
}

func TestRegistryDoesNotRecreateCaptureForMarkedPod(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	for _, state := range []string{
		constants.KernelCacheCaptureStateStarted,
		constants.KernelCacheCaptureStateComplete,
		constants.KernelCacheCaptureStateUnchanged,
	} {
		t.Run(state, func(t *testing.T) {
			inferenceService := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "team"},
			}
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "model-pod",
					Namespace: "team",
					Labels:    map[string]string{constants.InferenceServicePodLabelKey: inferenceService.Name},
					Annotations: map[string]string{
						constants.KernelCacheCaptureStateAnnotationKey: state,
					},
				},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "mcv",
					Env: []corev1.EnvVar{
						{Name: "MCV_CAPTURE_NAME", Value: "model-capture"},
						{Name: "MCV_TARGET_IMAGE", Value: "registry.example/model:cache"},
					},
				}}},
			}
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(inferenceService, pod).
				Build()
			reconciler := &KernelCacheRegistryReconciler{Client: k8sClient, Reader: k8sClient}

			available, err := reconciler.ensureCaptureForPod(ctx, pod, &v1beta1.KernelCacheConfig{})
			require.NoError(t, err)
			require.False(t, available)
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: "model-capture"}, &v1alpha1.KernelCacheCapture{})
			require.True(t, apierrors.IsNotFound(err))
		})
	}
}

func TestCaptureSessionBackfillsSchedulingInformation(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: "capture", Namespace: "team"},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: "InferenceService", Name: "model",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{ActiveSession: &v1alpha1.KernelCacheCaptureSession{
			ID: "session", PodName: "model-pod",
		}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(capture).WithObjects(capture).Build()
	r := &KernelCacheRegistryReconciler{Client: c, Reader: c}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-pod",
			Namespace: "team",
			UID:       "pod-uid",
			Labels:    map[string]string{constants.InferenceServicePodLabelKey: "model"},
			Annotations: map[string]string{
				constants.KernelCacheNodeGroupAnnotationKey: "gpu-workers",
			},
		},
		Spec: corev1.PodSpec{NodeName: "gpu-node", Containers: []corev1.Container{{Name: "mcv", Env: []corev1.EnvVar{
			{Name: "MCV_CAPTURE_NAME", Value: "capture"},
			{Name: "MCV_CAPTURE_SESSION_ID", Value: "session"},
		}}}},
	}

	selected, terminal, err := r.activateCaptureSession(ctx, pod)
	require.NoError(t, err)
	require.True(t, selected)
	require.False(t, terminal)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(capture), capture))
	require.Equal(t, "gpu-node", capture.Status.ActiveSession.NodeName)
	require.Equal(t, "gpu-workers", capture.Status.ActiveSession.RequestedNodeGroup)
}

func TestCompletedCaptureSessionBackfillsSchedulingInformationWithoutReactivation(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: "capture", Namespace: "team"},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: "InferenceService", Name: "model",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{
			Phase: v1alpha1.KernelCacheCapturePhaseComplete,
			ActiveSession: &v1alpha1.KernelCacheCaptureSession{
				ID: "session", PodName: "model-pod",
			},
			Artifact: &v1alpha1.KernelCacheArtifact{ImageReference: "registry.example/cache@sha256:old"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(capture).WithObjects(capture).Build()
	r := &KernelCacheRegistryReconciler{Client: c, Reader: c}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-pod",
			Namespace: "team",
			UID:       "pod-uid",
			Labels:    map[string]string{constants.InferenceServicePodLabelKey: "model"},
			Annotations: map[string]string{
				constants.KernelCacheNodeGroupAnnotationKey: "gpu-workers",
			},
		},
		Spec: corev1.PodSpec{NodeName: "gpu-node", Containers: []corev1.Container{{Name: "mcv", Env: []corev1.EnvVar{
			{Name: "MCV_CAPTURE_NAME", Value: "capture"},
			{Name: "MCV_CAPTURE_SESSION_ID", Value: "session"},
		}}}},
	}

	selected, terminal, err := r.activateCaptureSession(ctx, pod)
	require.NoError(t, err)
	require.False(t, selected)
	require.True(t, terminal)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(capture), capture))
	require.Equal(t, "gpu-node", capture.Status.ActiveSession.NodeName)
	require.Equal(t, "gpu-workers", capture.Status.ActiveSession.RequestedNodeGroup)
}

func TestCaptureSessionDoesNotResetCompletedCapture(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	image := "registry.example/cache@sha256:old"
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: "capture", Namespace: "team"},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: "InferenceService", Name: "model",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{
			Phase:          v1alpha1.KernelCacheCapturePhaseComplete,
			ActiveSession:  &v1alpha1.KernelCacheCaptureSession{ID: "completed-session", PodName: "completed-pod"},
			RuntimeResult:  map[string]string{runtimeResultStateKey: runtimeResultSucceededState, runtimeResultCaptureSessionIDKey: "completed-session", runtimeResultSourcePodNameKey: "completed-pod", runtimeResultImageReferenceKey: image},
			Artifact:       &v1alpha1.KernelCacheArtifact{ImageReference: image},
			KernelCacheRef: &v1alpha1.NamespacedName{Name: "old-cache", Namespace: "team"},
			Signing:        &v1alpha1.KernelCacheSigningStatus{Mode: "none", State: v1alpha1.KernelCacheArtifactSecurityStateSkipped},
			Conditions:     []metav1.Condition{{Type: kernelCacheCaptureReadyConditionType, Status: metav1.ConditionTrue}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(capture).WithObjects(capture).Build()
	r := &KernelCacheRegistryReconciler{Client: c, Reader: c}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "new-pod", Namespace: "team", UID: "new-uid", CreationTimestamp: metav1.NewTime(time.Unix(2, 0)), Labels: map[string]string{constants.InferenceServicePodLabelKey: "model"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "mcv", Env: []corev1.EnvVar{
			{Name: "MCV_CAPTURE_NAME", Value: "capture"}, {Name: "MCV_CAPTURE_SESSION_ID", Value: "new-session"},
		}}}},
	}
	originalStatus := capture.Status.DeepCopy()
	selected, terminal, err := r.activateCaptureSession(ctx, pod)
	require.NoError(t, err)
	require.False(t, selected)
	require.True(t, terminal)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(capture), capture))
	require.Equal(t, originalStatus, &capture.Status)
}

func TestKernelCacheRegistryBootstrap(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace},
		Data:       map[string]string{"kernelcache": `{"enabled":true,"defaultSidecarInjection":true,"registry":{"endpoint":"registry.example:5000","auth":{"type":"openshift"}}}`},
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
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "team", Name: registryauth.PusherServiceAccount}, sa))
	require.NotNil(t, sa.AutomountServiceAccountToken)
	require.False(t, *sa.AutomountServiceAccountToken)
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "team", Name: reporter.ServiceAccount}, sa))
	require.NotNil(t, sa.AutomountServiceAccountToken)
	require.False(t, *sa.AutomountServiceAccountToken)
	binding := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "team", Name: registryauth.TokenRequesterRole}, binding))
	require.Equal(t, registryauth.TokenRequesterRole, binding.RoleRef.Name)
	require.Equal(t, []rbacv1.Subject{{Kind: "ServiceAccount", Name: "operator", Namespace: "kserve"}}, binding.Subjects)
	bindings := &rbacv1.RoleBindingList{}
	require.NoError(t, c.List(ctx, bindings, client.InNamespace("team")))
	require.Len(t, bindings.Items, 3)

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(cm), cm))
	cm.Data["kernelcache"] = `{"enabled":false}`
	require.NoError(t, c.Update(ctx, cm))
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, c.List(ctx, bindings, client.InNamespace("team")))
	require.Empty(t, bindings.Items)
}
