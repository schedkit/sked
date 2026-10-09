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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	skedv1 "github.com/schedkit/sked/api/v1"
)

func conditionFor(t *testing.T, scx *skedv1.Scheduler, conditionType string) metav1.Condition {
	t.Helper()

	condition := meta.FindStatusCondition(scx.Status.Conditions, conditionType)
	require.NotNil(t, condition, "condition %q is missing", conditionType)
	return *condition
}

func TestApplyDaemonSetStatus(t *testing.T) {
	daemonSet := func(generation, observed int64, status appsv1.DaemonSetStatus) *appsv1.DaemonSet {
		status.ObservedGeneration = observed
		return &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Generation: generation},
			Status:     status,
		}
	}

	t.Run("reports every node ready once the rollout completed", func(t *testing.T) {
		scx := &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{Generation: 3}}
		ds := daemonSet(2, 2, appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 4,
			UpdatedNumberScheduled: 4,
			NumberReady:            4,
			NumberAvailable:        4,
		})

		applyDaemonSetStatus(scx, ds)

		require.Equal(t, skedv1.SchedulerNodeStatus{Desired: 4, Ready: 4, Available: 4}, scx.Status.Nodes)

		ready := conditionFor(t, scx, skedv1.SchedulerConditionReady)
		require.Equal(t, metav1.ConditionTrue, ready.Status)
		require.Equal(t, skedv1.ReasonDaemonSetReady, ready.Reason)
		require.Equal(t, int64(3), ready.ObservedGeneration)

		require.Equal(t, metav1.ConditionFalse, conditionFor(t, scx, skedv1.SchedulerConditionProgressing).Status)
		require.Equal(t, metav1.ConditionFalse, conditionFor(t, scx, skedv1.SchedulerConditionDegraded).Status)
	})

	t.Run("reports a rollout that has not reached every node", func(t *testing.T) {
		scx := &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{Generation: 1}}
		ds := daemonSet(1, 1, appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 4,
			UpdatedNumberScheduled: 2,
			NumberReady:            3,
			NumberAvailable:        3,
			NumberUnavailable:      1,
		})

		applyDaemonSetStatus(scx, ds)

		require.Equal(t, skedv1.SchedulerNodeStatus{Desired: 4, Ready: 3, Available: 3, Unavailable: 1}, scx.Status.Nodes)
		require.Equal(t, metav1.ConditionFalse, conditionFor(t, scx, skedv1.SchedulerConditionReady).Status)
		require.Equal(t, skedv1.ReasonRolloutInProgress, conditionFor(t, scx, skedv1.SchedulerConditionProgressing).Reason)
		require.Equal(t, metav1.ConditionTrue, conditionFor(t, scx, skedv1.SchedulerConditionProgressing).Status)
		require.Equal(t, metav1.ConditionFalse, conditionFor(t, scx, skedv1.SchedulerConditionDegraded).Status)
	})

	t.Run("degrades when updated nodes fail to become available", func(t *testing.T) {
		scx := &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{Generation: 1}}
		ds := daemonSet(1, 1, appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 3,
			UpdatedNumberScheduled: 3,
			NumberReady:            1,
			NumberAvailable:        1,
			NumberUnavailable:      2,
		})

		applyDaemonSetStatus(scx, ds)

		degraded := conditionFor(t, scx, skedv1.SchedulerConditionDegraded)
		require.Equal(t, metav1.ConditionTrue, degraded.Status)
		require.Equal(t, skedv1.ReasonNodesUnavailable, degraded.Reason)
		require.Equal(t, metav1.ConditionFalse, conditionFor(t, scx, skedv1.SchedulerConditionReady).Status)
	})

	t.Run("does not claim readiness when no node matches", func(t *testing.T) {
		scx := &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{Generation: 1}}

		applyDaemonSetStatus(scx, daemonSet(1, 1, appsv1.DaemonSetStatus{}))

		require.Equal(t, skedv1.SchedulerNodeStatus{}, scx.Status.Nodes)
		ready := conditionFor(t, scx, skedv1.SchedulerConditionReady)
		require.Equal(t, metav1.ConditionFalse, ready.Status)
		require.Equal(t, skedv1.ReasonNoNodesScheduled, ready.Reason)
		require.Equal(t, metav1.ConditionFalse, conditionFor(t, scx, skedv1.SchedulerConditionDegraded).Status)
	})

	t.Run("waits for the DaemonSet to observe its own generation", func(t *testing.T) {
		scx := &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{Generation: 1}}
		ds := daemonSet(2, 1, appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 2,
			UpdatedNumberScheduled: 2,
			NumberReady:            1,
			NumberAvailable:        1,
			NumberUnavailable:      1,
		})

		applyDaemonSetStatus(scx, ds)

		require.Equal(t, metav1.ConditionTrue, conditionFor(t, scx, skedv1.SchedulerConditionProgressing).Status)
		require.Equal(t, metav1.ConditionFalse, conditionFor(t, scx, skedv1.SchedulerConditionDegraded).Status)
	})
}

