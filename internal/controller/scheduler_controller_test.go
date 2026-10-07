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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
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
}
