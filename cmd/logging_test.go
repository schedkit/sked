package main

import (
	"flag"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestControllerLoggingCLI(t *testing.T) {
	if os.Getenv("SKED_MAIN_LOGGING_TEST") == "1" {
		os.Args = append([]string{"manager"}, flag.Args()...)
		flag.CommandLine = flag.NewFlagSet("manager", flag.ExitOnError)
		main()
		return
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
		code int
	}{
		{name: "help", args: []string{"--help"}, want: "-log-format"},
		{name: "format", args: []string{"--log-format=yaml"}, want: "invalid log format", code: 2},
		{name: "level", args: []string{"--log-level=trace"}, want: "invalid log level", code: 2},
		{name: "verbosity", args: []string{"--log-verbosity=-1"}, want: "invalid log verbosity", code: 2},
		{
			name: "conflicting flags", args: []string{"--log-level=info", "--log-verbosity=0"},
			want: "mutually exclusive", code: 2,
		},
		{name: "removed flag", args: []string{"--zap-devel=false"}, want: "flag provided but not defined", code: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestControllerLoggingCLI$", "--"}, tc.args...)...)
			cmd.Env = append(os.Environ(), "SKED_MAIN_LOGGING_TEST=1")
			output, err := cmd.CombinedOutput()
			if tc.code == 0 {
				require.NoError(t, err, "%s", output)
				require.Contains(t, string(output), "-log-level")
				require.Contains(t, string(output), "-log-verbosity")
				require.NotContains(t, string(output), "-zap-")
			} else {
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit, "%s", output)
				require.Equal(t, tc.code, exit.ExitCode())
			}
			require.Contains(t, string(output), tc.want)
		})
	}
}
