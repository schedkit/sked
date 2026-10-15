#!/usr/bin/env bash
set -euo pipefail

namespace=${1:?Usage: check-manager-ha.sh NAMESPACE DEPLOYMENT SELECTOR}
deployment=${2:?Usage: check-manager-ha.sh NAMESPACE DEPLOYMENT SELECTOR}
selector=${3:?Usage: check-manager-ha.sh NAMESPACE DEPLOYMENT SELECTOR}
lease=b4c95cb3.schedkit.io

kubectl wait -n "$namespace" "deployment/$deployment" --for=jsonpath='{.status.readyReplicas}'=2 --timeout=2m
kubectl wait -n "$namespace" "pdb/$deployment" --for=jsonpath='{.status.disruptionsAllowed}'=1 --timeout=2m
[[ $(kubectl get pdb "$deployment" -n "$namespace" -o jsonpath='{.spec.minAvailable}') == 1 ]]

mapfile -t nodes < <(kubectl get pods -n "$namespace" -l "$selector" -o jsonpath='{range .items[*]}{.spec.nodeName}{"\n"}{end}' | sort -u)
mapfile -t pods < <(kubectl get pods -n "$namespace" -l "$selector" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
[[ ${#nodes[@]} == 2 && ${#pods[@]} == 2 ]]

kubectl wait -n "$namespace" "lease/$lease" --for=jsonpath='{.spec.holderIdentity}' --timeout=1m
holder=$(kubectl get lease "$lease" -n "$namespace" -o jsonpath='{.spec.holderIdentity}')
leader=${holder%_*}
[[ "$leader" == "${pods[0]}" || "$leader" == "${pods[1]}" ]]
survivor=${pods[0]}
if [[ "$survivor" == "$leader" ]]; then
  survivor=${pods[1]}
fi

cleanup() {
  result=$?
  kubectl uncordon "${nodes[@]}" || true
  exit "$result"
}
trap cleanup EXIT
kubectl cordon "${nodes[@]}"

evict() {
  printf '{"apiVersion":"policy/v1","kind":"Eviction","metadata":{"name":"%s","namespace":"%s"}}' "$1" "$namespace" |
    kubectl create --raw "/api/v1/namespaces/$namespace/pods/$1/eviction" -f -
}

evict "$leader"
kubectl wait -n "$namespace" "pdb/$deployment" --for=jsonpath='{.status.currentHealthy}'=1 --timeout=1m
kubectl wait -n "$namespace" "pdb/$deployment" --for=jsonpath='{.status.disruptionsAllowed}'=0 --timeout=1m
if response=$(evict "$survivor" 2>&1); then
  echo "The PDB allowed eviction of the last healthy manager" >&2
  exit 1
fi
printf '%s\n' "$response"
grep -qi 'disruption budget' <<< "$response"

for ((attempt = 0; attempt < 60; attempt++)); do
  holder=$(kubectl get lease "$lease" -n "$namespace" -o jsonpath='{.spec.holderIdentity}')
  if [[ "$holder" == "${survivor}_"* ]]; then
    break
  fi
  sleep 1
done
[[ "$holder" == "${survivor}_"* ]] || { echo "The surviving manager did not acquire leadership" >&2; exit 1; }
kubectl wait -n "$namespace" "pod/$survivor" --for=condition=Ready --timeout=1m

kubectl uncordon "${nodes[@]}"
trap - EXIT
kubectl wait -n "$namespace" "deployment/$deployment" --for=jsonpath='{.status.readyReplicas}'=2 --timeout=2m
kubectl wait -n "$namespace" "pdb/$deployment" --for=jsonpath='{.status.disruptionsAllowed}'=1 --timeout=2m
