#!/usr/bin/env bash
# Keep the two newest published GitHub releases and delete older releases with their tags.
set -Eeuo pipefail

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set to owner/repository}"

command -v gh >/dev/null 2>&1 || {
  echo 'ERROR: GitHub CLI (gh) is required to prune releases' >&2
  exit 127
}
command -v jq >/dev/null 2>&1 || {
  echo 'ERROR: jq is required to process paginated GitHub releases' >&2
  exit 127
}

# Fetch every page, then sort published releases explicitly by publication
# time rather than relying on the API's page order. The API can include drafts
# for users with push access; drafts are not published releases and must not
# displace the two newest published releases from retention. gh api's --slurp
# returns one array containing all pages; process it with standalone jq because
# gh api does not allow --slurp and --jq together.
tags_to_delete="$(
  gh api --paginate --slurp \
    "repos/${GITHUB_REPOSITORY}/releases?per_page=100" \
    | jq -r 'add | map(select(.draft == false)) | sort_by(.published_at) | reverse | .[2:][].tag_name'
)"

while IFS= read -r tag; do
  [[ -n "$tag" ]] || continue
  # Match the repository used for listing; do not depend on gh's local
  # repository inference (which may point at a different checkout remote).
  gh release delete "$tag" --repo "$GITHUB_REPOSITORY" --yes --cleanup-tag
done <<< "$tags_to_delete"
