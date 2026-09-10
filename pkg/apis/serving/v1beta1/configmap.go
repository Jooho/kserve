/*
Copyright 2021 The KServe Authors.

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

package v1beta1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/constants"
	kernelcachetypes "github.com/kserve/kserve/pkg/kernelcache/types"
	"github.com/kserve/kserve/pkg/types"
	"github.com/kserve/kserve/pkg/utils"
)

// ConfigMap Keys
const (
	ExplainerConfigKeyName             = "explainers"
	InferenceServiceConfigKeyName      = "inferenceService"
	IngressConfigKeyName               = "ingress"
	DeployConfigName                   = "deploy"
	LocalModelConfigName               = "localModel"
	SecurityConfigName                 = "security"
	ServiceConfigName                  = "service"
	ResourceConfigName                 = "resource"
	MultiNodeConfigKeyName             = "multiNode"
	OtelCollectorConfigName            = "opentelemetryCollector"
	StorageInitializerConfigMapKeyName = "storageInitializer"
	AutoscalerConfigName               = "autoscaler"
	KernelCacheConfigName              = "kernelcache"
)

const (
	DefaultDomainTemplate = "{{ .Name }}-{{ .Namespace }}.{{ .IngressDomain }}"
	DefaultIngressDomain  = "example.com"
	DefaultUrlScheme      = "http"

	DefaultModelBasedRoutingHeaderName = "X-Gateway-Model-Name"
	DefaultModelBasedRoutingMode       = "enabled"
	DefaultLoRAModelRoutingStrategy    = constants.LoRAModelRoutingStrategyExact
)

// Error messages
const (
	ErrKserveIngressGatewayRequired         = "invalid ingress config - kserveIngressGateway is required"
	ErrInvalidKserveIngressGatewayFormat    = "invalid ingress config - kserveIngressGateway should be in the format <namespace>/<name>"
	ErrInvalidKserveIngressGatewayName      = "invalid ingress config - kserveIngressGateway gateway name is invalid"
	ErrInvalidKserveIngressGatewayNamespace = "invalid ingress config - kserveIngressGateway gateway namespace is invalid"
)

// +kubebuilder:object:generate=false
type ExplainerConfig struct {
	// explainer docker image name
	ContainerImage string `json:"image"`
	// default explainer docker image version
	DefaultImageVersion string `json:"defaultImageVersion"`
}

// +kubebuilder:object:generate=false
type ExplainersConfig struct {
	ARTExplainer ExplainerConfig `json:"art,omitempty"`
}

type OtelCollectorConfig struct {
	ScrapeInterval         string         `json:"scrapeInterval,omitempty"`
	MetricReceiverEndpoint string         `json:"metricReceiverEndpoint,omitempty"`
	MetricScalerEndpoint   string         `json:"metricScalerEndpoint,omitempty"`
	Resource               ResourceConfig `json:"resource,omitempty"` // Resource configuration for otel collector
}

type AutoscalerConfig struct {
	ScaleUpStabilizationWindowSeconds   string `json:"scaleUpStabilizationWindowSeconds,omitempty"`
	ScaleDownStabilizationWindowSeconds string `json:"scaleDownStabilizationWindowSeconds,omitempty"`
}

// +kubebuilder:object:generate=false
type InferenceServicesConfig struct {
	// Explainer configurations
	Explainers ExplainersConfig `json:"explainers"`
	// ServiceAnnotationDisallowedList is a list of annotations that are not allowed to be propagated to Knative
	// revisions
	ServiceAnnotationDisallowedList []string `json:"serviceAnnotationDisallowedList,omitempty"`
	// ServiceLabelDisallowedList is a list of labels that are not allowed to be propagated to Knative revisions
	ServiceLabelDisallowedList []string `json:"serviceLabelDisallowedList,omitempty"`
	// Resource configurations
	Resource ResourceConfig `json:"resource,omitempty"`
}

// +kubebuilder:object:generate=false
type MultiNodeConfig struct {
	// CustomGPUResourceTypeList is a list of custom GPU resource types that are allowed to be used in the ServingRuntime and inferenceService
	CustomGPUResourceTypeList []string `json:"customGPUResourceTypeList,omitempty"`
}

// +kubebuilder:object:generate=false
type IngressConfig struct {
	EnableGatewayAPI             bool      `json:"enableGatewayApi,omitempty"`
	KserveIngressGateway         string    `json:"kserveIngressGateway,omitempty"`
	IngressGateway               string    `json:"ingressGateway,omitempty"`
	KnativeLocalGatewayService   string    `json:"knativeLocalGatewayService,omitempty"`
	LocalGateway                 string    `json:"localGateway,omitempty"`
	LocalGatewayServiceName      string    `json:"localGatewayService,omitempty"`
	IngressDomain                string    `json:"ingressDomain,omitempty"`
	IngressClassName             *string   `json:"ingressClassName,omitempty"`
	AdditionalIngressDomains     *[]string `json:"additionalIngressDomains,omitempty"`
	DomainTemplate               string    `json:"domainTemplate,omitempty"`
	UrlScheme                    string    `json:"urlScheme,omitempty"`
	EnableLLMInferenceServiceTLS bool      `json:"enableLLMInferenceServiceTLS,omitempty"`
	DisableIstioVirtualHost      bool      `json:"disableIstioVirtualHost,omitempty"`
	PathTemplate                 string    `json:"pathTemplate,omitempty"`
	DisableIngressCreation       bool      `json:"disableIngressCreation,omitempty"`
	DisableHTTPRouteTimeout      bool      `json:"disableHTTPRouteTimeout,omitempty"`

	ModelBasedRoutingHeaderName string `json:"modelBasedRoutingHeaderName,omitempty"`
	ModelBasedRoutingMode       string `json:"modelBasedRoutingMode,omitempty"`

	// LoRAModelRoutingStrategy selects how LLMInferenceService LoRA adapter
	// expansion represents model identities in generated HTTPRoutes: "exact"
	// (the default) or "regex", compared case-insensitively. Any other value
	// fails config loading like the other ingress keys.
	LoRAModelRoutingStrategy string `json:"loraModelRoutingStrategy,omitempty"`
}

// +kubebuilder:object:generate=false
type DeployConfig struct {
	DefaultDeploymentMode     string                     `json:"defaultDeploymentMode,omitempty"`
	DeploymentRolloutStrategy *DeploymentRolloutStrategy `json:"deploymentRolloutStrategy,omitempty"`
}

// DeploymentRolloutStrategy defines the rollout strategy configuration for deployments
type DeploymentRolloutStrategy struct {
	// DefaultRollout specifies the default rollout configuration
	// +optional
	DefaultRollout *RolloutSpec `json:"defaultRollout,omitempty"`
}

// RolloutSpec defines the rollout strategy configuration using Kubernetes deployment strategy
type RolloutSpec struct {
	// MaxSurge specifies the maximum number of pods that can be created above the desired replica count.
	// Can be an absolute number (ex: 5) or a percentage of desired pods (ex: 10%).
	MaxSurge string `json:"maxSurge"`
	// MaxUnavailable specifies the maximum number of pods that can be unavailable during the update.
	// Can be an absolute number (ex: 5) or a percentage of desired pods (ex: 10%).
	MaxUnavailable string `json:"maxUnavailable"`
}

// +kubebuilder:object:generate=false
type LocalModelConfig struct {
	Enabled                      bool   `json:"enabled"`
	JobNamespace                 string `json:"jobNamespace"`
	DefaultJobImage              string `json:"defaultJobImage,omitempty"`
	FSGroup                      *int64 `json:"fsGroup,omitempty"`
	JobTTLSecondsAfterFinished   *int32 `json:"jobTTLSecondsAfterFinished,omitempty"`
	ReconcilationFrequencyInSecs *int64 `json:"reconcilationFrequencyInSecs,omitempty"`
	DisableVolumeManagement      bool   `json:"disableVolumeManagement,omitempty"`
}

const (
	DefaultKernelCacheMCVImage                                = "quay.io/gkm/mcv:latest"
	DefaultKernelCachePrefetchImage                           = "registry.access.redhat.com/ubi9/ubi-minimal:latest"
	DefaultKernelCacheMCVCaptureReadinessTimeoutSeconds int64 = 600
	DefaultKernelCacheOpenShiftRegistryEndpoint               = "image-registry.openshift-image-registry.svc:5000"
	DefaultKernelCacheOpenShiftCAConfigMapName                = "openshift-service-ca.crt"
	DefaultKernelCacheOpenShiftCAConfigMapKey                 = "service-ca.crt"
)

// +kubebuilder:object:generate=false
type KernelCacheConfig struct {
	Enabled                 bool   `json:"enabled"`
	DefaultSidecarInjection bool   `json:"defaultSidecarInjection"`
	DefaultMountType        string `json:"defaultMountType,omitempty"`
	// DefaultNodeGroup is used when an automatically created KernelCache has no workload override.
	DefaultNodeGroup string `json:"defaultNodeGroup,omitempty"`
	JobNamespace     string `json:"jobNamespace"`
	MCVImage         string `json:"mcvImage,omitempty"`
	// MCVCaptureReadinessTimeoutSeconds limits how long the MCV capture sidecar waits for workload readiness.
	MCVCaptureReadinessTimeoutSeconds int64                             `json:"mcvCaptureReadinessTimeoutSeconds,omitempty"`
	PrefetchImage                     string                            `json:"prefetchImage,omitempty"`
	Registry                          KernelCacheRegistryConfig         `json:"registry,omitempty"`
	ArtifactSecurity                  KernelCacheArtifactSecurityConfig `json:"artifactSecurity,omitempty"`
	JobTTLSecondsAfterFinished        *int32                            `json:"jobTTLSecondsAfterFinished,omitempty"`
	ReconcileIntervalSeconds          *int64                            `json:"reconcileIntervalSeconds,omitempty"`
	// AbandonedCapturePolicy controls generated captures whose producer Pod disappears before completion.
	AbandonedCapturePolicy string `json:"abandonedCapturePolicy,omitempty"`
	// CachePaths is resolved for each Pod and is not read from the ConfigMap.
	// +listType=atomic
	CachePaths []v1alpha1.KernelCachePath `json:"-"`
	// TargetImage is resolved for each Pod and is not read from the ConfigMap.
	TargetImage string `json:"-"`
	// +listType=atomic
	ReadinessEnv         []corev1.EnvVar `json:"-"`
	CredentialSecretName string          `json:"-"`
	ReporterSecretName   string          `json:"-"`
	CaptureName          string          `json:"-"`
	CaptureNamespace     string          `json:"-"`
	CaptureSessionID     string          `json:"-"`
}

// +kubebuilder:object:generate=false
type KernelCacheArtifactSecurityConfig struct {
	Mode          string                        `json:"mode,omitempty"`
	FailurePolicy string                        `json:"failurePolicy,omitempty"`
	Cert          KernelCacheArtifactCertConfig `json:"cert,omitempty"`
}

// +kubebuilder:object:generate=false
type KernelCacheArtifactCertConfig struct {
	SigningProfileRef string `json:"signingProfileRef,omitempty"`
	TrustBundle       string `json:"trustBundle,omitempty"`
	TrustBundleKey    string `json:"trustBundleKey,omitempty"`
	SubjectRegexp     string `json:"subjectRegexp,omitempty"`
}

func (c KernelCacheArtifactSecurityConfig) ToSecurityConfig() kernelcachetypes.SecurityConfig {
	return kernelcachetypes.SecurityConfig{
		Mode:          kernelcachetypes.Mode(c.Mode),
		FailurePolicy: kernelcachetypes.FailurePolicy(c.FailurePolicy),
		Cert: kernelcachetypes.CertConfig{
			TrustBundle:    c.Cert.TrustBundle,
			TrustBundleKey: c.Cert.TrustBundleKey,
			SubjectRegexp:  c.Cert.SubjectRegexp,
		},
	}
}

// DeepCopy returns an independent configuration for one Pod admission.
func (c *KernelCacheConfig) DeepCopy() *KernelCacheConfig {
	out := *c
	out.CachePaths = append([]v1alpha1.KernelCachePath(nil), c.CachePaths...)
	out.ReadinessEnv = (&corev1.Container{Env: c.ReadinessEnv}).DeepCopy().Env
	if c.Registry.Auth.OpenShift != nil {
		value := *c.Registry.Auth.OpenShift
		out.Registry.Auth.OpenShift = &value
	}
	if c.Registry.CAConfigMapRef != nil {
		value := *c.Registry.CAConfigMapRef
		out.Registry.CAConfigMapRef = &value
	}
	if c.JobTTLSecondsAfterFinished != nil {
		value := *c.JobTTLSecondsAfterFinished
		out.JobTTLSecondsAfterFinished = &value
	}
	if c.ReconcileIntervalSeconds != nil {
		value := *c.ReconcileIntervalSeconds
		out.ReconcileIntervalSeconds = &value
	}
	return &out
}

// +kubebuilder:object:generate=false
type KernelCacheRegistryConfig struct {
	Endpoint       string                      `json:"endpoint,omitempty"`
	Auth           KernelCacheRegistryAuth     `json:"auth,omitempty"`
	CAConfigMapRef *KernelCacheConfigMapKeyRef `json:"caConfigMapRef,omitempty"`
}

// +kubebuilder:object:generate=false
type KernelCacheRegistryAuth struct {
	Type      string                    `json:"type,omitempty"`
	OpenShift *KernelCacheOpenShiftAuth `json:"openshift,omitempty"`
}

// +kubebuilder:object:generate=false
type KernelCacheOpenShiftAuth struct {
	TokenTTLSeconds int64 `json:"tokenTTLSeconds,omitempty"`
}

// +kubebuilder:object:generate=false
type KernelCacheConfigMapKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// +kubebuilder:object:generate=false
type ResourceConfig struct {
	CPULimit      string `json:"cpuLimit,omitempty"`
	MemoryLimit   string `json:"memoryLimit,omitempty"`
	CPURequest    string `json:"cpuRequest,omitempty"`
	MemoryRequest string `json:"memoryRequest,omitempty"`
}

// +kubebuilder:object:generate=false
type SecurityConfig struct {
	AutoMountServiceAccountToken bool `json:"autoMountServiceAccountToken"`
}

// +kubebuilder:object:generate=false
type ServiceConfig struct {
	// ServiceClusterIPNone is a boolean flag to indicate if the service should have a clusterIP set to None.
	// If the DeploymentMode is Raw, the default value for ServiceClusterIPNone is false when the value is absent.
	ServiceClusterIPNone bool `json:"serviceClusterIPNone,omitempty"`
}

func GetInferenceServiceConfigMap(ctx context.Context, clientset kubernetes.Interface) (*corev1.ConfigMap, error) {
	if configMap, err := clientset.CoreV1().ConfigMaps(constants.KServeNamespace).Get(
		ctx, constants.InferenceServiceConfigMapName, metav1.GetOptions{}); err != nil {
		return nil, err
	} else {
		return configMap, nil
	}
}

func NewOtelCollectorConfig(isvcConfigMap *corev1.ConfigMap) (*OtelCollectorConfig, error) {
	otelConfig := &OtelCollectorConfig{}
	if otel, ok := isvcConfigMap.Data[OtelCollectorConfigName]; ok {
		err := json.Unmarshal([]byte(otel), otelConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to parse otel config json: %w", err)
		}
	}
	return otelConfig, nil
}

func NewAutoscalerConfig(isvcConfigMap *corev1.ConfigMap) (*AutoscalerConfig, error) {
	autoscalerConfig := &AutoscalerConfig{}
	if autoscaler, ok := isvcConfigMap.Data[AutoscalerConfigName]; ok {
		err := json.Unmarshal([]byte(autoscaler), autoscalerConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to parse autoscaler config json: %w", err)
		}
	}
	return autoscalerConfig, nil
}

func NewInferenceServicesConfig(isvcConfigMap *corev1.ConfigMap) (*InferenceServicesConfig, error) {
	icfg := &InferenceServicesConfig{}
	for _, err := range []error{
		getComponentConfig(ExplainerConfigKeyName, isvcConfigMap, &icfg.Explainers),
		getComponentConfig(InferenceServiceConfigKeyName, isvcConfigMap, &icfg),
	} {
		if err != nil {
			return nil, err
		}
	}

	if isvc, ok := isvcConfigMap.Data[InferenceServiceConfigKeyName]; ok {
		errisvc := json.Unmarshal([]byte(isvc), &icfg)
		if errisvc != nil {
			return nil, fmt.Errorf("unable to parse isvc config json: %w", errisvc)
		}
		if icfg.ServiceAnnotationDisallowedList == nil {
			icfg.ServiceAnnotationDisallowedList = constants.ServiceAnnotationDisallowedList
		} else {
			icfg.ServiceAnnotationDisallowedList = append(
				constants.ServiceAnnotationDisallowedList,
				icfg.ServiceAnnotationDisallowedList...)
		}
		if icfg.ServiceLabelDisallowedList == nil {
			icfg.ServiceLabelDisallowedList = constants.RevisionTemplateLabelDisallowedList
		} else {
			icfg.ServiceLabelDisallowedList = append(
				constants.RevisionTemplateLabelDisallowedList,
				icfg.ServiceLabelDisallowedList...)
		}
	}
	return icfg, nil
}

func NewMultiNodeConfig(isvcConfigMap *corev1.ConfigMap) (*MultiNodeConfig, error) {
	mncfg := &MultiNodeConfig{}
	for _, err := range []error{
		getComponentConfig(MultiNodeConfigKeyName, isvcConfigMap, &mncfg),
	} {
		if err != nil {
			return nil, err
		}
	}

	if mncfg.CustomGPUResourceTypeList == nil {
		mncfg.CustomGPUResourceTypeList = []string{}
	}

	// update global GPU resource type list
	_ = utils.UpdateGlobalGPUResourceTypeList(append(mncfg.CustomGPUResourceTypeList, constants.DefaultGPUResourceTypeList...))
	return mncfg, nil
}

func validateIngressGateway(ingressConfig *IngressConfig) error {
	if ingressConfig.KserveIngressGateway == "" {
		return errors.New(ErrKserveIngressGatewayRequired)
	}
	splits := strings.Split(ingressConfig.KserveIngressGateway, "/")
	if len(splits) != 2 {
		return errors.New(ErrInvalidKserveIngressGatewayFormat)
	}
	errs := validation.IsDNS1123Label(splits[0])
	if len(errs) != 0 {
		return errors.New(ErrInvalidKserveIngressGatewayNamespace)
	}
	errs = validation.IsDNS1123Label(splits[1])
	if len(errs) != 0 {
		return errors.New(ErrInvalidKserveIngressGatewayName)
	}
	return nil
}

func NewIngressConfig(isvcConfigMap *corev1.ConfigMap) (*IngressConfig, error) {
	ingressConfig := &IngressConfig{}
	if ingress, ok := isvcConfigMap.Data[IngressConfigKeyName]; ok {
		err := json.Unmarshal([]byte(ingress), &ingressConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to parse ingress config json: %w", err)
		}
		if ingressConfig.EnableGatewayAPI {
			if ingressConfig.KserveIngressGateway == "" {
				return nil, errors.New("invalid ingress config - kserveIngressGateway is required")
			}
			if err := validateIngressGateway(ingressConfig); err != nil {
				return nil, err
			}
		}
		if ingressConfig.IngressGateway == "" {
			return nil, errors.New("invalid ingress config - ingressGateway is required")
		}
		if ingressConfig.PathTemplate != "" {
			// TODO: ensure that the generated path is valid, that is:
			// * both Name and Namespace are used to avoid collisions
			// * starts with a /
			// For now simply check that this is a valid template.
			_, err := template.New("path-template").Parse(ingressConfig.PathTemplate)
			if err != nil {
				return nil, fmt.Errorf("invalid ingress config, unable to parse pathTemplate: %w", err)
			}
			if ingressConfig.IngressDomain == "" {
				return nil, errors.New("invalid ingress config - ingressDomain is required if pathTemplate is given")
			}
		}

		if len(ingressConfig.KnativeLocalGatewayService) == 0 {
			ingressConfig.KnativeLocalGatewayService = ingressConfig.LocalGatewayServiceName
		}
	}

	if ingressConfig.DomainTemplate == "" {
		ingressConfig.DomainTemplate = DefaultDomainTemplate
	}

	if ingressConfig.IngressDomain == "" {
		ingressConfig.IngressDomain = DefaultIngressDomain
	}

	if ingressConfig.UrlScheme == "" {
		ingressConfig.UrlScheme = DefaultUrlScheme
	}

	if ingressConfig.ModelBasedRoutingHeaderName == "" {
		ingressConfig.ModelBasedRoutingHeaderName = DefaultModelBasedRoutingHeaderName
	}

	if ingressConfig.ModelBasedRoutingMode == "" {
		ingressConfig.ModelBasedRoutingMode = DefaultModelBasedRoutingMode
	}

	switch strategy := strings.ToLower(strings.TrimSpace(ingressConfig.LoRAModelRoutingStrategy)); strategy {
	case "":
		ingressConfig.LoRAModelRoutingStrategy = DefaultLoRAModelRoutingStrategy
	case constants.LoRAModelRoutingStrategyExact, constants.LoRAModelRoutingStrategyRegex:
		ingressConfig.LoRAModelRoutingStrategy = strategy
	default:
		return nil, fmt.Errorf("invalid ingress config - loraModelRoutingStrategy must be %q or %q, got %q",
			constants.LoRAModelRoutingStrategyExact, constants.LoRAModelRoutingStrategyRegex, ingressConfig.LoRAModelRoutingStrategy)
	}

	return ingressConfig, nil
}

func getComponentConfig(key string, configMap *corev1.ConfigMap, componentConfig interface{}) error {
	if data, ok := configMap.Data[key]; ok {
		err := json.Unmarshal([]byte(data), componentConfig)
		if err != nil {
			return fmt.Errorf("unable to unmarshall %v json string due to %w ", key, err)
		}
	}
	return nil
}

func NewDeployConfig(isvcConfigMap *corev1.ConfigMap) (*DeployConfig, error) {
	deployConfig := &DeployConfig{}
	if deploy, ok := isvcConfigMap.Data[DeployConfigName]; ok {
		err := json.Unmarshal([]byte(deploy), &deployConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to parse deploy config json: %w", err)
		}

		if deployConfig.DefaultDeploymentMode == "" {
			return nil, errors.New("invalid deploy config, defaultDeploymentMode is required")
		}

		if deployConfig.DefaultDeploymentMode == string(constants.LegacyServerless) {
			// LegacyServerless is deprecated, so we convert it to Knative
			deployConfig.DefaultDeploymentMode = string(constants.Knative)
		}
		if deployConfig.DefaultDeploymentMode == string(constants.LegacyRawDeployment) {
			// LegacyRawDeployment is deprecated, so we convert it to Standard
			deployConfig.DefaultDeploymentMode = string(constants.Standard)
		}

		if deployConfig.DefaultDeploymentMode != string(constants.Knative) &&
			deployConfig.DefaultDeploymentMode != string(constants.Standard) &&
			deployConfig.DefaultDeploymentMode != string(constants.ModelMeshDeployment) {
			return nil, errors.New("invalid deployment mode. Supported modes are Knative," +
				" Standard and ModelMesh")
		}
	}
	return deployConfig, nil
}

func NewLocalModelConfig(isvcConfigMap *corev1.ConfigMap) (*LocalModelConfig, error) {
	localModelConfig := &LocalModelConfig{}
	if localModel, ok := isvcConfigMap.Data[LocalModelConfigName]; ok {
		err := json.Unmarshal([]byte(localModel), &localModelConfig)
		if err != nil {
			return nil, err
		}
	}
	return localModelConfig, nil
}

func NewKernelCacheConfig(isvcConfigMap *corev1.ConfigMap) (*KernelCacheConfig, error) {
	kernelCacheConfig := &KernelCacheConfig{
		DefaultSidecarInjection:           true,
		MCVImage:                          DefaultKernelCacheMCVImage,
		MCVCaptureReadinessTimeoutSeconds: DefaultKernelCacheMCVCaptureReadinessTimeoutSeconds,
		PrefetchImage:                     DefaultKernelCachePrefetchImage,
		ArtifactSecurity: KernelCacheArtifactSecurityConfig{
			Mode:          string(kernelcachetypes.ModeNone),
			FailurePolicy: string(kernelcachetypes.FailurePolicyReject),
		},
		Registry: KernelCacheRegistryConfig{
			Auth: KernelCacheRegistryAuth{Type: "none"},
		},
		AbandonedCapturePolicy: "retain",
	}
	if kernelCache, ok := isvcConfigMap.Data[KernelCacheConfigName]; ok {
		var legacy struct {
			Registry map[string]json.RawMessage `json:"registry"`
		}
		if err := json.Unmarshal([]byte(kernelCache), &legacy); err != nil {
			return nil, fmt.Errorf("unable to unmarshal kernelcache: %w", err)
		}
		if _, ok := legacy.Registry["authMode"]; ok {
			return nil, errors.New("registry.authMode was replaced by registry.auth.type")
		}
		if _, ok := legacy.Registry["token"]; ok {
			return nil, errors.New("registry.token was replaced by Secret-bound registry access")
		}
		if err := json.Unmarshal([]byte(kernelCache), kernelCacheConfig); err != nil {
			return nil, fmt.Errorf("unable to unmarshal kernelcache: %w", err)
		}
	}
	if kernelCacheConfig.MCVImage == "" {
		kernelCacheConfig.MCVImage = DefaultKernelCacheMCVImage
	}
	if kernelCacheConfig.PrefetchImage == "" {
		kernelCacheConfig.PrefetchImage = DefaultKernelCachePrefetchImage
	}
	if kernelCacheConfig.AbandonedCapturePolicy == "" {
		kernelCacheConfig.AbandonedCapturePolicy = "retain"
	}
	if kernelCacheConfig.AbandonedCapturePolicy != "retain" && kernelCacheConfig.AbandonedCapturePolicy != "delete" {
		return nil, errors.New("kernelcache.abandonedCapturePolicy must be retain or delete")
	}
	if kernelCacheConfig.DefaultMountType != "" && kernelCacheConfig.DefaultMountType != "oci" {
		return nil, fmt.Errorf("kernelcache.defaultMountType must be oci, got %q", kernelCacheConfig.DefaultMountType)
	}
	if kernelCacheConfig.MCVCaptureReadinessTimeoutSeconds <= 0 {
		return nil, errors.New("kernelcache.mcvCaptureReadinessTimeoutSeconds must be greater than zero")
	}
	if kernelCacheConfig.Registry.Auth.Type == "openshift" {
		if kernelCacheConfig.Registry.Endpoint == "" {
			kernelCacheConfig.Registry.Endpoint = DefaultKernelCacheOpenShiftRegistryEndpoint
		}
		if kernelCacheConfig.Registry.CAConfigMapRef == nil {
			kernelCacheConfig.Registry.CAConfigMapRef = &KernelCacheConfigMapKeyRef{
				Name: DefaultKernelCacheOpenShiftCAConfigMapName,
				Key:  DefaultKernelCacheOpenShiftCAConfigMapKey,
			}
		}
	}
	if err := kernelCacheConfig.Registry.Validate(); err != nil {
		return nil, err
	}
	artifactSecurity := kernelCacheConfig.ArtifactSecurity.ToSecurityConfig()
	artifactSecurity.Default()
	if err := artifactSecurity.Validate(); err != nil {
		return nil, fmt.Errorf("invalid kernelcache.artifactSecurity: %w", err)
	}
	if artifactSecurity.FailurePolicy != kernelcachetypes.FailurePolicyReject {
		return nil, errors.New("kernelcache.artifactSecurity.failurePolicy must be reject")
	}
	if artifactSecurity.Mode == kernelcachetypes.ModeCert && strings.TrimSpace(kernelCacheConfig.ArtifactSecurity.Cert.SigningProfileRef) == "" {
		return nil, errors.New("kernelcache.artifactSecurity.cert.signingProfileRef is required for cert mode")
	}
	kernelCacheConfig.ArtifactSecurity.Mode = string(artifactSecurity.Mode)
	kernelCacheConfig.ArtifactSecurity.FailurePolicy = string(artifactSecurity.FailurePolicy)
	return kernelCacheConfig, nil
}

func (c KernelCacheRegistryConfig) Validate() error {
	switch c.Auth.Type {
	case "", "none":
		if c.Auth.OpenShift != nil {
			return errors.New("registry.auth.openshift requires type openshift")
		}
	case "openshift":
		if c.Endpoint == "" || strings.ContainsAny(c.Endpoint, "/ \t\n") {
			return errors.New("registry.endpoint must be a registry host with optional port")
		}
		if c.Auth.OpenShift != nil && c.Auth.OpenShift.TokenTTLSeconds != 0 && (c.Auth.OpenShift.TokenTTLSeconds < 600 || c.Auth.OpenShift.TokenTTLSeconds > 3600) {
			return errors.New("registry.auth.openshift.tokenTTLSeconds must be between 600 and 3600")
		}
	default:
		return fmt.Errorf("unsupported registry.auth.type %q", c.Auth.Type)
	}
	return nil
}

func NewSecurityConfig(isvcConfigMap *corev1.ConfigMap) (*SecurityConfig, error) {
	securityConfig := &SecurityConfig{}
	if security, ok := isvcConfigMap.Data[SecurityConfigName]; ok {
		err := json.Unmarshal([]byte(security), &securityConfig)
		if err != nil {
			return nil, err
		}
	}
	return securityConfig, nil
}

func NewServiceConfig(isvcConfigMap *corev1.ConfigMap) (*ServiceConfig, error) {
	serviceConfig := &ServiceConfig{}
	if service, ok := isvcConfigMap.Data[ServiceConfigName]; ok {
		err := json.Unmarshal([]byte(service), &serviceConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to parse service config json: %w", err)
		}
	}
	return serviceConfig, nil
}

// GetStorageInitializerConfigs parses the StorageInitializer configuration from the provided ConfigMap.
// It unmarshalls JSON configuration under the "storageInitializer" key into a StorageInitializerConfig struct.
// The fields related to CPU and memory resource values are validated to contain valid Kubernetes resource quantities.
//
// Parameters:
//
//	configMap: The Kubernetes ConfigMap containing the storage initializer configuration.
//
// Returns:
//
//	*types.StorageInitializerConfig: The parsed storage initializer configuration.
//	error: An error if the configuration is missing, invalid, or resource values cannot be parsed.
func GetStorageInitializerConfigs(configMap *corev1.ConfigMap) (*types.StorageInitializerConfig, error) {
	storageInitializerConfig := &types.StorageInitializerConfig{}
	if initializerConfig, ok := configMap.Data[StorageInitializerConfigMapKeyName]; ok {
		err := json.Unmarshal([]byte(initializerConfig), &storageInitializerConfig)
		if err != nil {
			panic(fmt.Errorf("Unable to unmarshall %v json string due to %w ", StorageInitializerConfigMapKeyName, err))
		}
	}
	// Ensure that we set proper values for CPU/Memory Limit/Request
	resourceDefaults := map[string]string{
		"memoryRequest": storageInitializerConfig.MemoryRequest,
		"memoryLimit":   storageInitializerConfig.MemoryLimit,
		"cpuRequest":    storageInitializerConfig.CpuRequest,
		"cpuLimit":      storageInitializerConfig.CpuLimit,
	}

	// Only validate optional modelcar fields if they're set
	if storageInitializerConfig.CpuModelcar != "" {
		resourceDefaults["cpuModelcar"] = storageInitializerConfig.CpuModelcar
	}
	if storageInitializerConfig.MemoryModelcar != "" {
		resourceDefaults["memoryModelcar"] = storageInitializerConfig.MemoryModelcar
	}

	for key, value := range resourceDefaults {
		_, err := resource.ParseQuantity(value)
		if err != nil {
			return storageInitializerConfig, fmt.Errorf("failed to parse resource configuration for %q.%q: %w", StorageInitializerConfigMapKeyName, key, err)
		}
	}

	if storageInitializerConfig.OciModelMode != "" {
		switch storageInitializerConfig.OciModelMode {
		case types.OciModelModeModelcar, types.OciModelModeNative, types.OciModelModeFetch:
		default:
			return nil, fmt.Errorf("invalid %q.ociModelMode %q: must be one of %q, %q, %q",
				StorageInitializerConfigMapKeyName,
				storageInitializerConfig.OciModelMode,
				types.OciModelModeModelcar, types.OciModelModeNative, types.OciModelModeFetch)
		}
	}

	return storageInitializerConfig, nil
}
