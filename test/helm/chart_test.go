package helm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	"github.com/schedkit/sked/internal/controller"
)

type resource map[string]any

func root(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs("../..")
	require.NoError(t, err)
	return path
}

func command(t *testing.T, tool string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(tool, args...)
	cmd.Dir = root(t)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %v\n%s", tool, args, out)
	return out
}

func helm(t *testing.T) string {
	t.Helper()
	path := os.Getenv("HELM")
	if path == "" {
		t.Skip("run make test-helm to test the chart")
	}
	return path
}

func decode(t *testing.T, data []byte) map[string]resource {
	t.Helper()
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	docs := map[string]resource{}
	for {
		var doc resource
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if len(doc) == 0 {
			continue
		}
		key := fmt.Sprintf("%s/%s", doc["kind"], doc["metadata"].(map[string]any)["name"])
		require.NotContains(t, docs, key)
		docs[key] = doc
	}
	return docs
}

func render(t *testing.T, release, namespace, values string, args ...string) map[string]resource {
	t.Helper()
	cmd := []string{"template", release, "charts/sked", "--namespace", namespace, "--include-crds"}
	if values != "" {
		path := filepath.Join(t.TempDir(), "values.yaml")
		require.NoError(t, os.WriteFile(path, []byte(values), 0o600))
		cmd = append(cmd, "--values", path)
	}
	cmd = append(cmd, args...)
	return decode(t, command(t, helm(t), cmd...))
}

func nested(t *testing.T, value any, keys ...string) any {
	t.Helper()
	for _, key := range keys {
		m, ok := value.(map[string]any)
		if r, isResource := value.(resource); isResource {
			m, ok = map[string]any(r), true
		}
		require.True(t, ok, "expected object at %s, got %T", key, value)
		value, ok = m[key]
		require.True(t, ok, "missing %s", key)
	}
	return value
}

func manager(t *testing.T, docs map[string]resource, name string) map[string]any {
	t.Helper()
	deployment := docs["Deployment/"+name+"-controller-manager"]
	containers := nested(t, deployment, "spec", "template", "spec", "containers").([]any)
	require.Len(t, containers, 1)
	return containers[0].(map[string]any)
}

func normalize(value any) any {
	switch v := value.(type) {
	case resource:
		return normalize(map[string]any(v))
	case map[string]any:
		for key, child := range v {
			if key == "labels" || key == "checksum/trust-policy" ||
				key == "app.kubernetes.io/name" || key == "app.kubernetes.io/instance" {
				delete(v, key)
				continue
			}
			v[key] = normalize(child)
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = normalize(child)
		}
		return v
	case string:
		return strings.NewReplacer(
			"sked-controller-manager-metrics-service", "sked-metrics",
			"sked-validating-webhook-configuration", "sked-webhook",
			"sked-leader-election-rolebinding", "sked-leader-election-binding",
			"sked-metrics-auth-rolebinding", "sked-metrics-auth-binding",
		).Replace(v)
	default:
		return value
	}
}

