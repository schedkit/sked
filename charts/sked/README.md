# Sked Helm chart

## Install

Install [cert-manager](https://cert-manager.io/docs/installation/) first, then run
from a checkout of this repository:

```sh
helm install sked ./charts/sked \
  --namespace sked-system --create-namespace \
  --wait --timeout 5m
```

Helm 3 and 4 are supported. Installation needs permission to create CRDs, cluster
RBAC, and admission webhooks. Nodes running schedulers need sched_ext support.

Use **one Sked installation per cluster**: every controller watches all Scheduler
objects. Don't install this alongside a Kustomize deployment; the chart does not
adopt its resources.

## Configure

Pass overrides with `-f my-values.yaml`. The full list is in
[values.yaml](values.yaml). For example:

```yaml
replicaCount: 3
controller:
  reconcileTimeout: 30s
  maxConcurrentReconciles: 4
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: "1"
    memory: 256Mi
```

Scheduler DaemonSets run in the release namespace unless you set
`controller.workloadNamespace` to another existing namespace.

The image defaults to `ghcr.io/schedkit/sked` at `Chart.yaml`'s `appVersion`.
Override `image.tag` or `image.digest`; a digest wins if both are set.
`imagePullSecrets` must refer to secrets in the release namespace.

To reuse a service account, set `serviceAccount.create=false` and
`serviceAccount.name`. Set `rbac.create=false` only if you provide the roles and
bindings yourself.

### Availability

The defaults are two replicas, leader election enabled, and a PodDisruptionBudget
with `minAvailable: 1`. Only the leader reconciles; followers wait to take over
but still serve webhooks and use CPU and memory. Adding replicas does not increase
reconciliation throughput. Existing scheduler DaemonSets keep running even if all
managers are down, but reconciliation and status updates stop.

A soft topology-spread rule prefers different nodes using `kubernetes.io/hostname`.
It allows replicas to share a node, so single-node installs still work. At least
two schedulable nodes are needed to survive a node failure. The PDB protects
against voluntary evictions, not node failures or direct pod deletion.

Use `replicaCount` to change the number of managers. The chart rejects multiple
replicas with `controller.leaderElection=false`; don't bypass this through
`controller.extraArgs`. `podDisruptionBudget.minAvailable` accepts an integer or
a percentage such as `"50%"`. Setting it equal to the replica count blocks drains.
For a single-replica development install without eviction protection:

```yaml
replicaCount: 1
podDisruptionBudget:
  enabled: false
```

`topologySpreadConstraints: null` uses the default rule with this release's pod
selector. Set it to `[]` to disable spreading, or supply a list of Kubernetes
spread constraints to replace the rule. Custom constraints must include a
`labelSelector` matching the manager pods. `nodeSelector`, `tolerations`, and
`affinity` can also restrict placement.

### Trust policy

The default policy allows signed Schedkit scheduler images, matching
[`config/trustpolicy/trust_policy.yaml`](../../config/trustpolicy/trust_policy.yaml).
To replace it:

```yaml
trustPolicy:
  policy:
    allowedImages:
      - ghcr.io/my-org/my-scheduler
    verifySignatures: true
    cosign:
      issuer: https://token.actions.githubusercontent.com
      identity: https://github.com/my-org/my-scheduler/.github/workflows/release.yml@refs/heads/main
```

Or set `trustPolicy.existingConfigMap` to a ConfigMap you manage separately.
`trustPolicy.namespace` defaults to the release namespace; `trustPolicy.key`
defaults to `policy.yaml`. Changes to a chart-managed policy restart the controller
on upgrade.

### Webhook certificates

The webhook is always on. By default, cert-manager issues its certificate through
a self-signed Issuer created by the chart. To use an existing issuer:

```yaml
webhook:
  certManager:
    issuerRef:
      name: platform-ca
      kind: ClusterIssuer
```

Without cert-manager, set these three values:

- `webhook.certManager.enabled: false`
- `webhook.secretName`: a secret in the release namespace with `tls.crt` and `tls.key`
- `webhook.caBundle`: the base64-encoded PEM CA certificate

The certificate must cover `<fullname>-webhook-service.<namespace>.svc` and
`<fullname>-webhook-service.<namespace>.svc.cluster.local`. You handle rotation
when supplying your own secret.

### Metrics

Metrics use authenticated HTTPS on port 8443. With Prometheus Operator installed:

```yaml
metrics:
  serviceMonitor:
    enabled: true
  readerSubjects:
    - kind: ServiceAccount
      name: prometheus
      namespace: monitoring
```

Set `metrics.serviceMonitor.labels` if Prometheus selects monitors by label.
The manager uses a self-signed metrics certificate, so the default monitor skips
certificate verification, just like the Kustomize monitor. Webhook certificates
do not configure metrics TLS. Setting `metrics.secure=false` also removes
metrics authentication.

Enable `networkPolicy.enabled` to restrict metrics ingress to namespaces labeled
`metrics: enabled`. Webhook ingress stays open for the API server. Your CNI must
enforce NetworkPolicy for this to have any effect.

## Upgrade and rollback

Helm **does not upgrade CRDs**. From the checkout you want to deploy, apply them
first, then upgrade using your saved values:

```sh
kubectl apply -f ./charts/sked/crds/
helm upgrade sked ./charts/sked \
  --namespace sked-system -f my-values.yaml \
  --wait --timeout 5m
```

Don't change the release name, namespace, `nameOverride`, or `fullnameOverride`
on upgrade; they determine resource names and Deployment selectors.

```sh
helm history sked --namespace sked-system
helm rollback sked <revision> --namespace sked-system --wait --timeout 5m
```

Rollback does not restore CRDs or Scheduler objects. Check that the old controller
supports the installed schema before rolling back.

## Uninstall

```sh
helm uninstall sked --namespace sked-system
```

The CRD, Scheduler objects, and their DaemonSets stay behind. To stop schedulers,
delete their Scheduler objects and wait for the DaemonSets to disappear **before**
uninstalling. Deleting the CRD removes every Scheduler object in the cluster.

## Working on the chart

CRDs and `files/` are generated from `config/`; don't edit those copies directly.
After changing the API, RBAC, webhook rules, or default trust policy:

```sh
make helm-sync
make test-helm
```

CI checks for stale generated files and compares rendered defaults with Kustomize
on Helm 3 and 4. To test installation, failover, eviction protection, upgrade,
rollback, and uninstall locally, run `make test-helm-e2e`. It needs Docker and Kind,
creates a temporary cluster with two workers, and removes it afterward.

Bump `version` for chart changes and `appVersion` for a new default controller
release. Package the chart with `helm package ./charts/sked --destination /tmp`.
