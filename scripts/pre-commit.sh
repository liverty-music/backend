#!/usr/bin/env bash
# The commit hook's checks (`make pre-commit`): the checks of `make lint` and
# `make test`, run only on the Go packages the commit changes. A package is
# changed when a file in its directory differs from HEAD (staged or not) or is
# untracked; a changed non-Go file (testdata, an embedded template) counts for
# the nearest directory above it that holds Go files. A change to go.mod,
# go.sum or .golangci.yml can affect any package, so it checks all of them.
#
# Packages that import a changed package are not checked here: `make pre-push`
# runs the whole `make check` before the push, and CI checks everything.
#
# Expects GOLANGCI_LINT (the pinned binary) and the environment the Makefile
# exports; run it through `make pre-commit`.
set -euo pipefail

cd "$(dirname "$0")/.."
: "${GOLANGCI_LINT:?run through make pre-commit}"

# --no-renames lists both paths of a rename, so the package a file left is
# checked too.
changed=$( { git diff --name-only --no-renames HEAD; git ls-files --others --exclude-standard; } | sort -u)
[[ -n "$changed" ]] || { echo "pre-commit: nothing changed"; exit 0; }

has_go_files() { compgen -G "$1/*.go" >/dev/null; }

if grep -qxE 'go\.mod|go\.sum|\.golangci\.yml' <<<"$changed"; then
  pkgs=(./...)
  gofiles=(.)
else
  declare -A dirs=()
  gofiles=()
  while IFS= read -r file; do
    if [[ "$file" == *.go && -f "$file" ]]; then
      gofiles+=("$file")
    fi
    dir=$(dirname "$file")
    # A deleted directory has no package left to check.
    while [[ "$dir" != "." ]] && ! { [[ -d "$dir" ]] && has_go_files "$dir"; }; do
      dir=$(dirname "$dir")
    done
    # The root holds no Go package; a file outside every package (docs,
    # k8s manifests) changes nothing these checks look at.
    [[ "$dir" == "." ]] || dirs["./$dir"]=1
  done <<<"$changed"
  pkgs=("${!dirs[@]}")
fi

if ((${#pkgs[@]} == 0)); then
  echo "pre-commit: no Go package changed"
  exit 0
fi
echo "==> Packages: ${pkgs[*]}"

echo "==> Checking gofmt..."
if ((${#gofiles[@]} > 0)); then
  unformatted=$(gofmt -l "${gofiles[@]}")
  if [[ -n "$unformatted" ]]; then
    echo "$unformatted"
    exit 1
  fi
fi

echo "==> Running go vet..."
go vet -tags=integration "${pkgs[@]}"

echo "==> Running golangci-lint..."
"$GOLANGCI_LINT" run --timeout=3m --build-tags=integration "${pkgs[@]}"

# Start the shared test PostgreSQL only when a test needs it: the database
# tests reach it through internal/testutil.
if go list -test -deps "${pkgs[@]}" | grep -qx 'github.com/liverty-music/backend/internal/testutil'; then
  scripts/test-postgres.sh up
fi

echo "==> Running go test..."
go test "${pkgs[@]}"
