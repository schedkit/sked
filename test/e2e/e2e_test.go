/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schedkit/sked/test/utils"
)

// namespace where the project is deployed in
const namespace = "sked-system"

// serviceAccountName created for the project
const serviceAccountName = "sked-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "sked-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "sked-metrics-binding"

const (
	eventuallyTimeout = 2 * time.Minute
	eventuallyTick    = time.Second
)

func TestE2E(t *testing.T) {
	t.Cleanup(func() { teardownSuite(t) })
	setupSuite(t)

	var controllerPodName string

	t.Run("should run successfully", func(t *testing.T) {
		t.Cleanup(func() { dumpDiagnostics(t, controllerPodName) })

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			cmd := exec.Command("kubectl", "get",
				"pods", "-l", "control-plane=controller-manager",
				"-o", "go-template={{ range .items }}"+
					"{{ if not .metadata.deletionTimestamp }}"+
					"{{ .metadata.name }}"+
					"{{ \"\\n\" }}{{ end }}{{ end }}",
				"-n", namespace,
			)
			podOutput, err := utils.Run(cmd)
			if !assert.NoError(c, err, "Failed to retrieve controller-manager pod information") {
				return
			}
			podNames := utils.GetNonEmptyLines(podOutput)
			if !assert.Len(c, podNames, 1, "expected 1 controller pod running") {
				return
			}
			controllerPodName = podNames[0]
			assert.Contains(c, controllerPodName, "controller-manager")

			cmd = exec.Command("kubectl", "get",
				"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
				"-n", namespace,
			)
			output, err := utils.Run(cmd)
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, "Running", output, "Incorrect controller-manager pod status")
		}, eventuallyTimeout, eventuallyTick)
	})

	t.Run("should ensure the metrics endpoint is serving metrics", func(t *testing.T) {
		t.Cleanup(func() { dumpDiagnostics(t, controllerPodName) })

		cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
			"--clusterrole=sked-metrics-reader",
			fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
		)
		_, err := utils.Run(cmd)
		require.NoError(t, err, "Failed to create ClusterRoleBinding")

		cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
		_, err = utils.Run(cmd)
		require.NoError(t, err, "Metrics service should exist")

		cmd = exec.Command("kubectl", "get", "ServiceMonitor", "-n", namespace)
		_, err = utils.Run(cmd)
		require.NoError(t, err, "ServiceMonitor should exist")

		token := serviceAccountToken(t)
		require.NotEmpty(t, token)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
			output, err := utils.Run(cmd)
			if !assert.NoError(c, err) {
				return
			}
			assert.Contains(c, output, "8443", "Metrics endpoint is not ready")
		}, eventuallyTimeout, eventuallyTick)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			output, err := utils.Run(cmd)
			if !assert.NoError(c, err) {
				return
			}
			assert.Contains(c, output, "controller-runtime.metrics\tServing metrics server",
				"Metrics server not yet started")
		}, eventuallyTimeout, eventuallyTick)

		cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
			"--namespace", namespace,
			"--image=curlimages/curl:7.78.0",
			"--", "/bin/sh", "-c", fmt.Sprintf(
				"curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics",
				token, metricsServiceName, namespace))
		_, err = utils.Run(cmd)
		require.NoError(t, err, "Failed to create curl-metrics pod")

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
				"-o", "jsonpath={.status.phase}",
				"-n", namespace)
			output, err := utils.Run(cmd)
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, "Succeeded", output, "curl pod in wrong status")
		}, 5*time.Minute, eventuallyTick)

		metricsOutput := getMetricsOutput(t)
		require.Contains(t, metricsOutput, "controller_runtime_reconcile_total")
	})
}

func dumpDiagnostics(t *testing.T, controllerPodName string) {
	t.Helper()

	if !t.Failed() {
		return
	}

	t.Log("Fetching controller manager pod logs")
	cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
	if controllerLogs, err := utils.Run(cmd); err == nil {
		t.Logf("Controller logs:\n%s", controllerLogs)
	} else {
		t.Logf("Failed to get Controller logs: %s", err)
	}

	t.Log("Fetching Kubernetes events")
	cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
	if eventsOutput, err := utils.Run(cmd); err == nil {
		t.Logf("Kubernetes events:\n%s", eventsOutput)
	} else {
		t.Logf("Failed to get Kubernetes events: %s", err)
	}

	t.Log("Fetching curl-metrics logs")
	cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	if metricsOutput, err := utils.Run(cmd); err == nil {
		t.Logf("Metrics logs:\n%s", metricsOutput)
	} else {
		t.Logf("Failed to get curl-metrics logs: %s", err)
	}

	t.Log("Fetching controller manager pod description")
	cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
	if podDescription, err := utils.Run(cmd); err == nil {
		t.Logf("Pod description:\n%s", podDescription)
	} else {
		t.Log("Failed to describe controller pod")
	}
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken(t *testing.T) string {
	t.Helper()

	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	require.NoError(t, os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644)))

	var out string
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		if !assert.NoError(c, err) {
			return
		}

		var token tokenRequest
		if !assert.NoError(c, json.Unmarshal(output, &token)) {
			return
		}
		out = token.Status.Token
	}, eventuallyTimeout, eventuallyTick)

	return out
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput(t *testing.T) string {
	t.Helper()

	t.Log("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	require.NoError(t, err, "Failed to retrieve logs from curl pod")
	require.Contains(t, metricsOutput, "< HTTP/1.1 200 OK")
	return metricsOutput
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
