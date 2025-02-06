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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	skedv1 "github.com/schedkit/sked/api/v1"
)

// SchedExtReconciler reconciles a SchedExt object
type SchedExtReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sked.schedkit.io,resources=schedexts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sked.schedkit.io,resources=schedexts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sked.schedkit.io,resources=schedexts/finalizers,verbs=update

func (r *SchedExtReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var scx skedv1.SchedExt
	if err := r.Get(ctx, req.NamespacedName, &scx); err != nil {
		logger.Error(err, "unable to fetch SchedExt")
		return ctrl.Result{}, client.IgnoreNotFound(err)
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
							Image: scx.Spec.Sched,
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

	logger.Info("DaemonSet successfully created or patched", "result", result)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SchedExtReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&skedv1.SchedExt{}).
		Named("schedext").
		Complete(r)
}
