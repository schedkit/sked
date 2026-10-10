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
	"strings"
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

// schedulerName is the name of the Scheduler created by the reconciliation e2e scenario.
const schedulerName = "e2e-scheduler"

// testSchedulerImage is a signed, allowlisted no-op scheduler image that keeps a pod Running.
const testSchedulerImage = "ghcr.io/schedkit/scx_test:v1.1.2"

// updatedSchedulerImage is a second signed, allowlisted no-op image used to exercise updates.
const updatedSchedulerImage = "ghcr.io/schedkit/scx_test:v1.1.3"

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

	t.Run("should reconcile a Scheduler into a DaemonSet and clean it up", func(t *testing.T) {
		t.Cleanup(func() {
			deleteScheduler(t, schedulerName)
			waitForDaemonSetDeleted(t, schedulerName)
		})

		applyScheduler(t, schedulerName, testSchedulerImage)
		firstResolved := waitForSchedulerResolvedImage(t, schedulerName)
		waitForDaemonSetImage(t, schedulerName, firstResolved)
		waitForDaemonSetRollout(t, schedulerName)
		assertDaemonSetOwnedByScheduler(t, schedulerName)
		waitForSchedulerConditions(t, schedulerName)

		// Out-of-band deletion and modification of the owned DaemonSet must be repaired.
		deleteDaemonSet(t, schedulerName)
		waitForDaemonSetImage(t, schedulerName, firstResolved)

		patchDaemonSetImage(t, schedulerName, "ghcr.io/schedkit/scx_test:v1.1.1")
		waitForDaemonSetImage(t, schedulerName, firstResolved)

		applyScheduler(t, schedulerName, updatedSchedulerImage)
		secondResolved := waitForSchedulerResolvedImageChange(t, schedulerName, firstResolved)
		waitForDaemonSetImage(t, schedulerName, secondResolved)
		waitForDaemonSetRollout(t, schedulerName)
		waitForSchedulerConditions(t, schedulerName)

		deleteScheduler(t, schedulerName)
		waitForDaemonSetDeleted(t, schedulerName)
	})
}

func applyScheduler(t *testing.T, name, image string) {
	t.Helper()

	manifest := fmt.Sprintf(`apiVersion: sked.schedkit.io/v1
kind: Scheduler
metadata:
  name: %s
spec:
  sched: %s
`, name, image)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	require.NoError(t, err, "Failed to apply Scheduler %s", name)
}

func deleteScheduler(t *testing.T, name string) {
	t.Helper()

	cmd := exec.Command("kubectl", "delete", "scheduler", name, "--ignore-not-found")
	_, err := utils.Run(cmd)
	require.NoError(t, err, "Failed to delete Scheduler %s", name)
}

func waitForDaemonSetImage(t *testing.T, name, image string) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		cmd := exec.Command("kubectl", "get", "daemonset", name, "-n", namespace,
			"-o", "jsonpath={.spec.template.spec.containers[0].image}")
		output, err := utils.Run(cmd)
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, image, output, "DaemonSet %s has the wrong scheduler image", name)
	}, eventuallyTimeout, eventuallyTick)
}

func deleteDaemonSet(t *testing.T, name string) {
	t.Helper()

	cmd := exec.Command("kubectl", "delete", "daemonset", name, "-n", namespace, "--ignore-not-found")
	_, err := utils.Run(cmd)
	require.NoError(t, err, "Failed to delete DaemonSet %s", name)
}

func patchDaemonSetImage(t *testing.T, name, image string) {
	t.Helper()

	patch := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":"scx","image":%q}]}}}}`, image)
	cmd := exec.Command("kubectl", "patch", "daemonset", name, "-n", namespace, "--type=strategic", "-p", patch)
	_, err := utils.Run(cmd)
	require.NoError(t, err, "Failed to patch DaemonSet %s", name)
}

func waitForSchedulerResolvedImage(t *testing.T, name string) string {
	t.Helper()

	var resolved string
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		cmd := exec.Command("kubectl", "get", "scheduler", name,
			"-o", "jsonpath={.status.resolvedImage}")
		output, err := utils.Run(cmd)
		if !assert.NoError(c, err) {
			return
		}
		if !assert.Contains(c, output, "@sha256:", "Scheduler %s image is not pinned to a digest", name) {
			return
		}
		resolved = output
	}, eventuallyTimeout, eventuallyTick)
	return resolved
}

