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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KernelCacheNodeGroup selects the set of Kubernetes nodes that host prepared
// KernelCache artifacts and describes how those artifacts are persisted on
// each selected node. Node selection is mandatory; storage is optional so that
// OCI-backed KernelCache deployments (which store artifacts in the container
// image layer instead of a PersistentVolume) can share the same grouping
// primitive.
// +genclient
// +genclient:nonNamespaced
// +k8s:openapi-gen=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=kcng
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type KernelCacheNodeGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec KernelCacheNodeGroupSpec `json:"spec,omitempty"`
}

// KernelCacheNodeGroupSpec defines node membership and optional artifact
// storage for a KernelCacheNodeGroup.
// +k8s:openapi-gen=true
type KernelCacheNodeGroupSpec struct {
	// NodeSelector selects the nodes that belong to this group. A node is a
	// member of the group only when it carries every listed label. At least
	// one label is required.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	NodeSelector map[string]string `json:"nodeSelector"`

	// Tolerations are applied to controller-managed workloads that run on the
	// selected nodes (for example, node-local preparation jobs).
	// +optional
	// +listType=atomic
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Storage describes how prepared artifacts are persisted on the selected
	// nodes. Leaving Storage unset selects the OCI-only mode: no
	// PersistentVolume or PersistentVolumeClaim is created for the group and
	// KernelCache artifacts are served from container-image storage instead.
	// +optional
	Storage *KernelCacheNodeGroupStorage `json:"storage,omitempty"`
}

// KernelCacheNodeGroupStorage selects one of two mutually exclusive storage
// modes for the group:
//
//   - StorageClass mode:  dynamic provisioning through a named StorageClass.
//     Use this when the cluster has a working dynamic provisioner and the
//     operator should own volume lifecycle.
//   - Verbatim mode:      caller-supplied PersistentVolume and
//     PersistentVolumeClaim templates, mirroring the shape used by
//     LocalModelNodeGroup. Use this when the cluster does not have a suitable
//     dynamic provisioner and volumes must be pinned to individual nodes
//     (typically Local PVs).
//
// Exactly one of StorageClass or PersistentVolumeSpec must be set.
// PersistentVolumeClaimSpec is required whenever PersistentVolumeSpec is set.
// hostPath volumes are rejected in verbatim mode; use Local PVs instead.
// +k8s:openapi-gen=true
// +kubebuilder:validation:XValidation:rule="(has(self.storageClass) ? 1 : 0) + (has(self.persistentVolumeSpec) ? 1 : 0) == 1",message="exactly one of storageClass or persistentVolumeSpec must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.storageClass) || (!has(self.persistentVolumeClaimSpec) && !has(self.storageLimit))",message="storageClass cannot be combined with persistentVolumeClaimSpec or storageLimit"
// +kubebuilder:validation:XValidation:rule="!has(self.persistentVolumeSpec) || has(self.persistentVolumeClaimSpec)",message="persistentVolumeClaimSpec is required when persistentVolumeSpec is set"
// +kubebuilder:validation:XValidation:rule="!has(self.persistentVolumeSpec) || !has(self.persistentVolumeSpec.hostPath)",message="hostPath volumes are not allowed; use Local PVs instead"
type KernelCacheNodeGroupStorage struct {
	// StorageClass configures dynamic provisioning of one PersistentVolume per
	// selected node from a named StorageClass. Mutually exclusive with the
	// verbatim fields below.
	// +optional
	StorageClass *KernelCacheNodeGroupStorageClass `json:"storageClass,omitempty"`

	// PersistentVolumeSpec is the template used to create one PersistentVolume
	// per selected node in verbatim mode. Mirrors the field of the same name
	// on LocalModelNodeGroup so existing operator plumbing can be reused. Must
	// describe a Local PV (or another node-scoped source); hostPath is
	// rejected.
	// +optional
	PersistentVolumeSpec *corev1.PersistentVolumeSpec `json:"persistentVolumeSpec,omitempty"`

	// PersistentVolumeClaimSpec is the template used to create one
	// PersistentVolumeClaim per selected node in verbatim mode. Required
	// whenever PersistentVolumeSpec is set.
	// +optional
	PersistentVolumeClaimSpec *corev1.PersistentVolumeClaimSpec `json:"persistentVolumeClaimSpec,omitempty"`

	// StorageLimit caps the per-node disk usage in verbatim mode. Interpreted
	// the same way as LocalModelNodeGroup.Spec.StorageLimit. It is not valid in
	// StorageClass mode, where StorageClass.Size provides the capacity.
	// +optional
	StorageLimit *resource.Quantity `json:"storageLimit,omitempty"`
}

