package kservemodule

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-platform-utilities/api/common"
	"github.com/opendatahub-io/odh-platform-utilities/pkg/controller/conditions"

	platformv1alpha1 "github.com/opendatahub-io/kserve-module/pkg/apis/v1alpha1"
)

// deletionRequeueInterval is the fallback re-check interval for a blocked
// deletion; watches drive most re-checks, this re-reads if an event is missed.
const deletionRequeueInterval = 30 * time.Second

// configCleanupOutcome reports the result of a well-known config cleanup pass.
type configCleanupOutcome struct {
	// done is true when no well-known config remains.
	done bool
	// blocked is true when the configs cannot yet be deleted (still referenced,
	// or the llmisvc controller is unavailable).
	blocked bool
	// blockers describes what is holding deletion, for the status message.
	blockers []string
}

// cleanupLLMISVCConfigsOnDelete deletes the well-known LLMInferenceServiceConfigs
// during Kserve CR deletion. Configs still referenced by an LLMInferenceService
// (per status.referencedBy) are left in place and reported as blockers.
//
// The llmisvc controller's webhook rejects deletion of well-known configs unless
// PREVENT_WELL_KNOWN_CONFIG_DELETION is disabled on its controller deployment, so
// that env is set and its rollout awaited before any delete is issued.
func (r *KserveModuleReconciler) cleanupLLMISVCConfigsOnDelete(ctx context.Context) (configCleanupOutcome, error) {
	log := ctrl.LoggerFrom(ctx)
	ns := r.getApplicationsNamespace()

	configs, err := r.listWellKnownLLMISVCConfigs(ctx, ns)
	if err != nil {
		return configCleanupOutcome{}, err
	}
	if len(configs) == 0 {
		return configCleanupOutcome{done: true}, nil
	}

	controllerUnavailable := configCleanupOutcome{blocked: true, blockers: []string{"llmisvc controller is not available"}}
	dep := &appsv1.Deployment{}
	key := client.ObjectKey{Namespace: ns, Name: llmISVCControllerDeployment}
	if err := r.Get(ctx, key, dep); err != nil {
		if k8serr.IsNotFound(err) {
			return controllerUnavailable, nil
		}
		return configCleanupOutcome{}, fmt.Errorf("getting %s deployment: %w", llmISVCControllerDeployment, err)
	}
	if dep.Status.AvailableReplicas < 1 {
		return controllerUnavailable, nil
	}

	// check-before-delete: never touch a config that is still referenced.
	if blockers := referencedConfigBlockers(configs); len(blockers) > 0 {
		return configCleanupOutcome{blocked: true, blockers: blockers}, nil
	}

	// No references: lift the webhook guard of llmisvc controller and wait for its rollout.
	ready, err := r.ensureDeletionGuardDisabled(ctx, dep)
	if err != nil {
		return configCleanupOutcome{}, err
	}
	if !ready {
		// rollout pending; keep the block visible until the new pod is live.
		return configCleanupOutcome{blocked: true, blockers: []string{"waiting for llmisvc controller rollout"}}, nil
	}

	if err := r.deleteWellKnownConfigs(ctx, configs); err != nil {
		return configCleanupOutcome{}, err
	}

	// Confirm they are actually gone; the config finalizer may still be running,
	// or a service could have appeared mid-flight.
	remaining, err := r.listWellKnownLLMISVCConfigs(ctx, ns)
	if err != nil {
		return configCleanupOutcome{}, err
	}
	if len(remaining) > 0 {
		if blockers := referencedConfigBlockers(remaining); len(blockers) > 0 {
			return configCleanupOutcome{blocked: true, blockers: blockers}, nil
		}
		log.Info("well-known configs still terminating, requeueing", "count", len(remaining))
		return configCleanupOutcome{blocked: true, blockers: []string{"waiting for well-known configs to finish terminating"}}, nil
	}

	return configCleanupOutcome{done: true}, nil
}

// listWellKnownLLMISVCConfigs returns the LLMInferenceServiceConfigs in ns that
// carry the well-known annotation.
func (r *KserveModuleReconciler) listWellKnownLLMISVCConfigs(ctx context.Context, ns string) ([]unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(llmISVCConfigListGVK)
	// APIReader (non-cached): configs are only read at teardown, so avoid a
	// permanent informer and read the current referencedBy directly. Fall back
	// to the cached client when APIReader is unset (reconciler built without
	// SetupWithManager, e.g. in unit tests).
	reader := client.Reader(r.APIReader)
	if reader == nil {
		reader = r.Client
	}
	if err := reader.List(ctx, list, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("listing LLMInferenceServiceConfigs: %w", err)
	}

	var wellKnown []unstructured.Unstructured
	for i := range list.Items {
		if list.Items[i].GetAnnotations()[wellKnownAnnotationKey] == wellKnownAnnotationValue {
			wellKnown = append(wellKnown, list.Items[i])
		}
	}
	return wellKnown, nil
}

