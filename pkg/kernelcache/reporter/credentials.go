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

package reporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/kserve/kserve/pkg/kernelcache/registryauth"
)

const (
	ServiceAccount               = "kernel-cache-reporter"
	RoleBinding                  = "kernel-cache-reporter"
	ClusterRole                  = "kserve-kernelcache-reporter"
	AccessSecretAnnotation       = "internal.serving.kserve.io/kernelcache-reporter-secret"
	AccessKey                    = "access.json"
	legacyAccessKey              = "credential.json" // #nosec G101 -- compatibility with older Pods
	ManagedLabel                 = "internal.serving.kserve.io/kernelcache-reporter"
	accessSecretNamePrefix       = "mcv-reporter-" // #nosec G101 -- resource name prefix, not a credential
	defaultTokenTTLSeconds int64 = 600
)

// Credential is a short-lived token that can patch one KernelCacheCapture status.
type Credential struct {
	Token     string      `json:"token"`
	ExpiresAt metav1.Time `json:"expiresAt"`
}

// Credentials creates a Pod-bound reporter credential without changing the workload identity.
type Credentials struct {
	Client kubernetes.Interface
}

func (c *Credentials) Issue(ctx context.Context, pod *corev1.Pod) (*corev1.Secret, error) {
	live, err := c.Client.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if live.UID != pod.UID || live.UID == "" || live.DeletionTimestamp != nil || live.Annotations[registryauth.InjectedAnnotation] != "true" {
		return nil, apierrors.NewResourceExpired("capture Pod is no longer active")
	}
	name := live.Annotations[AccessSecretAnnotation]
	if !strings.HasPrefix(name, accessSecretNamePrefix) {
		return nil, errors.New("capture Pod has no reporter access Secret reference")
	}

	secrets := c.Client.CoreV1().Secrets(live.Namespace)
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		owner := metav1.NewControllerRef(live, corev1.SchemeGroupVersion.WithKind("Pod"))
		owner.BlockOwnerDeletion = boolPtr(false)
		secret, err = secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       live.Namespace,
				Labels:          map[string]string{ManagedLabel: "true"},
				OwnerReferences: []metav1.OwnerReference{*owner},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{AccessKey: []byte("{}")},
		}, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			secret, err = secrets.Get(ctx, name, metav1.GetOptions{})
		}
	}
	if err != nil {
		return nil, err
	}

	var updated *corev1.Secret
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := secrets.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if !ownedByPod(current, live) || current.UID != secret.UID || current.DeletionTimestamp != nil {
			return errors.New("reporter access Secret does not belong to this capture Pod")
		}
		var existing Credential
		accessData := current.Data[AccessKey]
		if len(accessData) == 0 {
			accessData = current.Data[legacyAccessKey]
		}
		if json.Unmarshal(accessData, &existing) == nil && existing.Token != "" && existing.ExpiresAt.After(time.Now().Add(time.Minute)) {
			if len(current.Data[AccessKey]) == 0 {
				current.Data[AccessKey] = accessData
				updated, err = secrets.Update(ctx, current, metav1.UpdateOptions{})
				return err
			}
			updated = current
			return nil
		}
		ttl := defaultTokenTTLSeconds
		result, err := c.Client.CoreV1().ServiceAccounts(current.Namespace).CreateToken(ctx, ServiceAccount, &authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				ExpirationSeconds: &ttl,
				BoundObjectRef:    &authenticationv1.BoundObjectReference{Kind: "Secret", APIVersion: "v1", Name: current.Name, UID: current.UID},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("request reporter token: %w", err)
		}
		if result.Status.Token == "" || !result.Status.ExpirationTimestamp.After(time.Now()) {
			return errors.New("TokenRequest returned an unacceptable reporter access")
		}
		data, err := json.Marshal(Credential{Token: result.Status.Token, ExpiresAt: result.Status.ExpirationTimestamp})
		if err != nil {
			return err
		}
		legacyAccessPresent := len(current.Data[legacyAccessKey]) > 0
		if current.Data == nil {
			current.Data = map[string][]byte{}
		}
		current.Data[AccessKey] = data
		if legacyAccessPresent {
			current.Data[legacyAccessKey] = data
		}
		updated, err = secrets.Update(ctx, current, metav1.UpdateOptions{})
		return err
	})
	return updated, err
}

func ownedByPod(secret *corev1.Secret, pod *corev1.Pod) bool {
	owner := metav1.GetControllerOf(secret)
	return secret.Labels[ManagedLabel] == "true" && pod.UID != "" &&
		owner != nil && owner.APIVersion == "v1" && owner.Kind == "Pod" && owner.Name == pod.Name && owner.UID == pod.UID
}

func boolPtr(value bool) *bool {
	return &value
}