func waitForSchedulerResolvedImageChange(t *testing.T, name, previous string) string {
	t.Helper()

	var resolved string
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		cmd := exec.Command("kubectl", "get", "scheduler", name,
			"-o", "jsonpath={.status.resolvedImage}")
		output, err := utils.Run(cmd)
		if !assert.NoError(c, err) {
			return
		}
		if !assert.Contains(c, output, "@sha256:", "Scheduler %s image is not pinned to a digest", name) {
			return
		}
		if !assert.NotEqual(c, previous, output, "Scheduler %s resolved image did not change", name) {
			return
		}
		resolved = output
	}, eventuallyTimeout, eventuallyTick)
	return resolved
}

func waitForDaemonSetRollout(t *testing.T, name string) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		cmd := exec.Command("kubectl", "rollout", "status", "daemonset/"+name,
			"-n", namespace, "--timeout=10s")
		_, err := utils.Run(cmd)
		assert.NoError(c, err, "DaemonSet %s did not roll out", name)
	}, 3*time.Minute, 10*time.Second)
}

func waitForSchedulerConditions(t *testing.T, name string) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		cmd := exec.Command("kubectl", "get", "scheduler", name,
			"-o", "jsonpath={.metadata.generation}|{.status.observedGeneration}|"+
				"{.status.conditions[?(@.type=='Ready')].status}|"+
				"{.status.conditions[?(@.type=='Ready')].reason}")
		output, err := utils.Run(cmd)
		if !assert.NoError(c, err) {
			return
		}
		fields := strings.Split(output, "|")
		if !assert.Len(c, fields, 4) {
			return
		}

		assert.Equal(c, fields[0], fields[1], "Scheduler %s status is not caught up with the spec", name)
		assert.Contains(c, []string{"True", "False"}, fields[2], "Scheduler %s has no Ready condition", name)
		assert.NotEmpty(c, fields[3], "Scheduler %s Ready condition has no reason", name)
	}, eventuallyTimeout, eventuallyTick)
}

func waitForDaemonSetDeleted(t *testing.T, name string) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		cmd := exec.Command("kubectl", "get", "daemonset", name, "-n", namespace)
		_, err := utils.Run(cmd)
		assert.Error(c, err, "DaemonSet %s still exists", name)
	}, eventuallyTimeout, eventuallyTick)
}

func assertDaemonSetOwnedByScheduler(t *testing.T, name string) {
	t.Helper()

	cmd := exec.Command("kubectl", "get", "daemonset", name, "-n", namespace, "-o", "json")
	output, err := utils.Run(cmd)
	require.NoError(t, err, "Failed to get DaemonSet %s", name)

	var ds struct {
		Metadata struct {
			Labels          map[string]string `json:"labels"`
			OwnerReferences []struct {
				Kind       string `json:"kind"`
				Name       string `json:"name"`
				Controller bool   `json:"controller"`
			} `json:"ownerReferences"`
		} `json:"metadata"`
		Spec struct {
			Selector struct {
				MatchLabels map[string]string `json:"matchLabels"`
			} `json:"selector"`
			Template struct {
				Spec struct {
					Containers []struct {
						SecurityContext struct {
							Privileged bool `json:"privileged"`
						} `json:"securityContext"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &ds))

	require.Equal(t, "sked-controller", ds.Metadata.Labels["managed-by"])
	require.Len(t, ds.Metadata.OwnerReferences, 1)
	require.Equal(t, "Scheduler", ds.Metadata.OwnerReferences[0].Kind)
	require.Equal(t, name, ds.Metadata.OwnerReferences[0].Name)
	require.True(t, ds.Metadata.OwnerReferences[0].Controller)
	require.Equal(t, name, ds.Spec.Selector.MatchLabels["name"])
	require.Len(t, ds.Spec.Template.Spec.Containers, 1)
	require.True(t, ds.Spec.Template.Spec.Containers[0].SecurityContext.Privileged)
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
