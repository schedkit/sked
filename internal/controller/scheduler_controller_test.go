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
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	skedv1 "github.com/schedkit/sked/api/v1"
	"github.com/schedkit/sked/internal/trust"
)

type fakePolicy struct{ policy *trust.Policy }

func (f fakePolicy) Get() *trust.Policy { return f.policy }

type fakeVerifier struct {
	resolved string
	err      error
	calls    []string
}

func (f *fakeVerifier) Verify(_ context.Context, imageRef string) (string, error) {
	f.calls = append(f.calls, imageRef)
	if f.err != nil {
		return "", f.err
	}
	return f.resolved, nil
}

func TestResolveImage(t *testing.T) {
	const image = "ghcr.io/schedkit/scx_rusty:latest"
	const pinned = "ghcr.io/schedkit/scx_rusty@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	t.Run("pins to the verified digest when verification is enabled", func(t *testing.T) {
		verifier := &fakeVerifier{resolved: pinned}
		r := &SchedulerReconciler{Policy: fakePolicy{trust.DefaultPolicy()}, Verifier: verifier}

		resolved, err := r.resolveImage(context.Background(), &skedv1.Scheduler{Spec: skedv1.SchedulerSpec{Sched: image}})
		require.NoError(t, err)
		require.Equal(t, pinned, resolved)
		require.Equal(t, []string{image}, verifier.calls)
	})

	t.Run("keeps the image as-is when verification is disabled", func(t *testing.T) {
		policy := trust.DefaultPolicy()
		policy.VerifySignatures = ptr.To(false)
		verifier := &fakeVerifier{resolved: pinned}
		r := &SchedulerReconciler{Policy: fakePolicy{policy}, Verifier: verifier}

		resolved, err := r.resolveImage(context.Background(), &skedv1.Scheduler{Spec: skedv1.SchedulerSpec{Sched: image}})
		require.NoError(t, err)
		require.Equal(t, image, resolved)
		require.Empty(t, verifier.calls)
	})

	t.Run("fails when verification fails", func(t *testing.T) {
		verifier := &fakeVerifier{err: errors.New("no trusted signature")}
		r := &SchedulerReconciler{Policy: fakePolicy{trust.DefaultPolicy()}, Verifier: verifier}

		_, err := r.resolveImage(context.Background(), &skedv1.Scheduler{Spec: skedv1.SchedulerSpec{Sched: image}})
		require.ErrorContains(t, err, "verify scheduler image")
	})

	t.Run("rejects an empty image", func(t *testing.T) {
		r := &SchedulerReconciler{Policy: fakePolicy{trust.DefaultPolicy()}, Verifier: &fakeVerifier{}}

		_, err := r.resolveImage(context.Background(), &skedv1.Scheduler{Spec: skedv1.SchedulerSpec{Sched: "  "}})
		require.ErrorContains(t, err, "must not be empty")
	})

	t.Run("requires a verifier when verification is enabled", func(t *testing.T) {
		r := &SchedulerReconciler{Policy: fakePolicy{trust.DefaultPolicy()}}

		_, err := r.resolveImage(context.Background(), &skedv1.Scheduler{Spec: skedv1.SchedulerSpec{Sched: image}})
		require.ErrorContains(t, err, "no verifier is configured")
	})
}

func TestSchedulerReconcilePinsImage(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const resourceName = "test-resource"
	const image = "ghcr.io/schedkit/scx_rusty:latest"
	const pinned = "ghcr.io/schedkit/scx_rusty@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	namespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

	resource := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: "default",
		},
		Spec: skedv1.SchedulerSpec{
			Sched: image,
		},
	}
	require.NoError(t, k8sClient.Create(ctx, resource))
	t.Cleanup(func() {
		require.NoError(t, k8sClient.Delete(ctx, resource))
	})

	verifier := &fakeVerifier{resolved: pinned}
	controllerReconciler := &SchedulerReconciler{
		Client:   k8sClient,
		Scheme:   k8sClient.Scheme(),
		Policy:   fakePolicy{trust.DefaultPolicy()},
		Verifier: verifier,
	}

	_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
	require.NoError(t, err)

	var ds appsv1.DaemonSet
	require.NoError(t, k8sClient.Get(ctx, namespacedName, &ds))
	require.Equal(t, pinned, ds.Spec.Template.Spec.Containers[0].Image)
	require.Equal(t, []string{image}, verifier.calls)

	var updated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, namespacedName, &updated))
	require.Equal(t, pinned, updated.Status.ResolvedImage)
	require.Equal(t, updated.Generation, updated.Status.ObservedGeneration)
}

