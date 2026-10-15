#!/usr/bin/env bash
set -euo pipefail

package=${1:?Usage: publish-helm-repo.sh PACKAGE [GIT_REMOTE_URL]}
remote=${2:-$(git remote get-url origin)}
helm=${HELM:-helm}
package=$(cd "$(dirname "$package")" && pwd)/$(basename "$package")
archive=$(basename "$package")
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

for attempt in {1..5}; do
  site="$work/$attempt"
  git init --quiet --initial-branch=gh-pages "$site"
  git -C "$site" remote add origin "$remote"
  refs=$(git -C "$site" ls-remote --heads origin refs/heads/gh-pages)
  if [[ -n $refs ]]; then
    git -C "$site" fetch --quiet --depth=1 origin refs/heads/gh-pages
    git -C "$site" checkout --quiet -B gh-pages FETCH_HEAD
  fi
  git -C "$site" config user.name 'github-actions[bot]'
  git -C "$site" config user.email '41898282+github-actions[bot]@users.noreply.github.com'

  if [[ -f "$site/$archive" ]] && ! cmp -s "$package" "$site/$archive"; then
    echo "Refusing to overwrite an existing chart version: $archive" >&2
    exit 1
  fi
  cp "$package" "$site/$archive"
  touch "$site/.nojekyll"
  "$helm" repo index "$site"
  git -C "$site" add -- .nojekyll index.yaml "$archive"
  if git -C "$site" diff --cached --quiet; then
    exit 0
  fi
  git -C "$site" commit --quiet -m "Publish $archive"
  if git -C "$site" push origin HEAD:refs/heads/gh-pages; then
    exit 0
  fi
  sleep "$attempt"
done

echo 'Could not update gh-pages after five attempts' >&2
exit 1