func TestDefaultManifestsMatchKustomize(t *testing.T) {
	actual := render(t, "sked", "sked-system", "")
	kustomize := os.Getenv("KUSTOMIZE")
	require.NotEmpty(t, kustomize, "run make test-helm")
	expected := decode(t, command(t, kustomize, "build", "config/default"))
	delete(expected, "Namespace/sked-system")

	chartData, err := os.ReadFile(filepath.Join(root(t), "charts/sked/Chart.yaml"))
	require.NoError(t, err)
	var chart struct {
		AppVersion string `json:"appVersion"`
	}
	require.NoError(t, yaml.Unmarshal(chartData, &chart))
	container := manager(t, expected, "sked")
	container["image"] = "ghcr.io/schedkit/sked:" + chart.AppVersion
	container["imagePullPolicy"] = "IfNotPresent"
	container["ports"] = append(container["ports"].([]any), map[string]any{
		"containerPort": float64(8443), "name": "metrics", "protocol": "TCP",
	})
	args := container["args"].([]any)
	for i, arg := range args {
		if arg == "--leader-elect" {
			args[i] = "--leader-elect=true"
		}
	}
	container["args"] = append(args,
		"--metrics-secure=true", "--enable-http2=false",
		"--trust-policy-namespace=sked-system", "--trust-policy-configmap-key=policy.yaml",
		"--workload-namespace=sked-system",
		"--reconcile-timeout="+compactDuration(controller.DefaultReconcileTimeout.String()),
		fmt.Sprintf("--max-concurrent-reconciles=%d", controller.DefaultMaxConcurrentReconciles),
		fmt.Sprintf("--reconcile-rate-limit-qps=%d", controller.DefaultRateLimitQPS),
		fmt.Sprintf("--reconcile-rate-limit-burst=%d", controller.DefaultRateLimitBurst),
		"--reconcile-retry-base-delay="+compactDuration(controller.DefaultRetryBaseDelay.String()),
		"--reconcile-retry-max-delay="+compactDuration(controller.DefaultRetryMaxDelay.String()))
	require.ElementsMatch(t, container["args"], manager(t, actual, "sked")["args"])
	delete(container, "args")
	delete(manager(t, actual, "sked"), "args")

	pod := nested(t, expected["Deployment/sked-controller-manager"], "spec", "template", "spec").(map[string]any)
	pod["volumes"].([]any)[0].(map[string]any)["secret"].(map[string]any)["secretName"] = "sked-webhook-server-cert"
	certificate := nested(t, expected["Certificate/sked-serving-cert"], "spec").(map[string]any)
	certificate["secretName"] = "sked-webhook-server-cert"

	require.Len(t, actual, len(expected))
	for _, doc := range expected {
		normalized := normalize(doc).(map[string]any)
		key := fmt.Sprintf("%s/%s", doc["kind"], nested(t, normalized, "metadata", "name"))
		require.Contains(t, actual, key)
		require.Equal(t, normalized, normalize(actual[key]), key)
	}
}

func compactDuration(value string) string {
	if strings.HasSuffix(value, "m0s") {
		return strings.TrimSuffix(value, "0s")
	}
	return value
}

