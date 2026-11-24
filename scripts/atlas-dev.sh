#!/usr/bin/env bash
# atlas-dev.sh — run a command with a fresh Atlas dev database.
#
#   scripts/atlas-dev.sh atlas migrate diff --env local <name>
#   scripts/atlas-dev.sh atlas migrate validate --env local
#
# Atlas computes a migration by replaying the migration directory into a
# throwaway "dev database". Production applies migrations with search_path=app,
# so the dev database must be scoped to schema app: then Atlas writes
# unqualified names ("homes", not "app"."homes") and a generated file needs no
# hand edit. A schema-scoped dev URL needs the schema to exist before Atlas
# reads it, and Atlas's own docker:// driver cannot create it without
# `atlas login`, so this script starts the container, creates schema app and
# passes the URL to atlas.hcl through ATLAS_DEV_URL.
#
# Every run uses its own container on a port Docker picks, so several sessions
# can run side by side. The container is removed when the command ends. It must
# not be reused: the replay creates schema dbos outside app, which Atlas does
# not clean up, so a second replay in the same container fails.
#
# Exit status: the command's, or 1 when no dev database could be started.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [ $# -eq 0 ]; then
  echo "usage: scripts/atlas-dev.sh <command> [args...]" >&2
  exit 2
fi

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo "atlas-dev: docker is not available" >&2
  exit 1
fi

# The same image as the local test database, so one pin (and one Renovate
# update) covers both.
image="$(awk '$1 == "image:" && $2 ~ /^postgres:/ { print $2; exit }' "$REPO_ROOT/compose.yml")"
if [ -z "$image" ]; then
  echo "atlas-dev: no postgres image found in compose.yml" >&2
  exit 1
fi

name="atlas-dev-$$-$RANDOM"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run -d --rm --name "$name" \
  -e POSTGRES_PASSWORD=atlas -e POSTGRES_DB=dev \
  -p 127.0.0.1::5432 "$image" >/dev/null

# The image starts a temporary server to initialise the database and then
# restarts. The temporary one listens on the socket only, so a connection over
# TCP succeeds only once the final server is up.
ready=false
for _ in $(seq 120); do
  if docker exec "$name" psql -h 127.0.0.1 -U postgres -d dev -qc 'CREATE SCHEMA app;' >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.5
done
if ! $ready; then
  echo "atlas-dev: the dev database did not start in time" >&2
  exit 1
fi

port="$(docker port "$name" 5432/tcp | head -n 1 | sed 's/.*://')"
export ATLAS_DEV_URL="postgres://postgres:atlas@127.0.0.1:${port}/dev?sslmode=disable&search_path=app"

cd "$REPO_ROOT"
"$@"
