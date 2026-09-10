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

package workload

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
)

func TestResolveInferenceService(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*corev1.Pod, *appsv1.ReplicaSet, *appsv1.Deployment, *v1beta1.InferenceService)
		wantResult bool
	}{
		{
			name:       "resolves deployment ownership chain",
			wantResult: true,
		},
		{
			name: "rejects replica set UID mismatch",
			mutate: func(_ *corev1.Pod, replicaSet *appsv1.ReplicaSet, _ *appsv1.Deployment, _ *v1beta1.InferenceService) {
				replicaSet.UID = "different-replicaset-uid"
			},
		},
		{
			name: "rejects deployment UID mismatch",
			mutate: func(_ *corev1.Pod, _ *appsv1.ReplicaSet, deployment *appsv1.Deployment, _ *v1beta1.InferenceService) {
				deployment.UID = "different-deployment-uid"
			},
		},
		{
			name: "rejects inference service UID mismatch",
			mutate: func(_ *corev1.Pod, _ *appsv1.ReplicaSet, _ *appsv1.Deployment, inferenceService *v1beta1.InferenceService) {
				inferenceService.UID = "different-inferenceservice-uid"
			},
		},
		{
			name: "rejects non controller owner",
			mutate: func(pod *corev1.Pod, _ *appsv1.ReplicaSet, _ *appsv1.Deployment, _ *v1beta1.InferenceService) {
				pod.OwnerReferences[0].Controller = nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, appsv1.AddToScheme(scheme))
			require.NoError(t, v1beta1.AddToScheme(scheme))

			inferenceService := &v1beta1.InferenceService{ObjectMeta: metav1.ObjectMeta{
				Name: "model", Namespace: "team", UID: types.UID("inferenceservice-uid"),
			}}
			deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
				Name: "model-predictor", Namespace: "team", UID: types.UID("deployment-uid"),
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(inferenceService, v1beta1.SchemeGroupVersion.WithKind(inferenceServiceKind))},
			}}
			replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
				Name: "model-predictor-abc", Namespace: "team", UID: types.UID("replicaset-uid"),
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind(deploymentKind))},
			}}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "model-predictor-abc-xyz", Namespace: "team",
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(replicaSet, appsv1.SchemeGroupVersion.WithKind(replicaSetKind))},
			}}
			if tc.mutate != nil {
				tc.mutate(pod, replicaSet, deployment, inferenceService)
			}
			client := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(inferenceService, deployment, replicaSet).Build()

			resolved, err := ResolveInferenceService(context.Background(), client, pod)
			require.NoError(t, err)
			if tc.wantResult {
				require.Equal(t, inferenceService, resolved)
			} else {
				require.Nil(t, resolved)
			}
		})
	}
}