func TestOverridesAndReferences(t *testing.T) {
	docs := render(t, "test", "operators", `
replicaCount: 2
image:
  repository: registry.example/sked
  tag: ignored
  digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
imagePullSecrets:
  - name: registry
serviceAccount:
  create: false
  name: existing-controller
controller:
  workloadNamespace: schedulers
  reconcileTimeout: 30s
  maxConcurrentReconciles: 4
  reconcileRateLimitQPS: 2.5
  reconcileRateLimitBurst: 8
  reconcileRetryBaseDelay: 2s
  reconcileRetryMaxDelay: 2m
  extraArgs:
    - --zap-log-level=debug
trustPolicy:
  key: custom.yaml
  policy:
    allowedImages: [registry.example/scheduler]
    verifySignatures: false
webhook:
  certManager:
    issuerRef:
      kind: ClusterIssuer
      name: platform-ca
metrics:
  serviceMonitor:
    enabled: true
    labels:
      release: monitoring
  readerSubjects:
    - kind: ServiceAccount
      name: prometheus
      namespace: monitoring
networkPolicy:
  enabled: true
nodeSelector:
  kubernetes.io/arch: arm64
tolerations:
  - key: dedicated
    operator: Exists
extraEnv:
  - name: GOMEMLIMIT
    value: 100MiB
extraVolumes:
  - name: root
    configMap:
      name: trusted-root
extraVolumeMounts:
  - name: root
    mountPath: /etc/sigstore
    readOnly: true
`)
	name := "test-sked"
	container := manager(t, docs, name)
	require.Equal(t, "registry.example/sked@sha256:"+strings.Repeat("a", 64), container["image"])
	for _, arg := range []string{
		"--workload-namespace=schedulers", "--trust-policy-configmap=test-sked-scheduler-trust-policy",
		"--trust-policy-namespace=operators", "--trust-policy-configmap-key=custom.yaml",
		"--reconcile-timeout=30s", "--max-concurrent-reconciles=4", "--reconcile-rate-limit-qps=2.5",
		"--reconcile-rate-limit-burst=8", "--reconcile-retry-base-delay=2s", "--reconcile-retry-max-delay=2m",
		"--zap-log-level=debug",
	} {
		require.Contains(t, container["args"], arg)
	}
	pod := nested(t, docs["Deployment/"+name+"-controller-manager"], "spec", "template", "spec")
	require.Equal(t, "existing-controller", nested(t, pod, "serviceAccountName"))
	require.Len(t, nested(t, pod, "imagePullSecrets"), 1)
	require.Equal(t, "arm64", nested(t, pod, "nodeSelector", "kubernetes.io/arch"))
	require.Len(t, nested(t, pod, "tolerations"), 1)
	require.Len(t, nested(t, pod, "volumes"), 2)
	require.Len(t, container["volumeMounts"], 2)
	require.Len(t, container["env"], 2)
	require.Equal(t, float64(2), nested(t, docs["Deployment/"+name+"-controller-manager"], "spec", "replicas"))
	require.NotContains(t, docs, "ServiceAccount/existing-controller")
	require.NotContains(t, docs, "Issuer/"+name+"-selfsigned-issuer")
	require.Equal(t, "platform-ca", nested(t, docs["Certificate/"+name+"-serving-cert"], "spec", "issuerRef", "name"))

	policyData := nested(t, docs["ConfigMap/"+name+"-scheduler-trust-policy"], "data", "custom.yaml").(string)
	var policy map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(policyData), &policy))
	require.Equal(t, false, policy["verifySignatures"])
	require.Equal(t, []any{"registry.example/scheduler"}, policy["allowedImages"])

	monitor := docs["ServiceMonitor/"+name+"-metrics"]
	require.Equal(t, "monitoring", nested(t, monitor, "metadata", "labels", "release"))
	require.Equal(t, "metrics", nested(t, monitor, "spec", "selector", "matchLabels", "app.kubernetes.io/component"))
	require.Len(t, nested(t, docs["NetworkPolicy/"+name+"-ingress"], "spec", "ingress"), 2)

	for _, doc := range docs {
		metadata := doc["metadata"].(map[string]any)
		if namespace, ok := metadata["namespace"]; ok {
			require.Equal(t, "operators", namespace)
		}
		if doc["kind"] == "ClusterRoleBinding" || doc["kind"] == "RoleBinding" {
			role := doc["roleRef"].(map[string]any)
			require.Contains(t, docs, fmt.Sprintf("%s/%s", role["kind"], role["name"]))
			if metadata["name"] != name+"-metrics-reader-binding" {
				require.Equal(t, []any{map[string]any{
					"kind": "ServiceAccount", "name": "existing-controller", "namespace": "operators",
				}}, doc["subjects"])
			}
		}
	}
	webhook := docs["ValidatingWebhookConfiguration/"+name+"-webhook"]["webhooks"].([]any)[0]
	require.Equal(t, name+"-webhook-service", nested(t, webhook, "clientConfig", "service", "name"))
	require.Equal(t, "operators", nested(t, webhook, "clientConfig", "service", "namespace"))
	configuration := docs["ValidatingWebhookConfiguration/"+name+"-webhook"]
	require.Equal(t, "operators/"+name+"-serving-cert",
		nested(t, configuration, "metadata", "annotations", "cert-manager.io/inject-ca-from"))
	require.Equal(t, []any{
		name + "-webhook-service.operators.svc", name + "-webhook-service.operators.svc.cluster.local",
	}, nested(t, docs["Certificate/"+name+"-serving-cert"], "spec", "dnsNames"))
}

func TestExternalResourcesAndDisabledMetrics(t *testing.T) {
	docs := render(t, "sked", "operators", `
rbac:
  create: false
serviceAccount:
  create: false
  name: external
trustPolicy:
  existingConfigMap: trusted-schedulers
  namespace: security
webhook:
  certManager:
    enabled: false
  secretName: external-tls
  caBundle: Y2E=
metrics:
  enabled: false
networkPolicy:
  enabled: true
`)
	for _, doc := range docs {
		require.NotContains(t, []string{
			"Issuer", "Certificate", "ConfigMap", "ServiceAccount", "Role", "ClusterRole",
			"RoleBinding", "ClusterRoleBinding", "ServiceMonitor",
		}, doc["kind"])
	}
	require.NotContains(t, docs, "Service/sked-metrics")
	container := manager(t, docs, "sked")
	require.Contains(t, container["args"], "--metrics-bind-address=0")
	require.Contains(t, container["args"], "--trust-policy-configmap=trusted-schedulers")
	require.Contains(t, container["args"], "--trust-policy-namespace=security")
	require.Len(t, container["ports"], 1)
	pod := nested(t, docs["Deployment/sked-controller-manager"], "spec", "template", "spec")
	volume := nested(t, pod, "volumes").([]any)[0]
	require.Equal(t, "external-tls", nested(t, volume, "secret", "secretName"))
	webhook := docs["ValidatingWebhookConfiguration/sked-webhook"]["webhooks"].([]any)[0]
	require.Equal(t, "Y2E=", nested(t, webhook, "clientConfig", "caBundle"))
	require.Len(t, nested(t, docs["NetworkPolicy/sked-ingress"], "spec", "ingress"), 1)
}