func TestSchedulerReconcilePopulatesStatus(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const resourceName = "status-resource"
	const image = "ghcr.io/schedkit/scx_rusty:latest"
	const pinned = "ghcr.io/schedkit/scx_rusty@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	namespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

	resource := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"},
		Spec:       skedv1.SchedulerSpec{Sched: image},
	}
	require.NoError(t, k8sClient.Create(ctx, resource))
	t.Cleanup(func() { require.NoError(t, k8sClient.Delete(ctx, resource)) })

	r := &SchedulerReconciler{
		Client:   k8sClient,
		Scheme:   k8sClient.Scheme(),
		Policy:   fakePolicy{trust.DefaultPolicy()},
		Verifier: &fakeVerifier{resolved: pinned},
	}

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
	require.NoError(t, err)

	var updated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, namespacedName, &updated))
	require.Equal(t, updated.Generation, updated.Status.ObservedGeneration)
	require.Equal(t, pinned, updated.Status.ResolvedImage)

	require.Equal(t, skedv1.SchedulerNodeStatus{}, updated.Status.Nodes)
	ready := conditionFor(t, &updated, skedv1.SchedulerConditionReady)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, skedv1.ReasonNoNodesScheduled, ready.Reason)

	var ds appsv1.DaemonSet
	require.NoError(t, k8sClient.Get(ctx, namespacedName, &ds))
	ds.Status = appsv1.DaemonSetStatus{
		CurrentNumberScheduled: 2,
		DesiredNumberScheduled: 2,
		UpdatedNumberScheduled: 2,
		NumberReady:            2,
		NumberAvailable:        2,
		ObservedGeneration:     ds.Generation,
	}
	require.NoError(t, k8sClient.Status().Update(ctx, &ds))

	_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
	require.NoError(t, err)

	require.NoError(t, k8sClient.Get(ctx, namespacedName, &updated))
	require.Equal(t, skedv1.SchedulerNodeStatus{Desired: 2, Ready: 2, Available: 2}, updated.Status.Nodes)
	ready = conditionFor(t, &updated, skedv1.SchedulerConditionReady)
	require.Equal(t, metav1.ConditionTrue, ready.Status)
	require.Equal(t, skedv1.ReasonDaemonSetReady, ready.Reason)
	require.Equal(t, metav1.ConditionFalse, conditionFor(t, &updated, skedv1.SchedulerConditionDegraded).Status)
}

func TestSchedulerReconcileReportsVerificationFailure(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const resourceName = "unverified-resource"
	const image = "ghcr.io/schedkit/scx_rusty:latest"

	namespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

	resource := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"},
		Spec:       skedv1.SchedulerSpec{Sched: image},
	}
	require.NoError(t, k8sClient.Create(ctx, resource))
	t.Cleanup(func() { require.NoError(t, k8sClient.Delete(ctx, resource)) })

	r := &SchedulerReconciler{
		Client:   k8sClient,
		Scheme:   k8sClient.Scheme(),
		Policy:   fakePolicy{trust.DefaultPolicy()},
		Verifier: &fakeVerifier{err: errors.New("no trusted signature")},
	}

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
	require.ErrorContains(t, err, "verify scheduler image")

	var updated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, namespacedName, &updated))
	require.Equal(t, updated.Generation, updated.Status.ObservedGeneration)

	degraded := conditionFor(t, &updated, skedv1.SchedulerConditionDegraded)
	require.Equal(t, metav1.ConditionTrue, degraded.Status)
	require.Equal(t, skedv1.ReasonImageVerificationFailed, degraded.Reason)
	require.Equal(t, metav1.ConditionFalse, conditionFor(t, &updated, skedv1.SchedulerConditionReady).Status)

	var ds appsv1.DaemonSet
	err = k8sClient.Get(ctx, namespacedName, &ds)
	require.Error(t, err)
}

