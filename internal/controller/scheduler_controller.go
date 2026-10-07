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
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	skedv1 "github.com/schedkit/sked/api/v1"
	"github.com/schedkit/sked/internal/trust"
)

// SchedulerReconciler reconciles a Scheduler object
type SchedulerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Policy   trust.PolicyProvider
	Verifier trust.Verifier
}

// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sked.schedkit.io,resources=schedulers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sked.schedkit.io,resources=schedulers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sked.schedkit.io,resources=schedulers/finalizers,verbs=update

func (r *SchedulerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var scx skedv1.Scheduler
	if err := r.Get(ctx, req.NamespacedName, &scx); err != nil {
		logger.Error(err, "unable to fetch Scheduler")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	image, err := r.resolveImage(ctx, &scx)
	if err != nil {
		logger.Error(err, "unable to resolve scheduler image")
		return ctrl.Result{}, err
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
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "scx",
							Image: image,
							SecurityContext: &corev1.SecurityContext{
								Privileged: ptr.To(true),
							},
						},
					},
				},
			},
		}

		return nil
	})
	if err != nil {
		logger.Error(err, "unable to create or patch DaemonSet")
		return ctrl.Result{}, err
	}

	if scx.Status.ResolvedImage != image {
		scx.Status.ResolvedImage = image
		if err := r.Status().Update(ctx, &scx); err != nil {
			logger.Error(err, "unable to update Scheduler status")
			return ctrl.Result{}, err
		}
	}

	logger.Info("DaemonSet successfully created or patched", "result", result, "image", image)
	return ctrl.Result{}, nil
}

// resolveImage returns the image reference the DaemonSet must run. When
// signature verification is enabled the image is verified and pinned to the
// digest that was verified, so a tag re-pointed after admission cannot change
// what gets scheduled.
func (r *SchedulerReconciler) resolveImage(ctx context.Context, scx *skedv1.Scheduler) (string, error) {
	image := strings.TrimSpace(scx.Spec.Sched)
	if image == "" {
		return "", fmt.Errorf("spec.sched must not be empty")
	}
	if r.Policy == nil || !r.Policy.Get().SignatureVerificationEnabled() {
		return image, nil
	}
	if r.Verifier == nil {
		return "", fmt.Errorf("signature verification is enabled but no verifier is configured")
	}
	pinned, err := r.Verifier.Verify(ctx, image)
	if err != nil {
		return "", fmt.Errorf("verify scheduler image %q: %w", image, err)
	}
	return pinned, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SchedulerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&skedv1.Scheduler{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("scheduler").
		Complete(r)
}
