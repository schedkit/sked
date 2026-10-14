#!/usr/bin/env bash
set -euo pipefail

HELM=${HELM:-helm}
workdir=$(mktemp -d)
cluster="sked-helm-${workdir##*.}"
cluster=${cluster,,}
export KUBECONFIG="$workdir/kubeconfig"

cleanup() {
  result=$?
  if (( result != 0 )); then
    kubectl get pods -A -o wide || true
    kubectl get events -n operators --sort-by=.lastTimestamp || true
    kubectl logs -n operators -l app.kubernetes.io/instance=sked --all-containers=true || true
  fi
  kind delete cluster --name "$cluster"
  rm -rf "$workdir"
  exit "$result"
}
trap cleanup EXIT

kind create cluster --name "$cluster" --kubeconfig "$KUBECONFIG" --wait 2m
make docker-build IMG=sked-helm-test:local
kind load docker-image sked-helm-test:local --name "$cluster"

"$HELM" install cert-manager oci://quay.io/jetstack/charts/cert-manager \
  --version v1.21.2 --namespace cert-manager --create-namespace \
  --set crds.enabled=true --wait --timeout 5m

"$HELM" install sked ./charts/sked --namespace operators --create-namespace \
  --set image.repository=sked-helm-test,image.tag=local,image.pullPolicy=Never \
  --wait --timeout 5m
kubectl wait --for=condition=Ready certificate/sked-serving-cert -n operators --timeout=2m

kubectl apply -f ./charts/sked/crds/
"$HELM" upgrade sked ./charts/sked --namespace operators \
  --set image.repository=sked-helm-test,image.tag=local,image.pullPolicy=Never \
  --set replicaCount=2,controller.reconcileTimeout=30s --wait --timeout 5m
[[ $(kubectl get deployment sked-controller-manager -n operators -o jsonpath='{.spec.replicas}') == 2 ]]

"$HELM" rollback sked 1 --namespace operators --wait --timeout 5m
[[ $(kubectl get deployment sked-controller-manager -n operators -o jsonpath='{.spec.replicas}') == 1 ]]

printf '%s\n' \
  'apiVersion: sked.schedkit.io/v1' \
  'kind: Scheduler' \
  'metadata:' \
  '  name: rejected-image' \
  'spec:' \
  '  sched: registry.example/untrusted:latest' > "$workdir/rejected.yaml"
bash hack/check-helm-admission.sh "$workdir/rejected.yaml"

"$HELM" uninstall sked --namespace operators --wait --timeout 5m
kubectl get crd schedulers.sked.schedkit.io
[[ -z $(kubectl get deployment -n operators -l app.kubernetes.io/instance=sked -o name) ]]
[[ -z $(kubectl get validatingwebhookconfiguration sked-webhook --ignore-not-found -o name) ]]