func TestSchedulerReconcileEnforcesSingleActiveScheduler(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const image = "ghcr.io/schedkit/scx_rusty:latest"
	const pinned = "ghcr.io/schedkit/scx_rusty@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	active := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{Name: "active-scheduler", Namespace: "default"},
		Spec:       skedv1.SchedulerSpec{Sched: image},
	}
	conflict := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{Name: "conflict-scheduler", Namespace: "default"},
		Spec:       skedv1.SchedulerSpec{Sched: image},
	}
	require.NoError(t, k8sClient.Create(ctx, active))
	require.NoError(t, k8sClient.Create(ctx, conflict))
	t.Cleanup(func() {
		require.NoError(t, client.IgnoreNotFound(k8sClient.Delete(ctx, active)))
		require.NoError(t, client.IgnoreNotFound(k8sClient.Delete(ctx, conflict)))
	})

	r := &SchedulerReconciler{
		Client:   k8sClient,
		Scheme:   k8sClient.Scheme(),
		Policy:   fakePolicy{trust.DefaultPolicy()},
		Verifier: &fakeVerifier{resolved: pinned},
	}

	for _, name := range []string{active.Name, conflict.Name} {
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "default"}})
		require.NoError(t, err)
	}

	var activeUpdated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: active.Name, Namespace: "default"}, &activeUpdated))
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, &activeUpdated, skedv1.SchedulerConditionActive).Status)
	require.Equal(t, pinned, activeUpdated.Status.ResolvedImage)

	var activeDS appsv1.DaemonSet
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: active.Name, Namespace: "default"}, &activeDS))

	var conflictUpdated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: conflict.Name, Namespace: "default"}, &conflictUpdated))

	activeCondition := conditionFor(t, &conflictUpdated, skedv1.SchedulerConditionActive)
	require.Equal(t, metav1.ConditionFalse, activeCondition.Status)
	require.Equal(t, skedv1.ReasonSchedulerConflict, activeCondition.Reason)
	require.Contains(t, activeCondition.Message, "default/active-scheduler")
	require.Equal(t, metav1.ConditionFalse, conditionFor(t, &conflictUpdated, skedv1.SchedulerConditionReady).Status)
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, &conflictUpdated, skedv1.SchedulerConditionDegraded).Status)
	require.Empty(t, conflictUpdated.Status.ResolvedImage)

	var conflictDS appsv1.DaemonSet
	err := k8sClient.Get(ctx, types.NamespacedName{Name: conflict.Name, Namespace: "default"}, &conflictDS)
	require.Error(t, err)
}

func TestSchedulerReconcileFailsOverWhenActiveSchedulerIsDeleted(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const image = "ghcr.io/schedkit/scx_rusty:latest"
	const pinned = "ghcr.io/schedkit/scx_rusty@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	first := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{Name: "first-scheduler", Namespace: "default"},
		Spec:       skedv1.SchedulerSpec{Sched: image},
	}
	second := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{Name: "second-scheduler", Namespace: "default"},
		Spec:       skedv1.SchedulerSpec{Sched: image},
	}
	require.NoError(t, k8sClient.Create(ctx, first))
	require.NoError(t, k8sClient.Create(ctx, second))
	t.Cleanup(func() {
		require.NoError(t, client.IgnoreNotFound(k8sClient.Delete(ctx, first)))
		require.NoError(t, client.IgnoreNotFound(k8sClient.Delete(ctx, second)))
	})

	r := &SchedulerReconciler{
		Client:   k8sClient,
		Scheme:   k8sClient.Scheme(),
		Policy:   fakePolicy{trust.DefaultPolicy()},
		Verifier: &fakeVerifier{resolved: pinned},
	}

	for _, name := range []string{first.Name, second.Name} {
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "default"}})
		require.NoError(t, err)
	}

	var firstUpdated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: first.Name, Namespace: "default"}, &firstUpdated))
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, &firstUpdated, skedv1.SchedulerConditionActive).Status)

	require.NoError(t, k8sClient.Delete(ctx, first))

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: second.Name, Namespace: "default"}})
	require.NoError(t, err)

	var secondUpdated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: second.Name, Namespace: "default"}, &secondUpdated))
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, &secondUpdated, skedv1.SchedulerConditionActive).Status)
	require.Equal(t, pinned, secondUpdated.Status.ResolvedImage)

	var secondDS appsv1.DaemonSet
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: second.Name, Namespace: "default"}, &secondDS))
}

func TestReleaseDaemonSet(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	namespacedName := types.NamespacedName{Name: "released-scheduler", Namespace: "default"}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: namespacedName.Name, Namespace: namespacedName.Namespace},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": namespacedName.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"name": namespacedName.Name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "scx", Image: "example.com/scx:latest"}},
				},
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, ds))
	t.Cleanup(func() { require.NoError(t, client.IgnoreNotFound(k8sClient.Delete(ctx, ds))) })

	r := &SchedulerReconciler{Client: k8sClient}

	require.NoError(t, r.releaseDaemonSet(ctx, &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{
		Name: namespacedName.Name, Namespace: namespacedName.Namespace,
	}}))

	var got appsv1.DaemonSet
	require.Error(t, k8sClient.Get(ctx, namespacedName, &got))
	require.NoError(t, r.releaseDaemonSet(ctx, &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{
		Name: namespacedName.Name, Namespace: namespacedName.Namespace,
	}}))
}
