package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func loggingOutput(t *testing.T, args ...string) string {
	t.Helper()
	fs := flag.NewFlagSet("logging-test", flag.ContinueOnError)
	opts := bindLoggingFlags(fs)
	require.NoError(t, fs.Parse(args))
	var output bytes.Buffer
	opts.DestWriter = &output
	logger := zap.New(zap.UseFlagOptions(opts)).WithName("logging-test").WithValues("scheduler", "example")
	logger.V(1).Info("debug")
	logger.V(2).Info("verbose")
	logger.V(3).Info("trace")
	logger.Info("info", "nodes", 2)
	logger.Error(errors.New("test failure"), "error")
	return output.String()
}

func TestJSONLogging(t *testing.T) {
	for _, tc := range []struct {
		name         string
		args         []string
		messages     []string
		withoutStack bool
	}{
		{name: "production defaults", messages: []string{"info", "error"}},
		{name: "deployment defaults", args: []string{"--zap-devel=false"}, messages: []string{"info", "error"}},
		{name: "debug", args: []string{"--zap-log-level=debug"}, messages: []string{"debug", "info", "error"}},
		{name: "verbosity two", args: []string{"--zap-log-level=2"}, messages: []string{"debug", "verbose", "info", "error"}},
		{name: "errors only", args: []string{"--zap-log-level=error"}, messages: []string{"error"}},
		{
			name: "development overrides", messages: []string{"info", "error"},
			args: []string{"--zap-devel=true", "--zap-encoder=json", "--zap-log-level=info"},
		},
		{
			name: "stacktrace override", messages: []string{"info", "error"},
			args: []string{"--zap-stacktrace-level=panic"}, withoutStack: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := loggingOutput(t, tc.args...)
			lines := strings.Split(strings.TrimSpace(output), "\n")
			require.Len(t, lines, len(tc.messages))
			for i, line := range lines {
				var entry map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &entry))
				require.Equal(t, tc.messages[i], entry["msg"])
				require.Equal(t, "logging-test", entry["logger"])
				require.Equal(t, "example", entry["scheduler"])
				timestamp, ok := entry["ts"].(string)
				require.True(t, ok)
				_, err := time.Parse(time.RFC3339, timestamp)
				require.NoError(t, err)

				switch tc.messages[i] {
				case "info":
					require.Equal(t, "info", entry["level"])
					require.Equal(t, float64(2), entry["nodes"])
				case "debug":
					require.Equal(t, "debug", entry["level"])
				case "error":
					require.Equal(t, "error", entry["level"])
					require.Equal(t, "test failure", entry["error"])
				}
				if tc.messages[i] == "error" && !tc.withoutStack {
					require.NotEmpty(t, entry["stacktrace"])
				} else {
					require.NotContains(t, entry, "stacktrace")
				}
			}
		})
	}
}

func TestConsoleLogging(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		debug bool
	}{
		{name: "console with production levels", args: []string{"--zap-encoder=console"}},
		{name: "development defaults", args: []string{"--zap-devel=true"}, debug: true},
		{name: "deployment override", args: []string{"--zap-devel=false", "--zap-devel=true"}, debug: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := loggingOutput(t, tc.args...)
			require.False(t, json.Valid([]byte(strings.Split(output, "\n")[0])))
			require.Contains(t, output, "\tINFO\tlogging-test\tinfo\t")
			require.Contains(t, output, "\tERROR\tlogging-test\terror\t")
			if tc.debug {
				require.Contains(t, output, "\tDEBUG\tlogging-test\tdebug\t")
			} else {
				require.NotContains(t, output, "\tDEBUG\t")
			}
		})
	}
}

func TestLogTimeEncoding(t *testing.T) {
	output := loggingOutput(t, "--zap-time-encoding=epoch")
	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.Split(output, "\n")[0]), &entry))
	require.IsType(t, float64(0), entry["ts"])
}

func TestInvalidLoggingFlags(t *testing.T) {
	for _, arg := range []string{
		"--zap-devel=invalid",
		"--zap-encoder=yaml",
		"--zap-log-level=verbose",
		"--zap-log-level=-1",
		"--zap-log-level=0",
		"--zap-stacktrace-level=debug",
		"--zap-time-encoding=invalid",
	} {
		t.Run(arg, func(t *testing.T) {
			fs := flag.NewFlagSet("logging-test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			bindLoggingFlags(fs)
			require.Error(t, fs.Parse([]string{arg}))
		})
	}
}
