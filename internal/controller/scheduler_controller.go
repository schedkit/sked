/*
Copyright 2025.

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

package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	equality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	skedv1 "github.com/schedkit/sked/api/v1"
	"github.com/schedkit/sked/internal/trust"
)

var (
	errEmptySched      = errors.New("spec.sched must not be empty")
	errMissingVerifier = errors.New("signature verification is enabled but no verifier is configured")
)

// SchedulerReconciler reconciles a Scheduler object
type SchedulerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Policy   trust.PolicyProvider
	Verifier trust.Verifier
}

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sked.schedkit.io,resources=schedulers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sked.schedkit.io,resources=schedulers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sked.schedkit.io,resources=schedulers/finalizers,verbs=update

func (r *SchedulerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var scx skedv1.Scheduler
	if err := r.Get(ctx, req.NamespacedName, &scx); err != nil {
		// A missing Scheduler is the normal outcome during deletion: it is not a
		// failure, so do not log it at error level.
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch Scheduler")
		return ctrl.Result{}, err
	}

	base := scx.DeepCopy()

	scx.Status.ObservedGeneration = scx.Generation

	selected, conflicts, err := r.nodeAssignments(ctx, &scx)
	if err != nil {
		logger.Error(err, "unable to determine node assignments")
		markReconcileFailure(&scx, skedv1.ReasonReconcileFailed, err)
		return ctrl.Result{}, r.updateStatus(ctx, base, &scx, err)
	}
	if selected > 0 && len(conflicts) == selected {
		if err := r.releaseDaemonSet(ctx, &scx); err != nil {
			logger.Error(err, "unable to remove the conflicting scheduler workload")
			markReconcileFailure(&scx, skedv1.ReasonReconcileFailed, err)
			return ctrl.Result{}, r.updateStatus(ctx, base, &scx, err)
		}
		markSchedulerConflict(&scx, conflicts)
		return ctrl.Result{}, r.updateStatus(ctx, base, &scx, nil)
	}
	markSchedulerActive(&scx)

	image, err := r.resolveImage(ctx, &scx)
	if err != nil {
		logger.Error(err, "unable to resolve scheduler image")
		markReconcileFailure(&scx, resolveFailureReason(err), err)
		return ctrl.Result{}, r.updateStatus(ctx, base, &scx, err)
	}

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}

	result, err := controllerutil.CreateOrPatch(ctx, r.Client, ds, func() error {
		if err := controllerutil.SetControllerReference(&scx, ds, r.Scheme); err != nil {
			return err
		}

		if ds.Labels == nil {
			ds.Labels = map[string]string{}
		}
		ds.Labels["managed-by"] = "sked-controller"

		ds.Spec = appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": req.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name":       req.Name,
						"managed-by": "sked-controller",
					},
				},
				Spec: podSpec(&scx, image, conflicts),
			},
		}

		return nil
	})
	if err != nil {
		logger.Error(err, "unable to create or patch DaemonSet")
		markReconcileFailure(&scx, skedv1.ReasonReconcileFailed, err)
		return ctrl.Result{}, r.updateStatus(ctx, base, &scx, err)
	}

	scx.Status.ResolvedImage = image
	applyDaemonSetStatus(&scx, ds)
	if len(conflicts) > 0 {
		markSchedulerNodeConflict(&scx, conflicts)
	}
	if err := r.updateStatus(ctx, base, &scx, nil); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("DaemonSet successfully created or patched", "result", result, "image", image)
	return ctrl.Result{}, nil
}

func (r *SchedulerReconciler) updateStatus(
	ctx context.Context,
	base, scx *skedv1.Scheduler,
	reconcileErr error,
) error {
	if equality.Semantic.DeepEqual(base.Status, scx.Status) {
		return reconcileErr
	}
	// Full Update, not a merge Patch: Patch omits the required zero-valued
	// counters and fails status validation when there are no matching nodes.
	if err := r.Status().Update(ctx, scx); err != nil {
		statusErr := fmt.Errorf("update scheduler status: %w", err)
		if reconcileErr != nil {
			return errors.Join(reconcileErr, statusErr)
		}
		return statusErr
	}
	return reconcileErr
}

func (r *SchedulerReconciler) nodeAssignments(ctx context.Context, scx *skedv1.Scheduler) (int, []nodeConflict, error) {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return 0, nil, fmt.Errorf("list nodes: %w", err)
	}
	var schedulers skedv1.SchedulerList
	if err := r.List(ctx, &schedulers); err != nil {
		return 0, nil, fmt.Errorf("list schedulers: %w", err)
	}

	selected := 0
	var conflicts []nodeConflict
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if !schedulerSelectsNode(scx, node) {
			continue
		}
		selected++
		winner := winningScheduler(schedulers.Items, node)
		if winner != nil && (winner.Namespace != scx.Namespace || winner.Name != scx.Name) {
			hostname := node.Labels[corev1.LabelHostname]
			if hostname == "" {
				hostname = node.Name
			}
			conflicts = append(conflicts, nodeConflict{
				node:     node.Name,
				hostname: hostname,
				winner:   types.NamespacedName{Namespace: winner.Namespace, Name: winner.Name},
			})
		}
	}
	return selected, conflicts, nil
}

func schedulerPrecedes(a, b *skedv1.Scheduler) bool {
	if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
		return a.CreationTimestamp.Before(&b.CreationTimestamp)
	}
	if a.Namespace != b.Namespace {
		return a.Namespace < b.Namespace
	}
	return a.Name < b.Name
}

func (r *SchedulerReconciler) releaseDaemonSet(ctx context.Context, scx *skedv1.Scheduler) error {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: scx.Name, Namespace: scx.Namespace},
	}
	if err := r.Delete(ctx, ds); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete conflicting DaemonSet %s/%s: %w", scx.Namespace, scx.Name, err)
	}
	return nil
}

func (r *SchedulerReconciler) schedulerRequests(ctx context.Context, _ client.Object) []reconcile.Request {
	var schedulers skedv1.SchedulerList
	if err := r.List(ctx, &schedulers); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(schedulers.Items))
	for i := range schedulers.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: schedulers.Items[i].Namespace,
				Name:      schedulers.Items[i].Name,
			},
		})
	}
	return requests
}

func resolveFailureReason(err error) string {
	switch {
	case errors.Is(err, errEmptySched):
		return skedv1.ReasonSpecInvalid
	case errors.Is(err, errMissingVerifier):
		return skedv1.ReasonReconcileFailed
	default:
		return skedv1.ReasonImageVerificationFailed
	}
}

// resolveImage returns the image reference the DaemonSet must run. When
// signature verification is enabled the image is verified and pinned to the
// digest that was verified, so a tag re-pointed after admission cannot change
// what gets scheduled.
func (r *SchedulerReconciler) resolveImage(ctx context.Context, scx *skedv1.Scheduler) (string, error) {
	image := strings.TrimSpace(scx.Spec.Sched)
	if image == "" {
		return "", errEmptySched
	}
	if r.Policy == nil || !r.Policy.Get().SignatureVerificationEnabled() {
		return image, nil
	}
	if r.Verifier == nil {
		return "", errMissingVerifier
	}
	pinned, err := r.Verifier.Verify(ctx, image)
	if err != nil {
		return "", fmt.Errorf("verify scheduler image %q: %w", image, err)
	}
	return pinned, nil
}

func podSpec(scx *skedv1.Scheduler, image string, conflicts []nodeConflict) corev1.PodSpec {
	spec := corev1.PodSpec{
		NodeSelector:     scx.Spec.NodeSelector,
		Tolerations:      scx.Spec.Tolerations,
		ImagePullSecrets: scx.Spec.ImagePullSecrets,
		Containers: []corev1.Container{
			{
				Name:      "scx",
				Image:     image,
				Args:      scx.Spec.Args,
				Env:       scx.Spec.Env,
				Resources: scx.Spec.Resources,
				SecurityContext: &corev1.SecurityContext{
					Privileged: ptr.To(true),
				},
			},
		},
	}
	if len(conflicts) == 0 {
		return spec
	}

	hostnames := make([]string, 0, len(conflicts))
	for i := range conflicts {
		hostnames = append(hostnames, conflicts[i].hostname)
	}
	sort.Strings(hostnames)
	spec.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      corev1.LabelHostname,
								Operator: corev1.NodeSelectorOpNotIn,
								Values:   hostnames,
							},
						},
					},
				},
			},
		},
	}
	return spec
}

// SetupWithManager sets up the controller with the Manager.
func (r *SchedulerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&skedv1.Scheduler{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&skedv1.Scheduler{}, handler.EnqueueRequestsFromMapFunc(r.schedulerRequests)).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.schedulerRequests)).
		// No predicate: DaemonSet status updates do not bump the generation but do
		// carry the rollout state the Scheduler status is derived from.
		Owns(&appsv1.DaemonSet{}).
		Named("scheduler").
		Complete(r)
}
