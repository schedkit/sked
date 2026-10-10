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
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
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

	schedulerKey := types.NamespacedName{Name: resourceName}
	workloadKey := types.NamespacedName{Name: resourceName, Namespace: "default"}

	resource := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name: resourceName,
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

	_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: schedulerKey})
	require.NoError(t, err)

	var ds appsv1.DaemonSet
	require.NoError(t, k8sClient.Get(ctx, workloadKey, &ds))
	require.Equal(t, pinned, ds.Spec.Template.Spec.Containers[0].Image)
	require.Equal(t, []string{image}, verifier.calls)

	var updated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, schedulerKey, &updated))
	require.Equal(t, pinned, updated.Status.ResolvedImage)
	require.Equal(t, updated.Generation, updated.Status.ObservedGeneration)
}

func TestSchedulerReconcilePopulatesStatus(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const resourceName = "status-resource"
	const image = "ghcr.io/schedkit/scx_rusty:latest"
	const pinned = "ghcr.io/schedkit/scx_rusty@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	schedulerKey := types.NamespacedName{Name: resourceName}
	workloadKey := types.NamespacedName{Name: resourceName, Namespace: "default"}

	resource := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName},
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

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: schedulerKey})
	require.NoError(t, err)

	var updated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, schedulerKey, &updated))
	require.Equal(t, updated.Generation, updated.Status.ObservedGeneration)
	require.Equal(t, pinned, updated.Status.ResolvedImage)

	require.Equal(t, skedv1.SchedulerNodeStatus{}, updated.Status.Nodes)
	ready := conditionFor(t, &updated, skedv1.SchedulerConditionReady)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, skedv1.ReasonNoNodesScheduled, ready.Reason)

	var ds appsv1.DaemonSet
	require.NoError(t, k8sClient.Get(ctx, workloadKey, &ds))
	ds.Status = appsv1.DaemonSetStatus{
		CurrentNumberScheduled: 2,
		DesiredNumberScheduled: 2,
		UpdatedNumberScheduled: 2,
		NumberReady:            2,
		NumberAvailable:        2,
		ObservedGeneration:     ds.Generation,
	}
	require.NoError(t, k8sClient.Status().Update(ctx, &ds))

	_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: schedulerKey})
	require.NoError(t, err)

	require.NoError(t, k8sClient.Get(ctx, schedulerKey, &updated))
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

	namespacedName := types.NamespacedName{Name: resourceName}

	resource := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName},
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

func schedulerFor(name string, spec skedv1.SchedulerSpec) *skedv1.Scheduler {
	return &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       spec,
	}
}

func createNode(t *testing.T, ctx context.Context, k8sClient client.Client, name string, labels map[string]string) {
	t.Helper()

	labels[corev1.LabelHostname] = name
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
	}
	require.NoError(t, k8sClient.Create(ctx, node))
	t.Cleanup(func() { require.NoError(t, client.IgnoreNotFound(k8sClient.Delete(ctx, node))) })
}

func hostnameExclusions(t *testing.T, ds appsv1.DaemonSet) []string {
	t.Helper()

	affinity := ds.Spec.Template.Spec.Affinity
	require.NotNil(t, affinity)
	require.NotNil(t, affinity.NodeAffinity)
	selector := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	require.NotNil(t, selector)
	require.Len(t, selector.NodeSelectorTerms, 1)
	require.Len(t, selector.NodeSelectorTerms[0].MatchExpressions, 1)

	expression := selector.NodeSelectorTerms[0].MatchExpressions[0]
	require.Equal(t, corev1.LabelHostname, expression.Key)
	require.Equal(t, corev1.NodeSelectorOpNotIn, expression.Operator)
	return expression.Values
}

