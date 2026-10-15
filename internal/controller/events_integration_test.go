package controller

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	skedv1 "github.com/schedkit/sked/api/v1"
)

func TestSchedulerEventsStored(t *testing.T) {
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, testEnv.Stop()) })

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, skedv1.AddToScheme(scheme))
	logs, err := os.CreateTemp(t.TempDir(), "controller-logs")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logs.Close()) })
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Logger:                 logr.FromSlogHandler(slog.NewJSONHandler(logs, nil)),
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             config.Controller{SkipNameValidation: ptr.To(true)},
	})
	require.NoError(t, err)
	r := &SchedulerReconciler{Client: mgr.GetClient(), Scheme: scheme}
	require.NoError(t, r.SetupWithManager(mgr))
	require.NotNil(t, r.Recorder)

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	stopped := make(chan error, 1)
	go func() { stopped <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-stopped)
	})
	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	sc := schedulerFor("event-resource", skedv1.SchedulerSpec{Sched: "example.com/scheduler:v1"})
	require.NoError(t, k8sClient.Create(ctx, sc))

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		var recorded eventsv1.EventList
		if !assert.NoError(t, k8sClient.List(ctx, &recorded, client.InNamespace(metav1.NamespaceDefault))) {
			return
		}
		if !assert.Len(t, recorded.Items, 1) {
			return
		}
		event := recorded.Items[0]
		assert.Equal(t, corev1.EventTypeNormal, event.Type)
		assert.Equal(t, skedv1.ReasonNoNodesScheduled, event.Reason)
		assert.Equal(t, "Reconcile", event.Action)
		assert.Equal(t, "sked.schedkit.io/scheduler-controller", event.ReportingController)
		assert.Equal(t, sc.UID, event.Regarding.UID)
		assert.Equal(t, sc.Name, event.Regarding.Name)
		assert.Equal(t, "Scheduler", event.Regarding.Kind)
		assert.Empty(t, event.Regarding.Namespace)
		assert.Equal(t, "no nodes currently match the scheduler's node selection", event.Note)
	}, 20*time.Second, 100*time.Millisecond)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		output, err := os.ReadFile(logs.Name())
		if !assert.NoError(t, err) {
			return
		}
		for line := range strings.SplitSeq(string(output), "\n") {
			var entry map[string]any
			if json.Unmarshal([]byte(line), &entry) != nil || entry["msg"] != "DaemonSet successfully created or patched" {
				continue
			}
			assert.Equal(t, "INFO", entry["level"])
			assert.Equal(t, "scheduler", entry["controller"])
			assert.Equal(t, "sked.schedkit.io", entry["controllerGroup"])
			assert.Equal(t, "Scheduler", entry["controllerKind"])
			assert.Equal(t, map[string]any{"name": sc.Name}, entry["Scheduler"])
			assert.Equal(t, sc.Name, entry["name"])
			assert.NotEmpty(t, entry["reconcileID"])
			return
		}
		assert.Fail(t, "no successful reconcile log found")
	}, 10*time.Second, 100*time.Millisecond)

	var legacy corev1.EventList
	require.NoError(t, k8sClient.List(ctx, &legacy, client.InNamespace(metav1.NamespaceDefault)))
	require.Len(t, legacy.Items, 1)
	require.Equal(t, sc.UID, legacy.Items[0].InvolvedObject.UID)
}
