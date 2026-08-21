package kservemodule_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/opendatahub-io/odh-platform-utilities/api/common"

	platformv1alpha1 "github.com/opendatahub-io/kserve-module/pkg/apis/v1alpha1"
	"github.com/opendatahub-io/kserve-module/pkg/kservemodule"
	"github.com/opendatahub-io/kserve-module/pkg/kservemodule/fixture"
)

var llmISVCConfigGVK = kservemodule.LLMISVCConfigGVK

var _ = Describe("LLMInferenceServiceConfig cleanup on delete", Ordered, func() {
	const ns = "opendatahub"

	var cr *platformv1alpha1.Kserve

	BeforeEach(func(ctx SpecContext) {
		cr = fixture.KserveCR()
		Expect(testEnv.Client.Create(ctx, cr)).To(Succeed())
		waitFinalizerAdded(ctx, cr)

		DeferCleanup(func(ctx SpecContext) {
			latest := &platformv1alpha1.Kserve{}
			if err := testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cr), latest); err != nil {
				return
			}
			// Force-drop the finalizer so a spec that left the CR blocked does not
			// wedge the singleton Kserve CR for the next spec.
			if controllerutil.RemoveFinalizer(latest, kservemodule.ModuleFinalizerName) {
				_ = testEnv.Client.Update(ctx, latest)
			}
			_ = client.IgnoreNotFound(testEnv.Client.Delete(ctx, latest))
			Eventually(func(g Gomega) {
				err := testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cr), latest)
				g.Expect(k8serr.IsNotFound(err)).To(BeTrue())
			}).WithContext(ctx).Should(Succeed())
		})
	})

	It("deletes an unreferenced well-known config and releases the finalizer", func(ctx SpecContext) {
		createReadyLLMISVCControllerDeployment(ctx, ns)
		waitLLMISVCControllerReadyInCache(ctx, ns)

		cfg := createWellKnownConfig(ctx, "kserve-config-unref", ns, nil)

		Expect(testEnv.Client.Delete(ctx, cr)).To(Succeed())

		Eventually(func(g Gomega) {
			err := testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cfg), cfg)
			g.Expect(k8serr.IsNotFound(err)).To(BeTrue(), "unreferenced well-known config should be deleted")
		}).WithContext(ctx).Should(Succeed())

		Eventually(func(g Gomega) {
			err := testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			g.Expect(k8serr.IsNotFound(err)).To(BeTrue(), "Kserve CR should be deleted once configs are gone")
		}).WithContext(ctx).Should(Succeed())
	})

	It("keeps the finalizer and reports Degraded while a config is still referenced, then completes when dereferenced", func(ctx SpecContext) {
		createReadyLLMISVCControllerDeployment(ctx, ns)
		waitLLMISVCControllerReadyInCache(ctx, ns)

		cfg := createWellKnownConfig(ctx, "kserve-config-ref", ns, []string{"user-ns/my-llm"})

		Expect(testEnv.Client.Delete(ctx, cr)).To(Succeed())

		By("holding the Kserve CR in Terminating with a Degraded/DeletionBlocked condition")
		Eventually(func(g Gomega) {
			g.Expect(testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cr), cr)).To(Succeed())
			g.Expect(cr.DeletionTimestamp.IsZero()).To(BeFalse())
			g.Expect(cr.Finalizers).To(ContainElement(kservemodule.ModuleFinalizerName))

			cond := fixture.FindCondition(cr, string(common.ConditionTypeDegraded))
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(kservemodule.ReasonDeletionBlocked))
			g.Expect(cond.Message).To(ContainSubstring("user-ns/my-llm"))
		}).WithContext(ctx).Should(Succeed())

		By("keeping the config while it is referenced")
		Consistently(func(g Gomega) {
			g.Expect(testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cfg), cfg)).To(Succeed())
		}).WithContext(ctx).WithTimeout(3 * time.Second).Should(Succeed())

		By("completing deletion once the config is no longer referenced")
		clearReferencedBy(ctx, cfg)
		// The module does not watch configs; nudge a reconcile of the Terminating CR.
		triggerReconcile(ctx, cr, "config-dereferenced")

		Eventually(func(g Gomega) {
			err := testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cfg), cfg)
			g.Expect(k8serr.IsNotFound(err)).To(BeTrue(), "config should be deleted once dereferenced")
		}).WithContext(ctx).WithTimeout(60 * time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			err := testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			g.Expect(k8serr.IsNotFound(err)).To(BeTrue(), "Kserve CR should delete after the block clears")
		}).WithContext(ctx).WithTimeout(60 * time.Second).Should(Succeed())
	})

	It("blocks deletion and keeps the config when the llmisvc controller is absent", func(ctx SpecContext) {
		// No llmisvc controller deployment created.
		cfg := createWellKnownConfig(ctx, "kserve-config-noctrl", ns, nil)

		Expect(testEnv.Client.Delete(ctx, cr)).To(Succeed())

		By("reporting Degraded/DeletionBlocked because the controller is unavailable")
		Eventually(func(g Gomega) {
			g.Expect(testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cr), cr)).To(Succeed())
			g.Expect(cr.DeletionTimestamp.IsZero()).To(BeFalse())
			g.Expect(cr.Finalizers).To(ContainElement(kservemodule.ModuleFinalizerName))

			cond := fixture.FindCondition(cr, string(common.ConditionTypeDegraded))
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(kservemodule.ReasonDeletionBlocked))
			g.Expect(cond.Message).To(ContainSubstring("controller"))
		}).WithContext(ctx).Should(Succeed())

		By("leaving the config untouched without a controller")
		Consistently(func(g Gomega) {
			g.Expect(testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cfg), cfg)).To(Succeed())
		}).WithContext(ctx).WithTimeout(3 * time.Second).Should(Succeed())
	})
})