func TestSchedulerReconcileEnforcesOneSchedulerPerNode(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const image = "ghcr.io/schedkit/scx_rusty:latest"
	const pinned = "ghcr.io/schedkit/scx_rusty@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	createNode(t, ctx, k8sClient, "shared-node", map[string]string{"role": "shared"})

	first := schedulerFor("first-scheduler", skedv1.SchedulerSpec{Sched: image, NodeSelector: map[string]string{"role": "shared"}})
	second := schedulerFor("second-scheduler", skedv1.SchedulerSpec{Sched: image, NodeSelector: map[string]string{"role": "shared"}})
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
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
		require.NoError(t, err)
	}

	var firstUpdated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: first.Name}, &firstUpdated))
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, &firstUpdated, skedv1.SchedulerConditionActive).Status)
	require.Equal(t, pinned, firstUpdated.Status.ResolvedImage)

	var firstDS appsv1.DaemonSet
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: first.Name, Namespace: "default"}, &firstDS))
	require.Nil(t, firstDS.Spec.Template.Spec.Affinity)

	var secondUpdated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: second.Name}, &secondUpdated))

	activeCondition := conditionFor(t, &secondUpdated, skedv1.SchedulerConditionActive)
	require.Equal(t, metav1.ConditionFalse, activeCondition.Status)
	require.Equal(t, skedv1.ReasonSchedulerConflict, activeCondition.Reason)
	require.Contains(t, activeCondition.Message, "shared-node")
	require.Contains(t, activeCondition.Message, "first-scheduler")
	require.Equal(t, metav1.ConditionFalse, conditionFor(t, &secondUpdated, skedv1.SchedulerConditionReady).Status)
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, &secondUpdated, skedv1.SchedulerConditionDegraded).Status)
	require.Empty(t, secondUpdated.Status.ResolvedImage)

	var secondDS appsv1.DaemonSet
	err := k8sClient.Get(ctx, types.NamespacedName{Name: second.Name, Namespace: "default"}, &secondDS)
	require.Error(t, err)
}

func TestSchedulerReconcileAllowsDisjointSchedulers(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const image = "ghcr.io/schedkit/scx_rusty:latest"
	const pinned = "ghcr.io/schedkit/scx_rusty@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	createNode(t, ctx, k8sClient, "node-a", map[string]string{"role": "a"})
	createNode(t, ctx, k8sClient, "node-b", map[string]string{"role": "b"})

	first := schedulerFor("first-scheduler", skedv1.SchedulerSpec{Sched: image, NodeSelector: map[string]string{"role": "a"}})
	second := schedulerFor("second-scheduler", skedv1.SchedulerSpec{Sched: image, NodeSelector: map[string]string{"role": "b"}})
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
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
		require.NoError(t, err)
	}

	for _, name := range []string{first.Name, second.Name} {
		var updated skedv1.Scheduler
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: name}, &updated))
		require.Equal(t, metav1.ConditionTrue, conditionFor(t, &updated, skedv1.SchedulerConditionActive).Status)

		var ds appsv1.DaemonSet
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &ds))
	}
}

func TestSchedulerReconcileExcludesContestedNodes(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const image = "ghcr.io/schedkit/scx_rusty:latest"
	const pinned = "ghcr.io/schedkit/scx_rusty@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	createNode(t, ctx, k8sClient, "contested-node", map[string]string{"role": "a", "pool": "x"})
	createNode(t, ctx, k8sClient, "exclusive-node", map[string]string{"pool": "x"})

	first := schedulerFor("first-scheduler", skedv1.SchedulerSpec{Sched: image, NodeSelector: map[string]string{"role": "a"}})
	second := schedulerFor("second-scheduler", skedv1.SchedulerSpec{Sched: image, NodeSelector: map[string]string{"pool": "x"}})
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
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
		require.NoError(t, err)
	}

	var secondUpdated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: second.Name}, &secondUpdated))
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, &secondUpdated, skedv1.SchedulerConditionActive).Status)
	degraded := conditionFor(t, &secondUpdated, skedv1.SchedulerConditionDegraded)
	require.Equal(t, metav1.ConditionTrue, degraded.Status)
	require.Equal(t, skedv1.ReasonSchedulerConflict, degraded.Reason)
	require.Contains(t, degraded.Message, "contested-node")
	require.Contains(t, degraded.Message, "first-scheduler")
	require.Equal(t, pinned, secondUpdated.Status.ResolvedImage)

	var secondDS appsv1.DaemonSet
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: second.Name, Namespace: "default"}, &secondDS))
	require.Equal(t, []string{"contested-node"}, hostnameExclusions(t, secondDS))
}

