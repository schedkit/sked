package controller

import (
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	skedv1 "github.com/schedkit/sked/api/v1"
)

const maxEventNoteBytes = 1024

func eventCondition(scx *skedv1.Scheduler) *metav1.Condition {
	degraded := meta.FindStatusCondition(scx.Status.Conditions, skedv1.SchedulerConditionDegraded)
	if degraded != nil && degraded.Status == metav1.ConditionTrue {
		return degraded
	}
	return meta.FindStatusCondition(scx.Status.Conditions, skedv1.SchedulerConditionReady)
}

func (r *SchedulerReconciler) recordStatusEvent(base, scx *skedv1.Scheduler) {
	current := eventCondition(scx)
	if current == nil {
		return
	}
	previous := eventCondition(base)
	if previous != nil && previous.Type == current.Type && previous.Status == current.Status && previous.Reason == current.Reason {
		return
	}
	eventType := corev1.EventTypeNormal
	if current.Type == skedv1.SchedulerConditionDegraded {
		eventType = corev1.EventTypeWarning
	}
	r.recordEvent(scx, eventType, current.Reason, current.Message)
}

func (r *SchedulerReconciler) recordEvent(scx *skedv1.Scheduler, eventType, reason, message string) {
	if r.Recorder == nil {
		return
	}
	if len(message) > maxEventNoteBytes {
		message = message[:maxEventNoteBytes-len("...")]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
		message += "..."
	}
	r.Recorder.Eventf(scx, nil, eventType, reason, "Reconcile", "%s", message)
}