func createWellKnownConfig(ctx SpecContext, name, namespace string, referencedBy []string) *unstructured.Unstructured {
	cfg := &unstructured.Unstructured{}
	cfg.SetGroupVersionKind(llmISVCConfigGVK)
	cfg.SetName(name)
	cfg.SetNamespace(namespace)
	cfg.SetAnnotations(map[string]string{"serving.kserve.io/well-known-config": "true"})
	Expect(testEnv.Client.Create(ctx, cfg)).To(Succeed())
	DeferCleanup(func(ctx SpecContext) {
		_ = client.IgnoreNotFound(testEnv.Client.Delete(ctx, cfg))
	})

	if len(referencedBy) > 0 {
		setReferencedBy(ctx, cfg, referencedBy)
	}
	return cfg
}

func setReferencedBy(ctx SpecContext, cfg *unstructured.Unstructured, svcs []string) {
	refs := make([]any, 0, len(svcs))
	for _, s := range svcs {
		namespace, name, _ := strings.Cut(s, "/")
		refs = append(refs, map[string]any{"name": name, "namespace": namespace})
	}
	updateConfigStatus(ctx, cfg, func(u *unstructured.Unstructured) error {
		return unstructured.SetNestedSlice(u.Object, refs, "status", "referencedBy")
	})
}

func clearReferencedBy(ctx SpecContext, cfg *unstructured.Unstructured) {
	updateConfigStatus(ctx, cfg, func(u *unstructured.Unstructured) error {
		unstructured.RemoveNestedField(u.Object, "status", "referencedBy")
		return nil
	})
}

func updateConfigStatus(ctx SpecContext, cfg *unstructured.Unstructured, mutate func(*unstructured.Unstructured) error) {
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &unstructured.Unstructured{}
		latest.SetGroupVersionKind(llmISVCConfigGVK)
		if err := testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cfg), latest); err != nil {
			return err
		}
		if err := mutate(latest); err != nil {
			return err
		}
		return testEnv.Client.Status().Update(ctx, latest)
	})).To(Succeed())
}

// createReadyLLMISVCControllerDeployment creates the llmisvc controller deployment with the
// deletion guard already disabled and marks it fully rolled out, so the cleanup
// treats the guard change as live.
func createReadyLLMISVCControllerDeployment(ctx SpecContext, namespace string) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "llmisvc-controller-manager", Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "llmisvc-controller-manager"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "llmisvc-controller-manager"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:  "manager",
					Image: "test:latest",
					Env:   []corev1.EnvVar{{Name: "PREVENT_WELL_KNOWN_CONFIG_DELETION", Value: "false"}},
				}}},
			},
		},
	}
	Expect(testEnv.Client.Create(ctx, dep)).To(Succeed())
	DeferCleanup(func(ctx SpecContext) {
		_ = client.IgnoreNotFound(testEnv.Client.Delete(ctx, dep))
		Eventually(func(g Gomega) {
			err := testEnv.Client.Get(ctx, client.ObjectKeyFromObject(dep), &appsv1.Deployment{})
			g.Expect(k8serr.IsNotFound(err)).To(BeTrue())
		}).WithContext(ctx).Should(Succeed())
	})

	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &appsv1.Deployment{}
		if err := testEnv.Client.Get(ctx, client.ObjectKeyFromObject(dep), latest); err != nil {
			return err
		}
		latest.Status.ObservedGeneration = latest.Generation
		latest.Status.Replicas = 1
		latest.Status.UpdatedReplicas = 1
		latest.Status.AvailableReplicas = 1
		latest.Status.ReadyReplicas = 1
		return testEnv.Client.Status().Update(ctx, latest)
	})).To(Succeed())
}

// waitLLMISVCControllerReadyInCache blocks until the reconciler's cached client
// sees the controller deployment fully rolled out.
func waitLLMISVCControllerReadyInCache(ctx SpecContext, namespace string) {
	Eventually(func(g Gomega) {
		dep := &appsv1.Deployment{}
		g.Expect(testEnv.Reconciler.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "llmisvc-controller-manager"}, dep)).To(Succeed())
		g.Expect(kservemodule.DeploymentRolloutCompleteForTest(dep)).To(BeTrue(), "controller deployment not rolled out in cache")
	}).WithContext(ctx).Should(Succeed())
}

func waitFinalizerAdded(ctx SpecContext, cr *platformv1alpha1.Kserve) {
	Eventually(func(g Gomega) {
		g.Expect(testEnv.Client.Get(ctx, client.ObjectKeyFromObject(cr), cr)).To(Succeed())
		g.Expect(cr.Finalizers).To(ContainElement(kservemodule.ModuleFinalizerName))
	}).WithContext(ctx).WithTimeout(30 * time.Second).Should(Succeed())
}