func TestMarkReconcileFailure(t *testing.T) {
	scx := &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{Generation: 7}}
	scx.Status.Nodes = skedv1.SchedulerNodeStatus{Desired: 2, Ready: 2, Available: 2}
	scx.Status.ResolvedImage = "example.com/sched@sha256:abc"

	err := errors.New("no trusted signature")
	markReconcileFailure(scx, skedv1.ReasonImageVerificationFailed, err)

	require.Equal(t, skedv1.SchedulerNodeStatus{Desired: 2, Ready: 2, Available: 2}, scx.Status.Nodes)
	require.Equal(t, "example.com/sched@sha256:abc", scx.Status.ResolvedImage)

	ready := conditionFor(t, scx, skedv1.SchedulerConditionReady)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, skedv1.ReasonImageVerificationFailed, ready.Reason)
	require.Equal(t, int64(7), ready.ObservedGeneration)

	require.Equal(t, metav1.ConditionFalse, conditionFor(t, scx, skedv1.SchedulerConditionProgressing).Status)
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, scx, skedv1.SchedulerConditionDegraded).Status)
}

func TestMarkSchedulerActive(t *testing.T) {
	scx := &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{Generation: 2}}

	markSchedulerActive(scx)

	active := conditionFor(t, scx, skedv1.SchedulerConditionActive)
	require.Equal(t, metav1.ConditionTrue, active.Status)
	require.Equal(t, skedv1.ReasonActiveScheduler, active.Reason)
	require.Equal(t, int64(2), active.ObservedGeneration)
}

func TestMarkSchedulerConflict(t *testing.T) {
	scx := &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{Generation: 4}}
	scx.Status.ResolvedImage = "example.com/sched@sha256:abc"
	scx.Status.Nodes = skedv1.SchedulerNodeStatus{Desired: 1, Ready: 1, Available: 1}
	active := &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{Name: "active", Namespace: "default"}}

	markSchedulerConflict(scx, active)

	activeCondition := conditionFor(t, scx, skedv1.SchedulerConditionActive)
	require.Equal(t, metav1.ConditionFalse, activeCondition.Status)
	require.Equal(t, skedv1.ReasonSchedulerConflict, activeCondition.Reason)
	require.Contains(t, activeCondition.Message, "default/active")
	require.Equal(t, int64(4), activeCondition.ObservedGeneration)
	require.Equal(t, metav1.ConditionFalse, conditionFor(t, scx, skedv1.SchedulerConditionReady).Status)
	require.Equal(t, metav1.ConditionFalse, conditionFor(t, scx, skedv1.SchedulerConditionProgressing).Status)
	require.Equal(t, metav1.ConditionTrue, conditionFor(t, scx, skedv1.SchedulerConditionDegraded).Status)
	require.Equal(t, skedv1.SchedulerNodeStatus{}, scx.Status.Nodes)
	require.Empty(t, scx.Status.ResolvedImage)
}

func TestResolveFailureReason(t *testing.T) {
	require.Equal(t, skedv1.ReasonSpecInvalid, resolveFailureReason(errEmptySched))
	require.Equal(t, skedv1.ReasonReconcileFailed, resolveFailureReason(errMissingVerifier))
	require.Equal(t, skedv1.ReasonImageVerificationFailed, resolveFailureReason(errors.New("no trusted signature")))
}
