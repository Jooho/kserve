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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestKernelCacheNodeGroup_HasStorage(t *testing.T) {
	if (&KernelCacheNodeGroup{}).HasStorage() {
		t.Fatalf("expected HasStorage=false when Storage is nil")
	}
	g := &KernelCacheNodeGroup{Spec: KernelCacheNodeGroupSpec{Storage: &KernelCacheNodeGroupStorage{}}}
	if !g.HasStorage() {
		t.Fatalf("expected HasStorage=true when Storage is set")
	}
}

func TestKernelCacheNodeGroup_OCIOnly(t *testing.T) {
	g := &KernelCacheNodeGroup{}
	if _, ok := g.PersistentVolumeSpec(); ok {
		t.Fatalf("OCI-only: PersistentVolumeSpec should return ok=false")
	}
	if _, ok := g.PersistentVolumeClaimSpec(); ok {
		t.Fatalf("OCI-only: PersistentVolumeClaimSpec should return ok=false")
	}
	if _, ok := g.StorageLimit(); ok {
		t.Fatalf("OCI-only: StorageLimit should return ok=false")
	}
}

func TestKernelCacheNodeGroup_StorageClassMode(t *testing.T) {
	size := resource.MustParse("100Gi")
	g := &KernelCacheNodeGroup{
		Spec: KernelCacheNodeGroupSpec{
			Storage: &KernelCacheNodeGroupStorage{
				StorageClass: &KernelCacheNodeGroupStorageClass{
					Name:        "fast-ssd",
					Size:        &size,
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			},
		},
	}

	if _, ok := g.PersistentVolumeSpec(); ok {
		t.Fatalf("StorageClass mode: PersistentVolumeSpec should return ok=false")
	}

	pvc, ok := g.PersistentVolumeClaimSpec()
	if !ok {
		t.Fatalf("StorageClass mode: PersistentVolumeClaimSpec should return ok=true")
	}
	if pvc.StorageClassName == nil || *pvc.StorageClassName != "fast-ssd" {
		t.Fatalf("expected derived StorageClassName=fast-ssd, got %v", pvc.StorageClassName)
	}
	if len(pvc.AccessModes) != 1 || pvc.AccessModes[0] != corev1.ReadWriteOnce {
		t.Fatalf("expected derived AccessModes=[RWO], got %v", pvc.AccessModes)
	}
	if got := pvc.Resources.Requests[corev1.ResourceStorage]; got.Cmp(size) != 0 {
		t.Fatalf("expected derived storage request=%s, got %s", size.String(), got.String())
	}

	limit, ok := g.StorageLimit()
	if !ok {
		t.Fatalf("StorageClass mode: StorageLimit should return ok=true (mirrors Size)")
	}
	if limit.Cmp(size) != 0 {
		t.Fatalf("expected StorageLimit=%s, got %s", size.String(), limit.String())
	}
}

func TestKernelCacheNodeGroup_StorageClassModeRequiresSize(t *testing.T) {
	g := &KernelCacheNodeGroup{
		Spec: KernelCacheNodeGroupSpec{
			Storage: &KernelCacheNodeGroupStorage{
				StorageClass: &KernelCacheNodeGroupStorageClass{
					Name:        "fast-ssd",
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			},
		},
	}

	if _, ok := g.PersistentVolumeClaimSpec(); ok {
		t.Fatal("StorageClass mode without Size: PersistentVolumeClaimSpec should return ok=false")
	}
	if _, ok := g.StorageLimit(); ok {
		t.Fatal("StorageClass mode without Size: StorageLimit should return ok=false")
	}
}

func TestKernelCacheNodeGroup_VerbatimMode(t *testing.T) {
	pvSpec := corev1.PersistentVolumeSpec{
		Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("50Gi")},
		PersistentVolumeSource: corev1.PersistentVolumeSource{
			Local: &corev1.LocalVolumeSource{Path: "/mnt/kernelcache"},
		},
	}
	pvcSpec := corev1.PersistentVolumeClaimSpec{
		AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
	}
	limit := resource.MustParse("50Gi")
	g := &KernelCacheNodeGroup{
		Spec: KernelCacheNodeGroupSpec{
			Storage: &KernelCacheNodeGroupStorage{
				PersistentVolumeSpec:      &pvSpec,
				PersistentVolumeClaimSpec: &pvcSpec,
				StorageLimit:              &limit,
			},
		},
	}

	got, ok := g.PersistentVolumeSpec()
	if !ok {
		t.Fatalf("verbatim: PersistentVolumeSpec should return ok=true")
	}
	if got.Local == nil || got.Local.Path != "/mnt/kernelcache" {
		t.Fatalf("expected Local PV path preserved, got %+v", got.PersistentVolumeSource)
	}
	got.Local.Path = "/mutated"
	if g.Spec.Storage.PersistentVolumeSpec.Local.Path == "/mutated" {
		t.Fatalf("PersistentVolumeSpec must return a deep copy")
	}

	gotPVC, ok := g.PersistentVolumeClaimSpec()
	if !ok {
		t.Fatalf("verbatim: PersistentVolumeClaimSpec should return ok=true")
	}
	if len(gotPVC.AccessModes) != 1 || gotPVC.AccessModes[0] != corev1.ReadWriteOnce {
		t.Fatalf("expected verbatim AccessModes preserved, got %v", gotPVC.AccessModes)
	}

	gotLimit, ok := g.StorageLimit()
	if !ok || gotLimit.Cmp(limit) != 0 {
		t.Fatalf("expected verbatim StorageLimit=%s ok=true, got %s ok=%v", limit.String(), gotLimit.String(), ok)
	}
}
