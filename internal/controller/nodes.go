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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	skedv1 "github.com/schedkit/sked/api/v1"
)

type nodeConflict struct {
	node     string
	hostname string
	winner   types.NamespacedName
}

func schedulerSelectsNode(scx *skedv1.Scheduler, node *corev1.Node) bool {
	for key, value := range scx.Spec.NodeSelector {
		if node.Labels[key] != value {
			return false
		}
	}
	return true
}

func winningScheduler(schedulers []skedv1.Scheduler, node *corev1.Node) *skedv1.Scheduler {
	var winner *skedv1.Scheduler
	for i := range schedulers {
		candidate := &schedulers[i]
		if !candidate.DeletionTimestamp.IsZero() || !schedulerSelectsNode(candidate, node) {
			continue
		}
		if winner == nil || schedulerPrecedes(candidate, winner) {
			winner = candidate
		}
	}
	return winner
}
