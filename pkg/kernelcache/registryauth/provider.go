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

package registryauth

import (
	"context"
	"errors"
	"fmt"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
)

const (
	PusherServiceAccount             = "kernel-cache-pusher"
	TokenRequesterRole               = "kserve-kernelcache-token-requester" // #nosec G101 -- this is an RBAC role name, not a credential
	BootstrapRole                    = "kserve-kernelcache-registry-bootstrap"
	ManagedLabel                     = "internal.serving.kserve.io/kernelcache-registry"
	AccessSecretAnnotation           = "internal.serving.kserve.io/kernelcache-access-secret"
	LegacyCredentialSecretAnnotation = "internal.serving.kserve.io/kernelcache-credential-secret"
	AccessKey                        = "access.json"
	LegacyAccessKey                  = "credential.json" // #nosec G101 -- compatibility with older Pods
)

type CredentialRequest struct {
	Secret   corev1.ObjectReference
	Registry string
}

// RegistryCredential must never be included in logs or CR status.
type RegistryCredential struct {
	Registry  string      `json:"registry"`
	Username  string      `json:"username"`
	Token     string      `json:"token"`
	ExpiresAt metav1.Time `json:"expiresAt"`
}

type RegistryAuthProvider interface {
	GetCredential(context.Context, CredentialRequest) (*RegistryCredential, error)
}

type NoAuthProvider struct{}

func (NoAuthProvider) GetCredential(context.Context, CredentialRequest) (*RegistryCredential, error) {
	return nil, nil
}

type OpenShiftAuthProvider struct {
	Client     kubernetes.Interface
	TTLSeconds int64
}

func NewProvider(c kubernetes.Interface, cfg v1beta1.KernelCacheRegistryConfig) (RegistryAuthProvider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Auth.Type {
	case "", "none":
		return NoAuthProvider{}, nil
	case "openshift":
		ttl := int64(600)
		if cfg.Auth.OpenShift != nil && cfg.Auth.OpenShift.TokenTTLSeconds != 0 {
			ttl = cfg.Auth.OpenShift.TokenTTLSeconds
		}
		return &OpenShiftAuthProvider{Client: c, TTLSeconds: ttl}, nil
	default:
		return nil, fmt.Errorf("unsupported registry authentication provider %q", cfg.Auth.Type)
	}
}

func (p *OpenShiftAuthProvider) GetCredential(ctx context.Context, req CredentialRequest) (*RegistryCredential, error) {
	ref := req.Secret
	if ref.Kind != "Secret" || ref.APIVersion != "v1" || ref.Namespace == "" || ref.Name == "" || ref.UID == "" || req.Registry == "" {
		return nil, errors.New("registry access requires an existing Secret name, namespace and UID")
	}
	result, err := p.Client.CoreV1().ServiceAccounts(ref.Namespace).CreateToken(ctx, PusherServiceAccount, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: &p.TTLSeconds,
			BoundObjectRef:    &authenticationv1.BoundObjectReference{Kind: "Secret", APIVersion: "v1", Name: ref.Name, UID: ref.UID},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("request registry publishing token: %w", err)
	}
	if result.Status.Token == "" {
		return nil, errors.New("TokenRequest returned empty registry access")
	}
	return &RegistryCredential{Registry: req.Registry, Username: "unused", Token: result.Status.Token, ExpiresAt: result.Status.ExpirationTimestamp}, nil
}