func TestHTTPMetrics(t *testing.T) {
	docs := render(t, "sked", "operators", `
metrics:
  secure: false
  port: 9090
  serviceMonitor:
    enabled: true
`)
	container := manager(t, docs, "sked")
	require.Contains(t, container["args"], "--metrics-bind-address=:9090")
	require.Contains(t, container["args"], "--metrics-secure=false")
	require.NotContains(t, docs, "ClusterRole/sked-metrics-auth-role")
	endpoint := nested(t, docs["ServiceMonitor/sked-metrics"], "spec", "endpoints").([]any)[0].(map[string]any)
	require.Equal(t, "http", endpoint["scheme"])
	require.Equal(t, "http", endpoint["port"])
	require.NotContains(t, endpoint, "tlsConfig")
	require.NotContains(t, endpoint, "bearerTokenFile")
	port := nested(t, docs["Service/sked-metrics"], "spec", "ports").([]any)[0]
	require.Equal(t, float64(9090), nested(t, port, "targetPort"))
}

func TestUpgradeKeepsSelectorsAndReferences(t *testing.T) {
	installed := render(t, "production", "operators", "")
	upgraded := render(t, "production", "operators", "image:\n  tag: v0.2.0\nreplicaCount: 3\n", "--is-upgrade")
	key := "Deployment/production-sked-controller-manager"
	require.Equal(t, float64(2), nested(t, installed[key], "spec", "replicas"))
	require.Equal(t, float64(3), nested(t, upgraded[key], "spec", "replicas"))
	pdb := "PodDisruptionBudget/production-sked-controller-manager"
	require.Equal(t, nested(t, installed[pdb], "spec", "selector"), nested(t, upgraded[pdb], "spec", "selector"))
	require.Equal(t, nested(t, installed[key], "spec", "selector"), nested(t, upgraded[key], "spec", "selector"))
	require.Equal(t, nested(t, installed[key], "spec", "template", "metadata", "labels"),
		nested(t, upgraded[key], "spec", "template", "metadata", "labels"))
	require.Equal(t, "ghcr.io/schedkit/sked:v0.2.0", manager(t, upgraded, "production-sked")["image"])
	for key := range installed {
		require.Contains(t, upgraded, key)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, test := range []struct{ setting, message string }{
		{"serviceAccount.create=false", "serviceAccount.name is required"},
		{"webhook.certManager.enabled=false,webhook.caBundle=Y2E=", "webhook.secretName is required"},
		{"webhook.certManager.enabled=false,webhook.secretName=tls", "webhook.caBundle is required"},
		{"trustPolicy.policy=invalid", "policy"},
		{"replicaCount=-1", "replicaCount"},
		{"controller.leaderElection=false", "controller.leaderElection must be enabled"},
		{"podDisruptionBudget.minAvailable=-1", "minAvailable"},
		{"podDisruptionBudget.minAvailable=101%", "minAvailable"},
		{"topologySpreadConstraints=invalid", "topologySpreadConstraints"},
		{"metrics.port=70000", "port"},
		{"controller.maxConcurrentReconciles=0", "maxConcurrentReconciles"},
	} {
		t.Run(test.setting, func(t *testing.T) {
			cmd := exec.Command(helm(t), "template", "sked", "charts/sked", "--set", test.setting)
			cmd.Dir = root(t)
			out, err := cmd.CombinedOutput()
			require.Error(t, err)
			require.Contains(t, string(out), test.message)
		})
	}
}
