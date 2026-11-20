#!/usr/bin/env bash
# Offline regression tests for scripts/prune-releases.sh; requires jq, as the helper does.
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fakebin="$work/bin"
mkdir -p "$fakebin"

cat >"$fakebin/gh" <<'GH'
#!/usr/bin/env bash
set -Eeuo pipefail

case "${1:-}" in
  api)
    shift
    [[ "${1:-}" == --paginate ]] || { echo 'missing --paginate' >&2; exit 90; }
    shift
    [[ "${1:-}" == --slurp ]] || { echo 'missing --slurp' >&2; exit 91; }
    shift
    [[ "${1:-}" == "repos/${GITHUB_REPOSITORY}/releases?per_page=100" && $# -eq 1 ]] || {
      echo "unexpected releases API arguments (gh api --slurp cannot use --jq): $*" >&2
      exit 92
    }
    [[ "${GH_FAIL_API:-0}" != 1 ]] || { echo 'simulated API failure' >&2; exit 94; }
    cat "$GH_RELEASE_FIXTURE"
    ;;
  release)
    [[ "${2:-}" == delete && $# -eq 7 ]] || { echo "unexpected gh release args: $*" >&2; exit 95; }
    [[ "${4:-}" == --repo && "${5:-}" == "$GITHUB_REPOSITORY" ]] || {
      echo "release deletion must target GITHUB_REPOSITORY explicitly: $*" >&2
      exit 96
    }
    [[ "${6:-}" == --yes && "${7:-}" == --cleanup-tag ]] || {
      echo "release deletion must clean up tags: $*" >&2
      exit 97
    }
    [[ "${GH_FAIL_DELETE_TAG:-}" != "${3:-}" ]] || {
      echo "simulated deletion failure for ${3}" >&2
      exit 98
    }
    printf '%s\n' "$3" >>"$GH_DELETED_TAGS"
    ;;
  *)
    echo "unexpected gh invocation: $*" >&2
    exit 99
    ;;
esac
GH
chmod +x "$fakebin/gh"

export PATH="$fakebin:$PATH"
export GITHUB_REPOSITORY='example/filtrite'
export GH_DELETED_TAGS="$work/deleted-tags.txt"
export GH_RELEASE_FIXTURE="$work/releases.json"
: >"$GH_DELETED_TAGS"

# Pages are deliberately out of order. Retention follows publication time,
# not creation time, and unpublished drafts must not count toward the two
# releases to keep. `published-late` was created first but published second.
cat >"$GH_RELEASE_FIXTURE" <<'JSON'
[
  [
    {"tag_name":"middle-2026-02","created_at":"2026-02-10T00:00:00Z","published_at":"2026-02-10T00:00:00Z","draft":false},
    {"tag_name":"draft-2026-03","created_at":"2026-03-20T00:00:00Z","published_at":null,"draft":true},
    {"tag_name":"published-late-2026-03","created_at":"2026-01-01T00:00:00Z","published_at":"2026-03-01T00:00:00Z","draft":false}
  ],
  [
    {"tag_name":"oldest-2026-01","created_at":"2026-01-10T00:00:00Z","published_at":"2026-01-10T00:00:00Z","draft":false},
    {"tag_name":"newest-2026-03","created_at":"2026-03-10T00:00:00Z","published_at":"2026-03-10T00:00:00Z","draft":false}
  ]
]
JSON
bash "$root/scripts/prune-releases.sh"
expected=$'middle-2026-02\noldest-2026-01'
actual="$(cat "$GH_DELETED_TAGS")"
[[ "$actual" == "$expected" ]] || {
  printf 'FAIL: expected deletions %q, got %q\n' "$expected" "$actual" >&2
  exit 1
}

# Empty repositories must be a successful no-op.
: >"$GH_DELETED_TAGS"
printf '[[], []]\n' >"$GH_RELEASE_FIXTURE"
bash "$root/scripts/prune-releases.sh"
[[ ! -s "$GH_DELETED_TAGS" ]] || {
  echo 'FAIL: empty release pages caused a deletion' >&2
  exit 1
}

# Draft-only repositories must also be a no-op: draft entries are not
# published releases and must never be selected for deletion.
printf '[[{\"tag_name\":\"draft-only\",\"created_at\":\"2026-03-20T00:00:00Z\",\"published_at\":null,\"draft\":true}]]' >"$GH_RELEASE_FIXTURE"
bash "$root/scripts/prune-releases.sh"
[[ ! -s "$GH_DELETED_TAGS" ]] || {
  echo 'FAIL: draft-only release caused a deletion' >&2
  exit 1
}

# API failures must fail closed: do not continue with an empty deletion list.
if GH_FAIL_API=1 bash "$root/scripts/prune-releases.sh" >/dev/null 2>&1; then
  echo 'FAIL: release API failure was ignored' >&2
  exit 1
fi
[[ ! -s "$GH_DELETED_TAGS" ]] || {
  echo 'FAIL: a release was deleted after an API failure' >&2
  exit 1
}

# A deletion failure must also fail the workflow step.
cat >"$GH_RELEASE_FIXTURE" <<'JSON'
[[{"tag_name":"newest","created_at":"2026-03-10T00:00:00Z","published_at":"2026-03-10T00:00:00Z","draft":false},{"tag_name":"second","created_at":"2026-02-10T00:00:00Z","published_at":"2026-02-10T00:00:00Z","draft":false},{"tag_name":"oldest","created_at":"2025-12-10T00:00:00Z","published_at":"2025-12-10T00:00:00Z","draft":false}]]
JSON
if GH_FAIL_DELETE_TAG=oldest bash "$root/scripts/prune-releases.sh" >/dev/null 2>&1; then
  echo 'FAIL: release deletion failure was ignored' >&2
  exit 1
fi

printf 'OK: release retention regression tests passed\n'
