package logging

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/klog/v2"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestInstall(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		maxV int
	}{
		{name: "defaults"},
		{name: "debug", args: []string{"--log-level=debug"}, maxV: 4},
		{name: "verbosity zero", args: []string{"--log-verbosity=0"}},
		{name: "verbosity two", args: []string{"--log-verbosity=2"}, maxV: 2},
		{name: "errors only", args: []string{"--log-level=error"}, maxV: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestInstallProcess$", "--"}, tc.args...)...)
			cmd.Env = append(os.Environ(), "SKED_LOGGING_TEST=1")
			var output bytes.Buffer
			cmd.Stdout = io.Discard
			cmd.Stderr = &output
			err := cmd.Run()
			require.NoError(t, err, "%s", output.String())

			entries := records(t, output.String())
			byMessage := make(map[string]map[string]any, len(entries))
			for _, entry := range entries {
				msg := entry["msg"].(string)
				require.NotContains(t, byMessage, msg, "duplicate logging output")
				byMessage[msg] = entry
			}
			for _, name := range []string{"controller", "slog", "klog", "klog-context"} {
				require.Equal(t, "ERROR", byMessage[name+" error"]["level"])
				require.Equal(t, "test failure", byMessage[name+" error"]["err"])
				for v := 0; v <= 5; v++ {
					msg := fmt.Sprintf("%s v%d", name, v)
					if v <= tc.maxV {
						require.Contains(t, byMessage, msg)
						require.Equal(t, slog.Level(-v).String(), byMessage[msg]["level"])
					} else {
						require.NotContains(t, byMessage, msg)
					}
				}
			}
			if tc.maxV >= 0 {
				require.Equal(t, "setup", byMessage["setup"]["logger"])
				require.Equal(t, "INFO", byMessage["klog formatted"]["level"])
			} else {
				require.NotContains(t, byMessage, "setup")
				require.NotContains(t, byMessage, "klog formatted")
			}
		})
	}
}

func TestInstallProcess(t *testing.T) {
	if os.Getenv("SKED_LOGGING_TEST") != "1" {
		t.Skip("subprocess test")
	}
	setup := logf.Log.WithName("setup")
	require.NoError(t, options(t, flag.Args()...).Install(os.Stderr))
	setup.Info("setup")
	for v := 0; v <= 5; v++ {
		logf.Log.V(v).Info(fmt.Sprintf("controller v%d", v))
		slog.Log(t.Context(), slog.Level(-v), fmt.Sprintf("slog v%d", v))
		klog.V(klog.Level(v)).InfoS(fmt.Sprintf("klog v%d", v))
		klog.Background().V(v).Info(fmt.Sprintf("klog-context v%d", v))
	}
	err := errors.New("test failure")
	logf.Log.Error(err, "controller error")
	slog.Error("slog error", "err", err)
	klog.ErrorS(err, "klog error")
	klog.Background().Error(err, "klog-context error")
	klog.Infof("klog %s", "formatted")
}

func TestInstallRejectsInvalidOptions(t *testing.T) {
	for _, args := range [][]string{
		{"--log-format=invalid"},
		{"--log-level=info", "--log-verbosity=0"},
	} {
		var output bytes.Buffer
		require.Error(t, options(t, args...).Install(&output))
		require.Empty(t, output.String())
	}
}