func TestSchedulerReconcileFailsOverWhenWinningSchedulerIsDeleted(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const image = "ghcr.io/schedkit/scx_rusty:latest"
	const pinned = "ghcr.io/schedkit/scx_rusty@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	createNode(t, ctx, k8sClient, "shared-node", map[string]string{"role": "shared"})

	first := schedulerFor("first-scheduler", skedv1.SchedulerSpec{Sched: image})
	second := schedulerFor("second-scheduler", skedv1.SchedulerSpec{Sched: image})
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
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
		require.NoError(t, err)
	}

	var firstUpdated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: first.Name}, &firstUpdated))
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, &firstUpdated, skedv1.SchedulerConditionActive).Status)

	require.NoError(t, k8sClient.Delete(ctx, first))

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: second.Name}})
	require.NoError(t, err)

	var secondUpdated skedv1.Scheduler
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: second.Name}, &secondUpdated))
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, &secondUpdated, skedv1.SchedulerConditionActive).Status)
	require.Equal(t, pinned, secondUpdated.Status.ResolvedImage)

	var secondDS appsv1.DaemonSet
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: second.Name, Namespace: "default"}, &secondDS))
}

func TestPodSpecAppliesSchedulerConfiguration(t *testing.T) {
	scx := schedulerFor("configured-scheduler", skedv1.SchedulerSpec{
		Sched:            "ghcr.io/schedkit/scx_rusty:latest",
		NodeSelector:     map[string]string{"role": "worker"},
		Tolerations:      []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists}},
		Args:             []string{"--verbose", "--slice=scx"},
		Env:              []corev1.EnvVar{{Name: "RUST_LOG", Value: "info"}},
		ImagePullSecrets: []corev1.LocalObjectReference{{Name: "registry-creds"}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
		},
	})

	spec := podSpec(scx, "ghcr.io/schedkit/scx_rusty@sha256:abc", nil)

	require.Equal(t, map[string]string{"role": "worker"}, spec.NodeSelector)
	require.Equal(t, scx.Spec.Tolerations, spec.Tolerations)
	require.Equal(t, []corev1.LocalObjectReference{{Name: "registry-creds"}}, spec.ImagePullSecrets)
	require.Nil(t, spec.Affinity)

	container := spec.Containers[0]
	require.Equal(t, "ghcr.io/schedkit/scx_rusty@sha256:abc", container.Image)
	require.Equal(t, []string{"--verbose", "--slice=scx"}, container.Args)
	require.Equal(t, []corev1.EnvVar{{Name: "RUST_LOG", Value: "info"}}, container.Env)
	require.Equal(t, resource.MustParse("100m"), container.Resources.Requests[corev1.ResourceCPU])
}

func TestSchedulerSelectsNode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node", Labels: map[string]string{"role": "worker"}},
	}

	t.Run("empty selector matches every node", func(t *testing.T) {
		require.True(t, schedulerSelectsNode(schedulerFor("s", skedv1.SchedulerSpec{}), node))
	})

	t.Run("selector requires matching labels", func(t *testing.T) {
		matching := schedulerFor("s", skedv1.SchedulerSpec{NodeSelector: map[string]string{"role": "worker"}})
		require.True(t, schedulerSelectsNode(matching, node))

		mismatching := schedulerFor("s", skedv1.SchedulerSpec{NodeSelector: map[string]string{"role": "control-plane"}})
		require.False(t, schedulerSelectsNode(mismatching, node))
	})
}

func TestSchedulerReconcileSkipsLoggingMissingScheduler(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	var logs bytes.Buffer
	ctx = logf.IntoContext(ctx, zap.New(zap.WriteTo(&logs), zap.UseDevMode(true)))

	r := &SchedulerReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}

	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "missing-scheduler"},
	})
	require.NoError(t, err)
	require.NotContains(t, logs.String(), "unable to fetch Scheduler")
}

func TestReleaseDaemonSet(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	workloadKey := types.NamespacedName{Name: "released-scheduler", Namespace: "default"}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: workloadKey.Name, Namespace: workloadKey.Namespace},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": workloadKey.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"name": workloadKey.Name}},
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
		Name: workloadKey.Name,
	}}))

	var got appsv1.DaemonSet
	require.Error(t, k8sClient.Get(ctx, workloadKey, &got))
	require.NoError(t, r.releaseDaemonSet(ctx, &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{
		Name: workloadKey.Name,
	}}))
}
