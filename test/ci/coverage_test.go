package ci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCoverageCheck(t *testing.T) {
	script, err := filepath.Abs("../../hack/check-coverage.sh")
	require.NoError(t, err)
	const fortyPercent = "mode: atomic\nFILE:4.2,4.20 2 1\nFILE:5.2,5.20 3 0\n"

	cases := []struct {
		name      string
		threshold string
		profile   string
		missing   bool
		failure   bool
		report    string
	}{
		{
			name: "above threshold", threshold: "40",
			profile: "mode: atomic\nFILE:4.2,4.20 3 1\nFILE:5.2,5.20 2 0\n",
			report:  "| 60.00% | 40% |",
		},
		{
			name: "at threshold", threshold: "40",
			profile: fortyPercent,
			report:  "| 40.00% | 40% |",
		},
		{
			name: "below threshold", threshold: "40", failure: true,
			profile: "mode: atomic\nFILE:4.2,4.20 1 100\nFILE:5.2,5.20 4 0\n",
			report:  "| 20.00% | 40% |",
		},
		{
			name: "rounding cannot pass", threshold: "40", failure: true,
			profile: "mode: atomic\nFILE:4.2,4.20 39999 1\nFILE:5.2,5.20 60001 0\n",
			report:  "FAIL: Coverage is below the minimum.",
		},
		{
			name: "decimal threshold", threshold: "39.5",
			profile: fortyPercent,
			report:  "| 40.00% | 39.5% |",
		},
		{
			name: "fully covered", threshold: "100",
			profile: "mode: atomic\nFILE:4.2,4.20 2 1\nFILE:5.2,5.20 3 1\n",
			report:  "| 100.00% | 100% |",
		},
		{
			name: "zero threshold", threshold: "0",
			profile: "mode: atomic\nFILE:4.2,4.20 2 0\n",
			report:  "| 0.00% | 0% |",
		},
		{
			name: "no statements", threshold: "0", failure: true,
			profile: "mode: atomic\n",
			report:  "FAIL: Coverage profile contains no statements.",
		},
		{name: "missing profile", threshold: "40", missing: true, failure: true},
		{name: "empty profile", threshold: "40", failure: true},
		{name: "malformed profile", threshold: "40", profile: "mode: atomic\nbroken\n", failure: true},
		{name: "invalid threshold", threshold: "invalid", profile: fortyPercent, failure: true},
		{name: "negative threshold", threshold: "-1", profile: fortyPercent, failure: true},
		{name: "threshold over 100", threshold: "100.1", profile: fortyPercent, failure: true},
		{name: "empty threshold", threshold: "", profile: fortyPercent, failure: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source := filepath.Join(dir, "fixture.go")
			require.NoError(t, os.WriteFile(source,
				[]byte("package fixture\n\nfunc run() {\n println(1)\n println(2)\n}\n"), 0o600))
			profile := filepath.Join(dir, "cover.out")
			if !tc.missing {
				require.NoError(t, os.WriteFile(profile, []byte(strings.ReplaceAll(tc.profile, "FILE", source)), 0o600))
			}
			summary := filepath.Join(dir, "summary.md")
			require.NoError(t, os.WriteFile(summary, []byte("Existing summary\n"), 0o600))

			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", script, profile, tc.threshold)
			cmd.Env = append(os.Environ(), "GITHUB_STEP_SUMMARY="+summary)
			out, err := cmd.CombinedOutput()
			require.NoError(t, ctx.Err(), "coverage check exceeded its time budget")
			if tc.failure {
				require.Error(t, err, "%s", out)
			} else {
				require.NoError(t, err, "%s", out)
				require.Contains(t, string(out), "PASS: Coverage meets the minimum.")
			}

			report, err := os.ReadFile(summary)
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(string(report), "Existing summary\n"))
			if tc.report != "" {
				require.Contains(t, string(out), tc.report)
				require.Contains(t, string(report), tc.report)
			}
		})
	}
}
