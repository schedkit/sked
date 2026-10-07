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
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	skedv1 "github.com/schedkit/sked/api/v1"
)

func TestSchedulerReconcile(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	const resourceName = "test-resource"
	namespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

	resource := &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: "default",
		},
		Spec: skedv1.SchedulerSpec{
			Sched: "ghcr.io/schedkit/scx_rusty:latest",
		},
	}
	require.NoError(t, k8sClient.Create(ctx, resource))
	t.Cleanup(func() {
		require.NoError(t, k8sClient.Delete(ctx, resource))
	})

	controllerReconciler := &SchedulerReconciler{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
	}

	_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
	require.NoError(t, err)
}
