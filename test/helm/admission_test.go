package helm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdmissionCheck(t *testing.T) {
	denial := `Error from server (Forbidden): admission webhook "vscheduler-v1.kb.io" denied the request: ` +
		`image "registry.example/untrusted:latest" is not in the trusted image allowlist`
	transient := `Error from server (InternalError): failed calling webhook "vscheduler-v1.kb.io": `
	type response struct {
		message string
		status  int
	}
	type testCase struct {
		name      string
		responses []response
		timeout   int
		failure   string
	}
	cases := make([]testCase, 0, 15)
	cases = append(cases, []testCase{
		{"denied", []response{{denial, 1}}, 10, ""},
		{"accepted", []response{{"scheduler.sked.schedkit.io/rejected-image created (server dry run)", 0}},
			10, "The webhook accepted an untrusted image"},
		{"wrong denial", []response{{`admission webhook "vscheduler-v1.kb.io" denied the request: invalid spec`, 1}},
			10, "Unexpected admission error"},
		{"RBAC failure", []response{{"Error from server (Forbidden): cannot create resource schedulers", 1}},
			10, "Unexpected admission error"},
		{"bad certificate", []response{{transient + "x509: certificate signed by unknown authority", 1}},
			10, "Unexpected webhook error"},
		{"wrong webhook", []response{{`failed calling webhook "other.example": context deadline exceeded`, 1}},
			10, "Unexpected admission error"},
		{"retry then accepted", []response{{transient + "context deadline exceeded", 1}, {"accepted", 0}},
			10, "The webhook accepted an untrusted image"},
		{"deadline", []response{{transient + "context deadline exceeded", 1}},
			1, "Timed out waiting for the webhook"},
	}...)
	for _, message := range []string{
		"context deadline exceeded", "i/o timeout", "Client.Timeout exceeded while awaiting headers",
		"connection refused", "connection reset by peer", `no endpoints available for service "sked-webhook-service"`,
		"unexpected EOF",
	} {
		cases = append(cases, testCase{message, []response{{transient + message, 1}, {denial, 1}}, 10, ""})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			stub := `#!/usr/bin/env bash
set -euo pipefail
count=0
if [[ -f "$ADMISSION_FIXTURES/attempts" ]]; then
  read -r count < "$ADMISSION_FIXTURES/attempts"
fi
count=$((count + 1))
printf '%s\n' "$count" > "$ADMISSION_FIXTURES/attempts"
printf '%s\n' "$*" >> "$ADMISSION_FIXTURES/arguments"
fixture="$ADMISSION_FIXTURES/response-$count"
if [[ ! -f "$fixture" ]]; then
  fixture="$ADMISSION_FIXTURES/response-last"
fi
printf '%s\n' "$(< "$fixture")"
exit "$(< "$fixture.status")"
`
			require.NoError(t, os.WriteFile(filepath.Join(dir, "kubectl"), []byte(stub), 0o755))
			for i, reply := range tc.responses {
				names := []string{fmt.Sprintf("response-%d", i+1)}
				if i == len(tc.responses)-1 {
					names = append(names, "response-last")
				}
				for _, name := range names {
					path := filepath.Join(dir, name)
					require.NoError(t, os.WriteFile(path, []byte(reply.message), 0o600))
					require.NoError(t, os.WriteFile(path+".status", []byte(strconv.Itoa(reply.status)), 0o600))
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "hack/check-helm-admission.sh", "rejected.yaml", strconv.Itoa(tc.timeout))
			cmd.Dir = root(t)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "ADMISSION_FIXTURES="+dir)
			out, err := cmd.CombinedOutput()
			require.NoError(t, ctx.Err(), "admission check exceeded its time budget")
			if tc.failure == "" {
				require.NoError(t, err, "%s", out)
			} else {
				require.Error(t, err, "%s", out)
				require.Contains(t, string(out), tc.failure)
			}
			for _, reply := range tc.responses {
				require.Contains(t, string(out), reply.message, "admission errors must be visible in CI logs")
			}
			attempts, err := os.ReadFile(filepath.Join(dir, "attempts"))
			require.NoError(t, err)
			require.Equal(t, strconv.Itoa(len(tc.responses)), strings.TrimSpace(string(attempts)))
			arguments, err := os.ReadFile(filepath.Join(dir, "arguments"))
			require.NoError(t, err)
			for _, line := range strings.Split(strings.TrimSpace(string(arguments)), "\n") {
				require.Contains(t, line, "--dry-run=server")
				require.Contains(t, line, "-f rejected.yaml")
				for _, arg := range strings.Fields(line) {
					if value, ok := strings.CutPrefix(arg, "--request-timeout="); ok {
						duration, err := time.ParseDuration(value)
						require.NoError(t, err)
						require.Positive(t, duration)
						require.LessOrEqual(t, duration, time.Duration(tc.timeout)*time.Second)
					}
				}
			}
		})
	}
}
