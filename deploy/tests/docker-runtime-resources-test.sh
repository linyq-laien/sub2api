#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

fail() {
  printf 'docker runtime resources test failed: %s\n' "$1" >&2
  exit 1
}

assert_line() {
  file=$1
  line=$2
  grep -Fqx "$line" "$file" || fail "$file is missing: $line"
}

test -s backend/resources/model-pricing/model_prices_and_context_window.json || \
  fail 'fallback pricing data is missing or empty'

assert_line Dockerfile.goreleaser 'COPY --chown=sub2api:sub2api backend/resources /app/resources'
assert_line deploy/Dockerfile 'COPY --from=backend-builder --chown=sub2api:sub2api /app/backend/resources /app/resources'
# Fork releases build Docker contexts through release_matrix.py; GoReleaser
# intentionally has no direct image publication configuration.
assert_line .goreleaser.yaml 'dockers: []'
assert_line .goreleaser.simple.yaml 'dockers: []'
assert_line .github/release-tools/release_matrix.py "        shutil.copytree('backend/resources', dest / 'backend/resources', dirs_exist_ok=True)"

printf 'docker runtime resources test passed\n'