// KernelCacheNodeGroupStorageClass configures dynamic provisioning through a
// named StorageClass.
// +k8s:openapi-gen=true
type KernelCacheNodeGroupStorageClass struct {
	// Name is the StorageClass name used to provision node-local volumes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Size is the requested capacity for each provisioned volume.
	// +optional
	// +kubebuilder:validation:XValidation:rule="type(self) == int ? self > 0 : quantity(self).compareTo(quantity('0')) > 0",message="size must be greater than zero when set"
	Size *resource.Quantity `json:"size,omitempty"`

	// AccessModes are the requested access modes for the provisioned volumes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=atomic
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes"`
}

// KernelCacheNodeGroupList contains a list of KernelCacheNodeGroup objects.
// +k8s:openapi-gen=true
// +kubebuilder:object:root=true
type KernelCacheNodeGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KernelCacheNodeGroup `json:"items"`
}

// HasStorage reports whether the group is expected to back KernelCache
// artifacts with a PersistentVolumeClaim. It returns false for OCI-only groups
// (Storage unset) and true for both StorageClass and verbatim modes.
func (g *KernelCacheNodeGroup) HasStorage() bool {
	return g != nil && g.Spec.Storage != nil
}

// PersistentVolumeSpec returns the PersistentVolumeSpec template that the
// operator should use to create one PersistentVolume per selected node.
//
// The second return value is false when no PV template applies:
//   - OCI-only mode (Storage unset): no PV is created.
//   - StorageClass mode: the dynamic provisioner is responsible for creating
//     the PersistentVolume, so the operator does not template one.
//
// Only verbatim mode returns a populated (deep-copied) template.
func (g *KernelCacheNodeGroup) PersistentVolumeSpec() (corev1.PersistentVolumeSpec, bool) {
	if !g.HasStorage() {
		return corev1.PersistentVolumeSpec{}, false
	}
	if g.Spec.Storage.PersistentVolumeSpec != nil {
		return *g.Spec.Storage.PersistentVolumeSpec.DeepCopy(), true
	}
	return corev1.PersistentVolumeSpec{}, false
}

// PersistentVolumeClaimSpec returns the PersistentVolumeClaimSpec template
// that the operator should use to create one PersistentVolumeClaim per
// selected node.
//
// The second return value is false only in OCI-only mode. In StorageClass
// mode a derived PVC template is synthesized from the StorageClass fields so
// that PV/PVC plumbing further downstream can treat both modes uniformly.
func (g *KernelCacheNodeGroup) PersistentVolumeClaimSpec() (corev1.PersistentVolumeClaimSpec, bool) {
	if !g.HasStorage() {
		return corev1.PersistentVolumeClaimSpec{}, false
	}
	if g.Spec.Storage.PersistentVolumeClaimSpec != nil {
		return *g.Spec.Storage.PersistentVolumeClaimSpec.DeepCopy(), true
	}
	if sc := g.Spec.Storage.StorageClass; sc != nil && sc.Size != nil {
		name := sc.Name
		return corev1.PersistentVolumeClaimSpec{
			StorageClassName: &name,
			AccessModes:      append([]corev1.PersistentVolumeAccessMode(nil), sc.AccessModes...),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: sc.Size.DeepCopy(),
				},
			},
		}, true
	}
	return corev1.PersistentVolumeClaimSpec{}, false
}

// StorageLimit returns the per-node storage cap, if any. Verbatim mode returns
// the caller-supplied limit. StorageClass mode returns the StorageClass.Size
// value so callers that gate on total capacity behave uniformly across modes.
// OCI-only mode returns a false second value.
func (g *KernelCacheNodeGroup) StorageLimit() (resource.Quantity, bool) {
	if !g.HasStorage() {
		return resource.Quantity{}, false
	}
	if g.Spec.Storage.StorageLimit != nil {
		return g.Spec.Storage.StorageLimit.DeepCopy(), true
	}
	if sc := g.Spec.Storage.StorageClass; sc != nil && sc.Size != nil {
		return sc.Size.DeepCopy(), true
	}
	return resource.Quantity{}, false
}
