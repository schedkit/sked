package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	skedv1 "github.com/schedkit/sked/api/v1"
	"github.com/schedkit/sked/internal/trust"
)

func TestSchedulerStatusEvents(t *testing.T) {
	newScheduler := func() *skedv1.Scheduler {
		return &skedv1.Scheduler{ObjectMeta: metav1.ObjectMeta{Name: "scheduler", Generation: 1}}
	}
	ready := newScheduler()
	applyDaemonSetStatus(ready, &appsv1.DaemonSet{Status: appsv1.DaemonSetStatus{
		DesiredNumberScheduled: 2, UpdatedNumberScheduled: 2, NumberReady: 2, NumberAvailable: 2,
	}})
	progressing := newScheduler()
	applyDaemonSetStatus(progressing, &appsv1.DaemonSet{Status: appsv1.DaemonSetStatus{
		DesiredNumberScheduled: 2, UpdatedNumberScheduled: 1, NumberReady: 1, NumberAvailable: 1,
	}})
	unavailable := newScheduler()
	applyDaemonSetStatus(unavailable, &appsv1.DaemonSet{Status: appsv1.DaemonSetStatus{
		DesiredNumberScheduled: 2, UpdatedNumberScheduled: 2, NumberReady: 1, NumberAvailable: 1, NumberUnavailable: 1,
	}})
	noNodes := newScheduler()
	applyDaemonSetStatus(noNodes, &appsv1.DaemonSet{})
	conflicts := []nodeConflict{{node: "node-a", winner: types.NamespacedName{Name: "other"}}}
	blocked := newScheduler()
	markSchedulerConflict(blocked, conflicts)
	partialConflict := ready.DeepCopy()
	markSchedulerNodeConflict(partialConflict, conflicts)
	verificationFailed := newScheduler()
	markReconcileFailure(verificationFailed, skedv1.ReasonImageVerificationFailed, errors.New("verification failed at 50%"))
	invalid := newScheduler()
	markReconcileFailure(invalid, skedv1.ReasonSpecInvalid, errEmptySched)
	reconcileFailed := newScheduler()
	markReconcileFailure(reconcileFailed, skedv1.ReasonReconcileFailed, errors.New("list nodes: forbidden"))

	cases := []struct {
		name    string
		before  *skedv1.Scheduler
		after   *skedv1.Scheduler
		message string
	}{
		{"no nodes", newScheduler(), noNodes, "Normal NoNodesScheduled no nodes currently match the scheduler's node selection"},
		{"rollout", noNodes, progressing, "Normal RolloutInProgress 1/2 nodes have a ready scheduler pod"},
		{"ready", progressing, ready, "Normal DaemonSetReady 2/2 nodes have a ready scheduler pod"},
		{"unavailable", ready, unavailable, "Warning NodesUnavailable 1/2 nodes are unavailable"},
		{"verification", newScheduler(), verificationFailed, "Warning ImageVerificationFailed verification failed at 50%"},
		{"invalid spec", newScheduler(), invalid, "Warning SpecInvalid spec.sched must not be empty"},
		{"reconcile failed", newScheduler(), reconcileFailed, "Warning ReconcileFailed list nodes: forbidden"},
		{"conflict", newScheduler(), blocked, "Warning SchedulerConflict " + conflictMessage(conflicts)},
		{"partial conflict", ready, partialConflict, "Warning SchedulerConflict " + conflictMessage(conflicts)},
		{"conflict resolved", partialConflict, ready, "Normal DaemonSetReady 2/2 nodes have a ready scheduler pod"},
		{"recovered", verificationFailed, ready, "Normal DaemonSetReady 2/2 nodes have a ready scheduler pod"},
		{"failure reason changed", unavailable, verificationFailed, "Warning ImageVerificationFailed verification failed at 50%"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := events.NewFakeRecorder(10)
			r := &SchedulerReconciler{Recorder: recorder}
			r.recordStatusEvent(tc.before, tc.after)
			require.Equal(t, []string{tc.message}, drainEvents(recorder))
			r.recordStatusEvent(tc.after.DeepCopy(), tc.after)
			require.Empty(t, drainEvents(recorder))
		})
	}

	t.Run("counter and metadata changes are silent", func(t *testing.T) {
		updated := progressing.DeepCopy()
		updated.Generation++
		updated.Status.Nodes.Ready++
		for i := range updated.Status.Conditions {
			updated.Status.Conditions[i].Message = "new counters"
			updated.Status.Conditions[i].ObservedGeneration++
			updated.Status.Conditions[i].LastTransitionTime = metav1.Now()
		}
		recorder := events.NewFakeRecorder(10)
		r := &SchedulerReconciler{Recorder: recorder}
		r.recordStatusEvent(progressing, updated)
		r.recordStatusEvent(newScheduler(), newScheduler())
		require.Empty(t, drainEvents(recorder))
	})

	t.Run("recorder is optional in tests", func(t *testing.T) {
		r := &SchedulerReconciler{}
		require.NotPanics(t, func() { r.recordStatusEvent(newScheduler(), ready) })
	})
}

func drainEvents(recorder *events.FakeRecorder) []string {
	var result []string
	for {
		select {
		case event := <-recorder.Events:
			result = append(result, event)
		default:
			return result
		}
	}
}

