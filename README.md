# Sked

Sked manages OCI-packaged sched_ext schedulers on Kubernetes nodes.

## Install with Helm

With [cert-manager](https://cert-manager.io/docs/installation/) installed:

```sh
helm repo add schedkit https://schedkit.github.io/sked
helm repo update
helm install sked schedkit/sked \
  --namespace sked-system --create-namespace \
  --wait --timeout 5m
```

Use `--version 1.2.3` to select a specific chart release. Chart archives are also
attached to [GitHub Releases](https://github.com/schedkit/sked/releases).
For development, replace `schedkit/sked` with `./charts/sked` from a checkout.

The [chart README](charts/sked/README.md) covers configuration, upgrades, and
uninstalling. Helm does not upgrade CRDs automatically; apply the CRDs from the
new chart before upgrading the release.

## Controller availability

Helm and Kustomize deploy two controller replicas by default. Only the elected
leader reconciles Schedulers; the other replica waits to take over. Both serve
admission webhooks and consume resources, so extra replicas improve availability,
not reconciliation throughput.

A PodDisruptionBudget keeps at least one controller ready during voluntary
evictions, such as a node drain. Kubernetes prefers to place controllers on
different nodes, but allows them to share a node if necessary. Use at least two
schedulable nodes for protection against a node failure. The budget does not
prevent node failures or direct pod deletion.

If all controllers go down, existing scheduler DaemonSets keep running.
Reconciliation and status updates stop until a controller returns. The
[chart README](charts/sked/README.md#availability) covers replica counts,
disruption budgets, and placement overrides.

## Container images

Images live at `ghcr.io/schedkit/sked` and support Linux on AMD64 and ARM64.

Every push to `main` updates the `main` and `latest` image tags. Pushing a Git tag
publishes a matching image tag: `v1.2.3` becomes `ghcr.io/schedkit/sked:v1.2.3`.
`latest` follows `main`, not the latest release. Each build also gets a
`sha-<full-commit-SHA>` tag.

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

The SBOM lists packages in the image, including the controller's Go modules. The
provenance records the source revision, build parameters, and base image digests.
Check that its source revision matches the commit you intend to deploy, and
deploy using `$ref` rather than the mutable tag.

### Mirroring images

Copy the complete image index, including its attestations, and its Cosign
signature. Use the verified digest, not a mutable tag.

## Releases and Helm repository

Tag the commit you want to ship and push the tag:

```sh
git tag v1.2.3
git push origin v1.2.3
```

After the image has been published and signed, the
[release workflow](.github/workflows/publish-image.yml):

- Packages `charts/sked` as `sked-1.2.3.tgz`, with chart version `1.2.3` and
  `appVersion: v1.2.3`, so installs use the matching controller image.
- Creates the GitHub Release if needed and attaches the chart archive. The same
  archive is available as the workflow's `helm-chart` artifact.
- Creates or updates `gh-pages` with the archive and `index.yaml`, preserving old
  versions, then deploys that branch's contents to GitHub Pages.

Chart release tags must be `vMAJOR.MINOR.PATCH` or `MAJOR.MINOR.PATCH`, optionally
with a SemVer prerelease suffix such as `-rc.1`. Build metadata (`+...`) is not
supported because it cannot be used unchanged as a container image tag. Other
tag formats still publish images, but the chart release job rejects them.
Prereleases are marked on GitHub; Helm users must select them with `--devel` or
an explicit `--version`. Push each release tag separately.

Release versions are immutable: don't move a published tag or use both `v1.2.3`
and `1.2.3`. Re-running a release reuses its existing chart asset, and the
repository publisher refuses to overwrite an archive with different contents.
Checked-in chart metadata does not need a version bump for the release; packaging
derives both versions from the tag without changing source files.

### One-time repository setup

1. In **Settings → Pages → Build and deployment**, set **Source** to
   **GitHub Actions**. No manually created `gh-pages` branch is needed.
2. If the `github-pages` environment has deployment branch/tag restrictions,
   allow your release tags.
3. Ensure repository rules allow `GITHUB_TOKEN` to create and update `gh-pages`
   and create releases. The workflow requests `contents: write`, `pages: write`,
   and `id-token: write` only in the jobs that need them. No PAT is required.

The deployment job checks out `gh-pages` and uploads it directly to Pages. Don't
select Pages' **Deploy from a branch** mode: commits made with `GITHUB_TOKEN`
don't trigger a branch-based Pages build. The repository becomes available at
`https://schedkit.github.io/sked` after the first successful tagged release and
Pages deployment. For a fork, use its Pages URL instead.

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

With Go 1.27 and a C compiler installed, run:

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

## Controller flags

| Flag | Default |
| --- | --- |
| `--reconcile-timeout` | `1m` |
| `--max-concurrent-reconciles` | `2` |
| `--reconcile-rate-limit-qps` | `5` |
| `--reconcile-rate-limit-burst` | `10` |
| `--reconcile-retry-base-delay` | `1s` |
| `--reconcile-retry-max-delay` | `5m` |
| `--log-format` | `json` |
| `--log-level` | `info` |
| `--log-verbosity` | unset |

`--reconcile-timeout` caps a single reconcile. Failed reconciles back off exponentially
up to `--reconcile-retry-max-delay`.

## Controller logging

The controller uses Go's standard-library `log/slog` backend through Logr. After
logging initialization, controller-runtime, Slog, and Klog write to the same
handler on stderr. Helm and Kustomize rely on the binary's JSON/info defaults.

- `--log-format=json|text` selects newline-delimited JSON or human-readable text.
- `--log-level=info|debug|error` selects a severity threshold. `debug` uses native
  Slog debug (`-4`), enabling Logr `V(1)` through `V(4)`.
- `--log-verbosity=N` selects an exact Logr threshold, from `0` to `2147483647`.
  `0` is info; `2` enables `V(1)` and `V(2)`, but not `V(3)`. Errors remain enabled.

Explicitly setting both `--log-level` and `--log-verbosity` is an error, even if
`--log-level=info`. An implicit default does not conflict with `--log-verbosity`.
Format and verbosity are independent; there is no development-mode switch.

For local development:

```sh
go run ./cmd/main.go --log-format=text --log-level=debug
```

For a precise verbosity threshold:

```sh
go run ./cmd/main.go --log-verbosity=2
```

### JSON fields

| Field | Meaning |
| --- | --- |
| `time` | RFC3339 timestamp with fractional precision as emitted by Slog |
| `level` | Native Slog severity: `INFO`, `ERROR`, `DEBUG`, or intermediate levels |
| `msg` | Message |
| `logger` | Optional slash-separated logger name, such as `controller-runtime/metrics` |
| `err` | Error passed to Logr's `Error`, when non-nil |

Reconcile logs retain controller/resource identity and `reconcileID`. Other values
remain structured fields. Logr `V(n)` maps to Slog level `-n`: for example, `V(1)`
is rendered as `DEBUG+3`, `V(2)` as `DEBUG+2`, and `V(4)` as `DEBUG`.

Ordinary errors do not include automatic stack traces. Logs are not sampled;
repeated errors can produce more output than with the previous production Zap
configuration. Successful no-op reconciles log at `V(1)` rather than info; actual
workload creations and changes remain visible at info. Kubernetes events and
status conditions are unchanged.

Use `log.FromContext(ctx)` in controller code to retain request fields, and log
Kubernetes objects as explicit references (`klog.KObj` / `klog.KRef`) or selected
fields. Slog does not reproduce Zap's Kubernetes-aware object truncation: logging
a whole object can expose its specification, environments, or sensitive data.
Do not log Secret contents or full trust-policy documents.

Sigstore/Rekor still brings a transitive Zap dependency and independently
configured loggers. These flags do not control every third-party log stream;
this migration does not make the binary entirely Zap-free.

### Breaking change: migrating from Zap

**Remove every `--zap-*` argument before upgrading.** Legacy flags are rejected
and prevent startup; there are no aliases. Update custom Deployments, scripts,
and Helm `controller.extraArgs`, including saved or reused release values.

An old invocation:

```sh
go run ./cmd/main.go --zap-devel=true --zap-log-level=debug
```

To retain its text output and `V(1)` threshold, use:

```sh
go run ./cmd/main.go --log-format=text --log-verbosity=1
```

Use `--log-level=debug` instead for the new, more verbose `V(4)` threshold.
Remove `--zap-devel=false` without replacement to use JSON/info defaults. Replace
`--zap-encoder=console` with `--log-format=text`. Remove `--zap-stacktrace-level`
and `--zap-time-encoding`; automatic error stacks and custom timestamp encodings
are no longer supported.

Update log consumers from `ts` to `time`, `error` to `err`, lowercase to native
uppercase severity names, and dotted to slash-separated logger names. For example,
the old metrics filter:

```sh
jq -c 'select(.logger == "controller-runtime.metrics" and .level == "info")'
```

becomes:

```sh
jq -c 'select(.logger == "controller-runtime/metrics" and .level == "INFO")'
```

See the [Helm logging examples](charts/sked/README.md#logging) for values-file changes.
