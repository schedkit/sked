# Sked

Sked manages OCI-packaged sched_ext schedulers on Kubernetes nodes.

## Install with Helm

With cert-manager installed, run from a checkout of this repository:

```sh
helm install sked ./charts/sked \
  --namespace sked-system --create-namespace \
  --wait --timeout 5m
```

The [chart README](charts/sked/README.md) covers configuration, upgrades, and
uninstalling. Helm does not upgrade CRDs automatically; apply the CRDs from the
new chart before upgrading the release.

## Manager availability

Helm and Kustomize deploy two manager replicas by default. Only the elected
leader reconciles Schedulers; the other replica waits to take over. Both serve
admission webhooks and consume resources, so extra replicas improve availability,
not reconciliation throughput.

A PodDisruptionBudget keeps at least one manager ready during voluntary evictions,
such as a node drain. Kubernetes prefers to place managers on different nodes,
but allows them to share a node if necessary. Use at least two schedulable nodes
for protection against a node failure. The budget does not prevent node failures
or direct pod deletion.

If all managers go down, existing scheduler DaemonSets keep running. Reconciliation
and status updates stop until a manager returns. The [chart README](charts/sked/README.md#availability)
covers replica counts, disruption budgets, and placement overrides.

## Container images

Images live at `ghcr.io/schedkit/sked` and support Linux on AMD64 and ARM64.

Every push to `main` updates the `main` and `latest` image tags. Pushing a Git tag
publishes a matching image tag: `v1.2.3` becomes `ghcr.io/schedkit/sked:v1.2.3`.
`latest` follows `main`, not the latest release. Each build also gets a
`sha-<full-commit-SHA>` tag.

To release, tag the commit you want to ship and push the tag:

```sh
git tag v1.2.3
git push origin v1.2.3
```

[The workflow](.github/workflows/publish-image.yml) uses `GITHUB_TOKEN` to publish;
there's no extra registry secret to set up. After the first publish, set the GHCR
package to public so clusters can pull without credentials, or keep it private
and configure an image pull secret.

### Verify signatures and attestations

The publish workflow signs images from `main` and Git tags with
[Cosign](https://docs.sigstore.dev/cosign/system_config/installation/) using
GitHub Actions OIDC and Sigstore's public transparency log. Older images may be
unsigned.

BuildKit adds an SPDX SBOM and SLSA build provenance (`mode=max`) for AMD64 and
ARM64. Cosign signs the image index, including those attestations. Verify the
index with `cosign verify`, then read the attestations with Docker Buildx.

Install Cosign 3.x and Docker Buildx. For private images, run
`docker login ghcr.io` first.

Replace `v1.2.3` below with the release you want to verify. The commands resolve
the tag once, then verify that digest:

```sh
set -eu
image=ghcr.io/schedkit/sked
tag=v1.2.3
digest=$(docker buildx imagetools inspect "$image:$tag" --format '{{.Manifest.Digest}}')
ref="$image@$digest"
identity="https://github.com/schedkit/sked/.github/workflows/publish-image.yml@refs/tags/$tag"

cosign verify \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity "$identity" \
  "$ref"
```

To verify `main` or `latest` instead, use that image tag and set `identity` to
`https://github.com/schedkit/sked/.github/workflows/publish-image.yml@refs/heads/main`.
A `sha-<full-commit-SHA>` tag uses the identity of the branch or Git tag that
triggered its build. Keep the identity and issuer checks enabled.

After verification passes, download each platform's SBOM and provenance:

```sh
for arch in amd64 arm64; do
  docker buildx imagetools inspect "$ref" \
    --format "{{ json (index .SBOM \"linux/$arch\").SPDX }}" \
    > "sked-$arch.spdx.json"
  docker buildx imagetools inspect "$ref" \
    --format "{{ json (index .Provenance \"linux/$arch\").SLSA }}" \
    > "sked-$arch.provenance.json"
done
```

The SBOM lists packages in the image, including the manager's Go modules. The
provenance records the source revision, build parameters, and base image digests.
Check that its source revision matches the commit you intend to deploy, and
deploy using `$ref` rather than the mutable tag.

### Mirroring images

Copy the complete image index, including its attestations, and its Cosign
signature. Use the verified digest, not a mutable tag.

## Scheduler events

Sked records events for rollout progress, readiness, and failures.
Replace `example` with your Scheduler's name:

```sh
kubectl describe scheduler example
kubectl get events -n default \
  --field-selector involvedObject.kind=Scheduler,involvedObject.name=example \
  --sort-by=.metadata.creationTimestamp
```

Schedulers are cluster-scoped, so their events are stored in `default`, regardless
of the workload namespace.

Event reasons:

- Normal: `RolloutInProgress`, `DaemonSetReady`, and `NoNodesScheduled`.
- Warning: `SchedulerConflict`, `NodesUnavailable`, `SpecInvalid`,
  `ImageVerificationFailed`, `ReconcileFailed`, and `StatusUpdateFailed`.

While a Scheduler is degraded, Sked records a Warning instead of a Normal event,
even when only some nodes have a conflict. When the Scheduler recovers, Sked
records a Normal event. Repeated reconciles and changes to pod counts alone
don't repeat these state-change events.

A `StatusUpdateFailed` warning means Sked couldn't save the Scheduler's status.
Events expire; use the Scheduler's status conditions for its current state.

## Development checks

With Go 1.26 and a C compiler installed, run:

```sh
make test
make coverage
go build ./...
```

`make test` runs the Go tests with race detection and writes `cover.out`.
`make coverage` checks total statement coverage against a 40% minimum. Coverage
includes generated code and test helpers, but excludes E2E tests. To try a higher
threshold locally, run `make coverage COVERAGE_THRESHOLD=45`.

CI reports coverage in the test job's summary, linked from the PR's Checks tab,
and uploads `cover.out` as the `coverage` artifact. The build runs in a separate
job, even if tests fail.

## Manager flags

| Flag | Default |
| --- | --- |
| `--reconcile-timeout` | `1m` |
| `--max-concurrent-reconciles` | `2` |
| `--reconcile-rate-limit-qps` | `5` |
| `--reconcile-rate-limit-burst` | `10` |
| `--reconcile-retry-base-delay` | `1s` |
| `--reconcile-retry-max-delay` | `5m` |

`--reconcile-timeout` caps a single reconcile. Failed reconciles back off exponentially
up to `--reconcile-retry-max-delay`.
