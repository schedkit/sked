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
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	skedv1 "github.com/schedkit/sked/api/v1"
)

func setSchedulerCondition(
	scx *skedv1.Scheduler,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	meta.SetStatusCondition(&scx.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: scx.Generation,
	})
}

func markSchedulerActive(scx *skedv1.Scheduler) {
	setSchedulerCondition(scx, skedv1.SchedulerConditionActive, metav1.ConditionTrue,
		skedv1.ReasonActiveScheduler, "this Scheduler is the active scheduler")
}

func markSchedulerConflict(scx *skedv1.Scheduler, conflicts []nodeConflict) {
	scx.Status.ResolvedImage = ""
	scx.Status.Nodes = skedv1.SchedulerNodeStatus{}

	message := conflictMessage(conflicts)
	setSchedulerCondition(scx, skedv1.SchedulerConditionActive, metav1.ConditionFalse,
		skedv1.ReasonSchedulerConflict, message)
	setSchedulerCondition(scx, skedv1.SchedulerConditionReady, metav1.ConditionFalse,
		skedv1.ReasonSchedulerConflict, message)
	setSchedulerCondition(scx, skedv1.SchedulerConditionProgressing, metav1.ConditionFalse,
		skedv1.ReasonSchedulerConflict, message)
	setSchedulerCondition(scx, skedv1.SchedulerConditionDegraded, metav1.ConditionTrue,
		skedv1.ReasonSchedulerConflict, message)
}

func markSchedulerNodeConflict(scx *skedv1.Scheduler, conflicts []nodeConflict) {
	setSchedulerCondition(scx, skedv1.SchedulerConditionDegraded, metav1.ConditionTrue,
		skedv1.ReasonSchedulerConflict, conflictMessage(conflicts))
}

func conflictMessage(conflicts []nodeConflict) string {
	nodes := make([]string, 0, len(conflicts))
	winners := map[string]struct{}{}
	for i := range conflicts {
		nodes = append(nodes, conflicts[i].node)
		winners[conflicts[i].winner.String()] = struct{}{}
	}
	sort.Strings(nodes)

	owners := make([]string, 0, len(winners))
	for winner := range winners {
		owners = append(owners, winner)
	}
	sort.Strings(owners)

	return fmt.Sprintf(
		"nodes %s are already served by scheduler(s) %s; a node runs only one scheduler at a time",
		strings.Join(nodes, ", "), strings.Join(owners, ", "),
	)
}

func markReconcileFailure(scx *skedv1.Scheduler, reason string, err error) {
	setSchedulerCondition(scx, skedv1.SchedulerConditionReady, metav1.ConditionFalse,
		reason, err.Error())
	setSchedulerCondition(scx, skedv1.SchedulerConditionProgressing, metav1.ConditionFalse,
		reason, "reconcile failed; the controller will retry")
	setSchedulerCondition(scx, skedv1.SchedulerConditionDegraded, metav1.ConditionTrue,
		reason, err.Error())
}

func applyDaemonSetStatus(scx *skedv1.Scheduler, ds *appsv1.DaemonSet) {
	desired := ds.Status.DesiredNumberScheduled
	updated := ds.Status.UpdatedNumberScheduled
	ready := ds.Status.NumberReady
	available := ds.Status.NumberAvailable

	scx.Status.Nodes = skedv1.SchedulerNodeStatus{
		Desired:     desired,
		Ready:       ready,
		Available:   available,
		Unavailable: ds.Status.NumberUnavailable,
	}

	daemonSetCurrent := ds.Status.ObservedGeneration >= ds.Generation
	rolloutComplete := daemonSetCurrent && updated >= desired && available >= desired

	switch {
	case desired == 0:
		setSchedulerCondition(scx, skedv1.SchedulerConditionReady, metav1.ConditionFalse,
			skedv1.ReasonNoNodesScheduled, "no nodes currently match the scheduler's node selection")
		setSchedulerCondition(scx, skedv1.SchedulerConditionProgressing, metav1.ConditionFalse,
			skedv1.ReasonReconcileSucceeded, "nothing to roll out")
		setSchedulerCondition(scx, skedv1.SchedulerConditionDegraded, metav1.ConditionFalse,
			skedv1.ReasonReconcileSucceeded, "")
	case rolloutComplete:
		setSchedulerCondition(scx, skedv1.SchedulerConditionReady, metav1.ConditionTrue,
			skedv1.ReasonDaemonSetReady,
			fmt.Sprintf("%d/%d nodes have a ready scheduler pod", ready, desired))
		setSchedulerCondition(scx, skedv1.SchedulerConditionProgressing, metav1.ConditionFalse,
			skedv1.ReasonReconcileSucceeded, "the scheduler is available on all targeted nodes")
		setSchedulerCondition(scx, skedv1.SchedulerConditionDegraded, metav1.ConditionFalse,
			skedv1.ReasonReconcileSucceeded, "")
	default:
		setSchedulerCondition(scx, skedv1.SchedulerConditionReady, metav1.ConditionFalse,
			skedv1.ReasonRolloutInProgress,
			fmt.Sprintf("%d/%d nodes have a ready scheduler pod", ready, desired))
		setSchedulerCondition(scx, skedv1.SchedulerConditionProgressing, metav1.ConditionTrue,
			skedv1.ReasonRolloutInProgress,
			fmt.Sprintf("%d/%d nodes are updated", updated, desired))
		if daemonSetCurrent && updated >= desired {
			setSchedulerCondition(scx, skedv1.SchedulerConditionDegraded, metav1.ConditionTrue,
				skedv1.ReasonNodesUnavailable,
				fmt.Sprintf("%d/%d nodes are unavailable", ds.Status.NumberUnavailable, desired))
		} else {
			setSchedulerCondition(scx, skedv1.SchedulerConditionDegraded, metav1.ConditionFalse,
				skedv1.ReasonRolloutInProgress, "rollout in progress")
		}
	}
}