func TestSchedulerReconcileFailureEvents(t *testing.T) {
	failure := errors.New("API unavailable")
	resource := schema.GroupResource{Group: skedv1.GroupVersion.Group, Resource: "schedulers"}
	conflict := apierrors.NewConflict(resource, "scheduler", errors.New("stale resource version"))
	deleted := apierrors.NewNotFound(resource, "scheduler")
	cases := []struct {
		name     string
		funcs    interceptor.Funcs
		verifier trust.Verifier
		objects  []client.Object
		err      error
		reasons  []string
	}{
		{
			name: "node list",
			funcs: interceptor.Funcs{List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return failure
			}},
			reasons: []string{"Warning ReconcileFailed list nodes: API unavailable"},
		},
		{
			name: "daemonset create",
			funcs: interceptor.Funcs{Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				return failure
			}},
			reasons: []string{"Warning ReconcileFailed API unavailable"},
		},
		{
			name: "daemonset patch",
			objects: []client.Object{&appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Name: "scheduler", Namespace: "default"},
			}},
			funcs: interceptor.Funcs{Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
				return failure
			}},
			reasons: []string{"Warning ReconcileFailed API unavailable"},
		},
		{
			name: "conflicting daemonset deletion",
			objects: []client.Object{
				&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node"}},
				schedulerFor("earlier", skedv1.SchedulerSpec{Sched: "example.com/scheduler:v1"}),
			},
			funcs: interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				return failure
			}},
			reasons: []string{"Warning ReconcileFailed delete conflicting DaemonSet default/scheduler: API unavailable"},
		},
		{
			name: "status update",
			funcs: interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
				return failure
			}},
			reasons: []string{"Warning StatusUpdateFailed update scheduler status: API unavailable"},
		},
		{
			name: "status conflict",
			funcs: interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
				return conflict
			}},
			err: conflict,
		},
		{
			name: "deleted during status update",
			funcs: interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
				return deleted
			}},
			err: deleted,
		},
		{
			name: "verification and status update",
			funcs: interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
				return failure
			}},
			verifier: &fakeVerifier{err: errors.New("untrusted image")},
			reasons: []string{
				`Warning ImageVerificationFailed verify scheduler image "example.com/scheduler:v1": untrusted image`,
				"Warning StatusUpdateFailed update scheduler status: API unavailable",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, skedv1.AddToScheme(scheme))
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, appsv1.AddToScheme(scheme))
			sc := schedulerFor("scheduler", skedv1.SchedulerSpec{Sched: "example.com/scheduler:v1"})
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sc).WithObjects(tc.objects...).
				WithStatusSubresource(sc).WithInterceptorFuncs(tc.funcs).Build()
			recorder := events.NewFakeRecorder(10)
			r := &SchedulerReconciler{Client: k8sClient, Scheme: scheme, Recorder: recorder}
			if tc.verifier != nil {
				r.Policy = fakePolicy{trust.DefaultPolicy()}
				r.Verifier = tc.verifier
			}
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: sc.Name}}
			_, err := r.Reconcile(t.Context(), req)
			expectedErr := failure
			if tc.err != nil {
				expectedErr = tc.err
			}
			require.ErrorIs(t, err, expectedErr)
			if tc.verifier != nil {
				require.ErrorContains(t, err, "untrusted image")
			}
			require.Equal(t, tc.reasons, drainEvents(recorder))
			if tc.funcs.SubResourceUpdate == nil {
				_, err = r.Reconcile(t.Context(), req)
				require.Error(t, err)
				require.Empty(t, drainEvents(recorder))
			}
		})
	}
}

func TestSchedulerEventReference(t *testing.T) {
	sc := schedulerFor("scheduler", skedv1.SchedulerSpec{})
	sc.UID = "scheduler-uid"
	recorder := &referenceRecorder{}
	r := &SchedulerReconciler{Recorder: recorder}
	r.recordEvent(sc, corev1.EventTypeWarning, "StatusUpdateFailed", "failed at 50%")
	require.Same(t, sc, recorder.regarding)
	require.Nil(t, recorder.related)
	require.Equal(t, "Reconcile", recorder.action)
	require.Equal(t, "failed at 50%", recorder.note)
}

func TestSchedulerEventNoteLength(t *testing.T) {
	for _, message := range []string{
		strings.Repeat("x", maxEventNoteBytes),
		strings.Repeat("x", maxEventNoteBytes+1),
		strings.Repeat("界", maxEventNoteBytes),
	} {
		recorder := &referenceRecorder{}
		r := &SchedulerReconciler{Recorder: recorder}
		r.recordEvent(&skedv1.Scheduler{}, corev1.EventTypeWarning, "ReconcileFailed", message)
		require.LessOrEqual(t, len(recorder.note), maxEventNoteBytes)
		require.True(t, utf8.ValidString(recorder.note))
		if len(message) > maxEventNoteBytes {
			require.True(t, strings.HasSuffix(recorder.note, "..."))
			require.True(t, strings.HasPrefix(message, strings.TrimSuffix(recorder.note, "...")))
		} else {
			require.Equal(t, message, recorder.note)
		}
	}
}

type referenceRecorder struct {
	regarding runtime.Object
	related   runtime.Object
	action    string
	note      string
}

func (r *referenceRecorder) Eventf(regarding, related runtime.Object, _, _, action, note string, args ...interface{}) {
	r.regarding = regarding
	r.related = related
	r.action = action
	r.note = fmt.Sprintf(note, args...)
}
