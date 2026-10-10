# Sked

Sked manages OCI-packaged sched_ext schedulers on Kubernetes nodes.

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
