#!/usr/bin/env bash
set -euo pipefail

deadline=$((SECONDS + ${2:-60}))
while (( SECONDS < deadline )); do
  remaining=$((deadline - SECONDS))
  if (( remaining <= 0 )); then
    break
  fi
  request_timeout=$((remaining < 15 ? remaining : 15))
  if response=$(kubectl apply --dry-run=server --request-timeout="${request_timeout}s" -f "$1" 2>&1); then
    printf '%s\n' "$response" 'The webhook accepted an untrusted image' >&2
    exit 1
  fi
  printf '%s\n' "$response" >&2

  if [[ $response == *'admission webhook "vscheduler-v1.kb.io" denied the request: image "registry.example/untrusted:latest" is not in the trusted image allowlist'* ]]; then
    exit 0
  fi

  if [[ $response != *'failed calling webhook "vscheduler-v1.kb.io"'* ]]; then
    printf 'Unexpected admission error\n' >&2
    exit 1
  fi
  case "$response" in
    *'context deadline exceeded'*|*'i/o timeout'*|*'Client.Timeout exceeded'*|\
    *'connection refused'*|*'connection reset by peer'*|*'no endpoints available for service'*|*'EOF'*)
      ;;
    *)
      printf 'Unexpected webhook error\n' >&2
      exit 1
      ;;
  esac

  remaining=$((deadline - SECONDS))
  if (( remaining > 0 )); then
    printf 'Webhook unavailable after rollback; retrying\n' >&2
    sleep "$((remaining < 2 ? remaining : 2))"
  fi
done

printf 'Timed out waiting for the webhook to reject the untrusted image\n' >&2
exit 1
