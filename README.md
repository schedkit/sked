# Sked

Sked manages OCI-packaged sched_ext schedulers on Kubernetes nodes.

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
