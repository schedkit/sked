package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func options(t *testing.T, args ...string) *Options {
	t.Helper()
	fs := flag.NewFlagSet("logging-test", flag.ContinueOnError)
	opts := BindFlags(fs)
	require.NoError(t, fs.Parse(args))
	return opts
}

func records(t *testing.T, output string) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), line)
		entries = append(entries, entry)
	}
	return entries
}

func TestJSONLogging(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		maxV int
	}{
		{name: "defaults"},
		{name: "explicit info", args: []string{"--log-level=info"}},
		{name: "explicit json", args: []string{"--log-format=json"}},
		{name: "verbosity zero", args: []string{"--log-verbosity=0"}},
		{name: "verbosity one", args: []string{"--log-verbosity=1"}, maxV: 1},
		{name: "verbosity two", args: []string{"--log-verbosity=2"}, maxV: 2},
		{name: "verbosity four", args: []string{"--log-verbosity=4"}, maxV: 4},
		{name: "debug", args: []string{"--log-level=debug"}, maxV: 4},
		{name: "errors only", args: []string{"--log-level=error"}, maxV: -1},
		{name: "last level wins", args: []string{"--log-level=debug", "--log-level=info"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			handler, err := options(t, tc.args...).NewHandler(&output)
			require.NoError(t, err)
			logger := logr.FromSlogHandler(handler).WithName("logging").WithName("test").WithValues("scheduler", "example")
			for v := 0; v <= 5; v++ {
				logger.V(v).Info("info", "v", v, "nodes", 2)
			}
			logger.Error(errors.New("test failure"), "error")

			entries := records(t, output.String())
			require.Len(t, entries, tc.maxV+2)
			for i, entry := range entries {
				require.Equal(t, "logging/test", entry["logger"])
				require.Equal(t, "example", entry["scheduler"])
				timestamp, ok := entry["time"].(string)
				require.True(t, ok)
				_, err := time.Parse(time.RFC3339Nano, timestamp)
				require.NoError(t, err)
				require.NotContains(t, entry, "ts")
				require.NotContains(t, entry, "stacktrace")
				require.NotContains(t, entry, "source")
				if i <= tc.maxV {
					require.Equal(t, "info", entry["msg"])
					require.Equal(t, slog.Level(-i).String(), entry["level"])
					require.Equal(t, float64(i), entry["v"])
					require.Equal(t, float64(2), entry["nodes"])
				} else {
					require.Equal(t, "error", entry["msg"])
					require.Equal(t, "ERROR", entry["level"])
					require.Equal(t, "test failure", entry["err"])
					require.NotContains(t, entry, "error")
				}
			}
		})
	}
}

func TestTextLogging(t *testing.T) {
	for _, level := range []string{"info", "debug", "error"} {
		t.Run(level, func(t *testing.T) {
			var output bytes.Buffer
			handler, err := options(t, "--log-format=text", "--log-level="+level).NewHandler(&output)
			require.NoError(t, err)
			logger := logr.FromSlogHandler(handler).WithName("logging").WithValues("scheduler", "example")
			logger.Info("info")
			logger.V(1).Info("verbose")
			logger.Error(errors.New("test failure"), "error")
			text := output.String()
			require.False(t, json.Valid([]byte(strings.Split(text, "\n")[0])))
			require.Contains(t, text, "level=ERROR msg=error")
			require.Contains(t, text, "scheduler=example logger=logging")
			require.Contains(t, text, `err="test failure"`)
			if level != "error" {
				require.Contains(t, text, "level=INFO msg=info")
			} else {
				require.NotContains(t, text, "msg=info")
			}
			if level == "debug" {
				require.Contains(t, text, "level=DEBUG+3 msg=verbose")
			} else {
				require.NotContains(t, text, "msg=verbose")
			}
		})
	}
}

func TestInvalidOptions(t *testing.T) {
	for _, args := range [][]string{
		{"--log-format=yaml"},
		{"--log-format="},
		{"--log-level=warn"},
		{"--log-level=2"},
		{"--log-level="},
		{"--log-verbosity=-1"},
		{"--log-verbosity=1.5"},
		{"--log-verbosity=verbose"},
		{"--log-verbosity=2147483648"},
		{"--log-verbosity=999999999999999999999999999999"},
		{"--log-verbosity="},
		{"--log-level=info", "--log-verbosity=0"},
		{"--log-verbosity=2", "--log-level=debug"},
		{"--zap-devel=false"},
		{"--zap-encoder=json"},
		{"--zap-log-level=info"},
		{"--zap-stacktrace-level=error"},
		{"--zap-time-encoding=epoch"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fs := flag.NewFlagSet("logging-test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			opts := BindFlags(fs)
			err := fs.Parse(args)
			if err == nil {
				_, err = opts.NewHandler(io.Discard)
			}
			require.Error(t, err)
		})
	}
}

func TestMaximumVerbosity(t *testing.T) {
	handler, err := options(t, "--log-verbosity=2147483647").NewHandler(io.Discard)
	require.NoError(t, err)
	logger := logr.FromSlogHandler(handler)
	require.True(t, logger.V(2147483647).Enabled())
}

func TestContextAndObjectReferences(t *testing.T) {
	var output bytes.Buffer
	handler, err := options(t).NewHandler(&output)
	require.NoError(t, err)
	logger := logr.FromSlogHandler(handler).WithValues("controller", "scheduler", "controllerKind", "Scheduler")
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "operators"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "scx", Env: []corev1.EnvVar{{Name: "TOKEN", Value: "sensitive-value"}},
		}}},
	}
	for _, id := range []string{"first", "second"} {
		ctx := logf.IntoContext(context.Background(), logger.WithValues("reconcileID", id, "Scheduler", klog.KRef("", id)))
		logf.FromContext(ctx).Info("reconciled", "pod", klog.KObj(pod), "counts", map[string]int{"ready": 2})
	}
	entries := records(t, output.String())
	require.Len(t, entries, 2)
	for i, id := range []string{"first", "second"} {
		entry := entries[i]
		require.Equal(t, "scheduler", entry["controller"])
		require.Equal(t, "Scheduler", entry["controllerKind"])
		require.Equal(t, id, entry["reconcileID"])
		require.Equal(t, map[string]any{"name": id}, entry["Scheduler"])
		require.Equal(t, map[string]any{"name": "example", "namespace": "operators"}, entry["pod"])
		require.Equal(t, map[string]any{"ready": float64(2)}, entry["counts"])
	}
	require.NotContains(t, output.String(), "sensitive-value")
	require.NotContains(t, output.String(), "TOKEN")
}

func TestConcurrentLoggingWithoutSampling(t *testing.T) {
	var output bytes.Buffer
	handler, err := options(t).NewHandler(&output)
	require.NoError(t, err)
	logger := logr.FromSlogHandler(handler)
	const workers, iterations = 10, 150
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			log := logger.WithName("worker").WithValues("worker", worker)
			for i := range iterations {
				log.Info("repeated\nmessage", "iteration", i)
			}
		})
	}
	wg.Wait()
	entries := records(t, output.String())
	require.Len(t, entries, workers*iterations)
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		require.Equal(t, "worker", entry["logger"])
		require.Equal(t, "repeated\nmessage", entry["msg"])
		key := fmt.Sprintf("%v/%v", entry["worker"], entry["iteration"])
		require.False(t, seen[key])
		seen[key] = true
	}
}
