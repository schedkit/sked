#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

tag=${1:?Usage: package-helm.sh TAG OUTPUT_DIRECTORY}
destination=${2:?Usage: package-helm.sh TAG OUTPUT_DIRECTORY}
helm=${HELM:-helm}
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

number='(0|[1-9][0-9]*)'
identifier='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
pattern="^v?${number}\\.${number}\\.${number}(-${identifier}(\\.${identifier})*)?$"
if [[ ! $tag =~ $pattern || ${#tag} -gt 128 ]]; then
  echo "Expected a release tag like v1.2.3 or v1.2.3-rc.1 (optional v, no build metadata): $tag" >&2
  exit 1
fi

mkdir -p "$destination"
"$helm" lint --strict "$root/charts/sked"
"$helm" package "$root/charts/sked" --destination "$destination" \
  --version "${tag#v}" --app-version "$tag"
