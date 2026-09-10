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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
)

// WorkloadRevision is the internal workload-neutral identity used by the
// capture lifecycle. It is intentionally not part of the KCC API.
type WorkloadRevision struct {
	Source     *v1beta1.InferenceService
	ReplicaSet *appsv1.ReplicaSet
	RevisionID string
}

// ResolveInferenceServiceRevision resolves the Deployment-backed workload
// revision for a Pod and validates every controller UID in the ownership chain.
func ResolveInferenceServiceRevision(ctx context.Context, reader client.Reader, pod *corev1.Pod) (*WorkloadRevision, error) {
	if reader == nil || pod == nil {
		return nil, nil
	}
	replicaSetRef := controllerOwner(pod, appsAPIVersion, replicaSetKind)
	if replicaSetRef == nil {
		return nil, nil
	}
	replicaSet := &appsv1.ReplicaSet{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: replicaSetRef.Name}, replicaSet); err != nil {
		return nil, err
	}
	if replicaSet.UID != replicaSetRef.UID {
		return nil, nil
	}

	deploymentRef := controllerOwner(replicaSet, appsAPIVersion, deploymentKind)
	if deploymentRef == nil {
		return nil, nil
	}
	deployment := &appsv1.Deployment{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: replicaSet.Namespace, Name: deploymentRef.Name}, deployment); err != nil {
		return nil, err
	}
	if deployment.UID != deploymentRef.UID {
		return nil, nil
	}

	inferenceServiceRef := controllerOwner(deployment, inferenceServiceAPIVer, inferenceServiceKind)
	if inferenceServiceRef == nil {
		return nil, nil
	}
	inferenceService := &v1beta1.InferenceService{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: deployment.Namespace, Name: inferenceServiceRef.Name}, inferenceService); err != nil {
		return nil, err
	}
	if inferenceService.UID != inferenceServiceRef.UID {
		return nil, nil
	}

	revisionID := pod.Labels[appsv1.DefaultDeploymentUniqueLabelKey]
	if revisionID == "" {
		revisionID = replicaSet.Labels[appsv1.DefaultDeploymentUniqueLabelKey]
	}
	if revisionID == "" {
		return nil, nil
	}
	return &WorkloadRevision{Source: inferenceService, ReplicaSet: replicaSet, RevisionID: revisionID}, nil
}
