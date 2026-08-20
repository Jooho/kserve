package kservemodule

import (
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
)

func TestSetPreventDeletionEnv(t *testing.T) {
	newDeployment := func(containers ...corev1.Container) *appsv1.Deployment {
		return &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{Containers: containers},
				},
			},
		}
	}

	t.Run("appends env when missing", func(t *testing.T) {
		g := NewWithT(t)
		dep := newDeployment(corev1.Container{Name: "manager"})

		g.Expect(setPreventDeletionEnv(dep, "false")).To(BeTrue())
		env := dep.Spec.Template.Spec.Containers[0].Env
		g.Expect(env).To(ConsistOf(corev1.EnvVar{Name: preventWellKnownConfigDeletionEnv, Value: "false"}))
	})

	t.Run("updates env when value differs", func(t *testing.T) {
		g := NewWithT(t)
		dep := newDeployment(corev1.Container{
			Name: "manager",
			Env:  []corev1.EnvVar{{Name: preventWellKnownConfigDeletionEnv, Value: "true"}},
		})

		g.Expect(setPreventDeletionEnv(dep, "false")).To(BeTrue())
		g.Expect(dep.Spec.Template.Spec.Containers[0].Env[0].Value).To(Equal("false"))
	})

	t.Run("no-op when already set", func(t *testing.T) {
		g := NewWithT(t)
		dep := newDeployment(corev1.Container{
			Name: "manager",
			Env:  []corev1.EnvVar{{Name: preventWellKnownConfigDeletionEnv, Value: "false"}},
		})

		g.Expect(setPreventDeletionEnv(dep, "false")).To(BeFalse())
	})

	t.Run("only sets on the manager container", func(t *testing.T) {
		g := NewWithT(t)
		dep := newDeployment(
			corev1.Container{Name: "manager"},
			corev1.Container{Name: "kube-rbac-proxy"},
		)

		g.Expect(setPreventDeletionEnv(dep, "false")).To(BeTrue())
		g.Expect(dep.Spec.Template.Spec.Containers[0].Env).To(
			ConsistOf(corev1.EnvVar{Name: preventWellKnownConfigDeletionEnv, Value: "false"}))
		g.Expect(dep.Spec.Template.Spec.Containers[1].Env).To(BeEmpty(),
			"non-manager containers must not receive the env")
	})
}

func TestDeploymentRolloutComplete(t *testing.T) {
	tests := []struct {
		name string
		dep  *appsv1.Deployment
		want bool
	}{
		{
			name: "stale generation",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
				Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 2, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1,
				},
			},
			want: false,
		},
		{
			name: "old pods still present",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 1, AvailableReplicas: 1,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(deploymentRolloutComplete(tt.dep)).To(Equal(tt.want))
		})
	}
}

func TestReferencedByNames(t *testing.T) {
	newConfig := func(refs ...map[string]any) *unstructured.Unstructured {
		cfg := &unstructured.Unstructured{Object: map[string]any{}}
		if refs != nil {
			list := make([]any, len(refs))
			for i := range refs {
				list[i] = refs[i]
			}
			_ = unstructured.SetNestedSlice(cfg.Object, list, "status", "referencedBy")
		}
		return cfg
	}

	t.Run("no status", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(referencedByNames(&unstructured.Unstructured{Object: map[string]any{}})).To(BeEmpty())
	})

	t.Run("namespaced names sorted", func(t *testing.T) {
		g := NewWithT(t)
		cfg := newConfig(
			map[string]any{"name": "svc-b", "namespace": "ns2"},
			map[string]any{"name": "svc-a", "namespace": "ns1"},
		)
		g.Expect(referencedByNames(cfg)).To(Equal([]string{"ns1/svc-a", "ns2/svc-b"}))
	})
}

func TestReferencedConfigBlockers(t *testing.T) {
	g := NewWithT(t)

	config := func(name string, refs ...map[string]any) unstructured.Unstructured {
		cfg := unstructured.Unstructured{Object: map[string]any{}}
		cfg.SetName(name)
		if refs != nil {
			list := make([]any, len(refs))
			for i := range refs {
				list[i] = refs[i]
			}
			_ = unstructured.SetNestedSlice(cfg.Object, list, "status", "referencedBy")
		}
		return cfg
	}

	configs := []unstructured.Unstructured{
		config("cfg-unused"),
		config("cfg-used", map[string]any{"name": "svc1", "namespace": "ns1"}),
	}

	blockers := referencedConfigBlockers(configs)
	g.Expect(blockers).To(ConsistOf("cfg-used (referenced by ns1/svc1)"))
}
