package helm

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagerAvailabilityDefaults(t *testing.T) {
	for _, tc := range []struct {
		name      string
		release   string
		namespace string
		values    string
		fullname  string
	}{
		{"default", "sked", "sked-system", "", "sked"},
		{"different release", "production", "operators", "", "production-sked"},
		{"name override", "production", "operators", "nameOverride: scheduler\n", "production-scheduler"},
		{"fullname override", "production", "operators", "fullnameOverride: custom\n", "custom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := render(t, tc.release, tc.namespace, tc.values)
			assertManagerAvailability(t, docs, tc.fullname+"-controller-manager", tc.namespace)
			require.Contains(t, manager(t, docs, tc.fullname)["args"], "--leader-elect=true")
		})
	}

	t.Run("kustomize base", func(t *testing.T) {
		kustomize := os.Getenv("KUSTOMIZE")
		if kustomize == "" {
			t.Skip("run make test-helm to test the manifests")
		}
		docs := decode(t, command(t, kustomize, "build", "config/manager"))
		assertManagerAvailability(t, docs, "controller-manager", "system")
		containers := nested(t, docs["Deployment/controller-manager"], "spec", "template", "spec", "containers").([]any)
		require.Contains(t, nested(t, containers[0], "args"), "--leader-elect")
	})
}

func assertManagerAvailability(t *testing.T, docs map[string]resource, name, namespace string) {
	t.Helper()
	deployment := docs["Deployment/"+name]
	pdb := docs["PodDisruptionBudget/"+name]
	require.NotNil(t, deployment)
	require.NotNil(t, pdb)
	require.Equal(t, float64(2), nested(t, deployment, "spec", "replicas"))
	require.Equal(t, "policy/v1", pdb["apiVersion"])
	require.Equal(t, namespace, nested(t, pdb, "metadata", "namespace"))
	require.Equal(t, float64(1), nested(t, pdb, "spec", "minAvailable"))
	labels := nested(t, deployment, "spec", "template", "metadata", "labels")
	require.Equal(t, labels, nested(t, pdb, "spec", "selector", "matchLabels"))
	constraints := nested(t, deployment, "spec", "template", "spec", "topologySpreadConstraints").([]any)
	require.Len(t, constraints, 1)
	require.Equal(t, float64(1), nested(t, constraints[0], "maxSkew"))
	require.Equal(t, "kubernetes.io/hostname", nested(t, constraints[0], "topologyKey"))
	require.Equal(t, "ScheduleAnyway", nested(t, constraints[0], "whenUnsatisfiable"))
	require.Equal(t, labels, nested(t, constraints[0], "labelSelector", "matchLabels"))
}

func TestManagerAvailabilityOverrides(t *testing.T) {
	docs := render(t, "production", "operators", `
replicaCount: 3
podDisruptionBudget:
  minAvailable: "50%"
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: topology.kubernetes.io/zone
    whenUnsatisfiable: DoNotSchedule
    labelSelector:
      matchLabels:
        app.kubernetes.io/instance: production
        control-plane: controller-manager
`)
	name := "production-sked-controller-manager"
	require.Equal(t, float64(3), nested(t, docs["Deployment/"+name], "spec", "replicas"))
	require.Equal(t, "50%", nested(t, docs["PodDisruptionBudget/"+name], "spec", "minAvailable"))
	constraints := nested(t, docs["Deployment/"+name], "spec", "template", "spec", "topologySpreadConstraints").([]any)
	require.Len(t, constraints, 1)
	require.Equal(t, "topology.kubernetes.io/zone", nested(t, constraints[0], "topologyKey"))
	require.Equal(t, "DoNotSchedule", nested(t, constraints[0], "whenUnsatisfiable"))
	require.Equal(t, "production", nested(t, constraints[0], "labelSelector", "matchLabels", "app.kubernetes.io/instance"))
}

func TestSingleManagerOptOut(t *testing.T) {
	docs := render(t, "sked", "operators", `
replicaCount: 1
controller:
  leaderElection: false
podDisruptionBudget:
  enabled: false
topologySpreadConstraints: []
`)
	require.Equal(t, float64(1), nested(t, docs["Deployment/sked-controller-manager"], "spec", "replicas"))
	require.Contains(t, manager(t, docs, "sked")["args"], "--leader-elect=false")
	require.NotContains(t, docs, "PodDisruptionBudget/sked-controller-manager")
	pod := nested(t, docs["Deployment/sked-controller-manager"], "spec", "template", "spec")
	require.Empty(t, nested(t, pod, "topologySpreadConstraints"))
}
