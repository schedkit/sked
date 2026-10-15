package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	skedv1 "github.com/schedkit/sked/api/v1"
)

func TestReconcileLogging(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelInfo, slog.LevelDebug} {
		t.Run(level.String(), func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, clientgoscheme.AddToScheme(scheme))
			require.NoError(t, skedv1.AddToScheme(scheme))
			sc := schedulerFor("logging-test", skedv1.SchedulerSpec{Sched: "example.com/scheduler:v1"})
			client := fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&skedv1.Scheduler{}).WithObjects(sc).Build()
			r := &SchedulerReconciler{Client: client, Scheme: scheme}
			var output bytes.Buffer
			logger := logr.FromSlogHandler(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: level}))
			ctx := logf.IntoContext(context.Background(), logger.WithValues("reconcileID", "test-reconcile"))
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: sc.Name}}

			check := func(result, wantLevel string) {
				t.Helper()
				output.Reset()
				_, err := r.Reconcile(ctx, req)
				require.NoError(t, err)
				if wantLevel == "" {
					require.Empty(t, output.String())
					return
				}
				var entry map[string]any
				require.NoError(t, json.Unmarshal(output.Bytes(), &entry))
				require.Equal(t, "DaemonSet successfully created or patched", entry["msg"])
				require.Equal(t, result, entry["result"])
				require.Equal(t, wantLevel, entry["level"])
				require.Equal(t, "test-reconcile", entry["reconcileID"])
			}
			check("created", "INFO")
			if level == slog.LevelDebug {
				check("unchanged", "DEBUG+3")
			} else {
				check("unchanged", "")
			}
			require.NoError(t, client.Get(ctx, req.NamespacedName, sc))
			sc.Spec.Sched = "example.com/scheduler:v2"
			require.NoError(t, client.Update(ctx, sc))
			check("updated", "INFO")
		})
	}
}