func referencedConfigBlockers(configs []unstructured.Unstructured) []string {
	var blockers []string
	for i := range configs {
		if refs := referencedByNames(&configs[i]); len(refs) > 0 {
			blockers = append(blockers, fmt.Sprintf("%s (referenced by %s)", configs[i].GetName(), strings.Join(refs, ", ")))
		}
	}
	sort.Strings(blockers)
	return blockers
}

func referencedByNames(cfg *unstructured.Unstructured) []string {
	refs, found, err := unstructured.NestedSlice(cfg.Object, "status", "referencedBy")
	if err != nil || !found {
		return nil
	}

	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		m, _ := ref.(map[string]any)
		name, _ := m["name"].(string)
		namespace, _ := m["namespace"].(string)
		names = append(names, namespace+"/"+name)
	}
	sort.Strings(names)
	return names
}

// ensureDeletionGuardDisabled disables the well-known config deletion guard on
// the llmisvc controller deployment and reports whether the new pod has rolled
// out, so the change is live before any delete is issued.
func (r *KserveModuleReconciler) ensureDeletionGuardDisabled(ctx context.Context, dep *appsv1.Deployment) (bool, error) {
	log := ctrl.LoggerFrom(ctx)

	original := dep.DeepCopy()
	if setPreventDeletionEnv(dep, "false") {
		if err := r.Patch(ctx, dep, client.MergeFrom(original)); err != nil {
			return false, fmt.Errorf("disabling %s on %s: %w", preventWellKnownConfigDeletionEnv, llmISVCControllerDeployment, err)
		}
		log.Info("disabled well-known config deletion guard for cleanup", "deployment", llmISVCControllerDeployment)
		return false, nil // wait for rollout on the next requeue
	}

	return deploymentRolloutComplete(dep), nil
}

// setPreventDeletionEnv sets PREVENT_WELL_KNOWN_CONFIG_DELETION on the manager
// container (the one that reads it), returning true if it was changed.
func setPreventDeletionEnv(dep *appsv1.Deployment, value string) bool {
	changed := false
	containers := dep.Spec.Template.Spec.Containers
	for i := range containers {
		c := &containers[i]
		if c.Name != llmISVCManagerContainer {
			continue
		}
		found := false
		for j := range c.Env {
			if c.Env[j].Name == preventWellKnownConfigDeletionEnv {
				found = true
				if c.Env[j].Value != value {
					c.Env[j].Value = value
					changed = true
				}
				break
			}
		}
		if !found {
			c.Env = append(c.Env, corev1.EnvVar{Name: preventWellKnownConfigDeletionEnv, Value: value})
			changed = true
		}
	}
	return changed
}

// deploymentRolloutComplete reports whether every replica runs the current pod
// template (no stale pods). Combined with the preceding setPreventDeletionEnv,
// this means the env change is live on the running pods.
func deploymentRolloutComplete(dep *appsv1.Deployment) bool {
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	st := dep.Status
	return st.ObservedGeneration >= dep.Generation &&
		st.UpdatedReplicas >= desired &&
		st.Replicas == st.UpdatedReplicas &&
		st.AvailableReplicas >= desired
}

func (r *KserveModuleReconciler) deleteWellKnownConfigs(ctx context.Context, configs []unstructured.Unstructured) error {
	log := ctrl.LoggerFrom(ctx)
	for i := range configs {
		if !configs[i].GetDeletionTimestamp().IsZero() {
			continue // already terminating
		}
		if err := r.Delete(ctx, &configs[i]); err != nil && !k8serr.IsNotFound(err) {
			return fmt.Errorf("deleting LLMInferenceServiceConfig %s: %w", configs[i].GetName(), err)
		}
		log.Info("deleted well-known LLMInferenceServiceConfig", "name", configs[i].GetName())
	}
	return nil
}

// setDeletionBlocked records the Degraded/DeletionBlocked condition when configs
// still block deletion, or clears it when nothing does, so the condition never
// lingers stale once the block resolves.
func (r *KserveModuleReconciler) setDeletionBlocked(ctx context.Context, kserve *platformv1alpha1.Kserve, blockers []string) error {
	condMgr := newConditionManager(kserve)
	if len(blockers) == 0 {
		condMgr.ClearCondition(string(common.ConditionTypeDegraded))
	} else {
		condMgr.MarkTrue(string(common.ConditionTypeDegraded),
			conditions.WithSeverity(common.ConditionSeverityError),
			conditions.WithReason(ReasonDeletionBlocked),
			conditions.WithMessage("deletion blocked: %s", strings.Join(blockers, "; ")))
	}
	return r.updateStatus(ctx, kserve, condMgr)
}
