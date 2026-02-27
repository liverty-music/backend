#!/usr/bin/env bash
# check-migration-drift.sh — Verify schema.sql ↔ migration consistency.
#
# Performs three fast, offline checks (no Docker/DB required):
#   1. kustomization.yaml lists every .sql file in migrations/
#   2. If migrations/ changed in git, schema.sql must also be changed
#   3. atlas migrate validate (hash integrity) — skipped if atlas not available
#
# Exit codes:
#   0  all checks passed
#   1  drift detected (details on stderr)
#
# Usage:
#   scripts/check-migration-drift.sh            # check staged + unstaged
#   scripts/check-migration-drift.sh --staged    # only staged changes (pre-commit)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MIGRATIONS_DIR="$REPO_ROOT/k8s/atlas/base/migrations"
KUSTOMIZATION="$REPO_ROOT/k8s/atlas/base/kustomization.yaml"
MODE="${1:-}"
errors=0

# ── Check 1: kustomization.yaml ↔ migrations/ sync ──────────────────
check_kustomization() {
  local missing=()

  for f in "$MIGRATIONS_DIR"/*.sql; do
    [ -f "$f" ] || continue
    local basename
    basename="$(basename "$f")"
    if ! grep -qF "migrations/$basename" "$KUSTOMIZATION"; then
      missing+=("$basename")
    fi
  done

  if [ ${#missing[@]} -gt 0 ]; then
    echo "FAIL: Migration files missing from kustomization.yaml:" >&2
    for m in "${missing[@]}"; do
      echo "  - $m" >&2
    done
    echo "  Add them to k8s/atlas/base/kustomization.yaml configMapGenerator.files" >&2
    errors=$((errors + 1))
  fi
}

# ── Check 2: migration changed → schema.sql must also change ────────
check_schema_in_sync() {
  local diff_cmd="git diff --name-only"
  if [ "$MODE" = "--staged" ]; then
    diff_cmd="git diff --cached --name-only"
  else
    # Check both staged and unstaged
    diff_cmd="git diff --name-only HEAD"
  fi

  local migration_changed=false
  local schema_changed=false

  while IFS= read -r file; do
    case "$file" in
      k8s/atlas/base/migrations/*.sql) migration_changed=true ;;
      internal/infrastructure/database/rdb/schema/schema.sql) schema_changed=true ;;
    esac
  done < <(cd "$REPO_ROOT" && $diff_cmd 2>/dev/null || true)

  if $migration_changed && ! $schema_changed; then
    echo "FAIL: Migration files changed but schema.sql was not updated." >&2
    echo "  Atlas best practice: edit schema.sql (desired state) FIRST," >&2
    echo "  then run 'atlas migrate diff --env local <name>' to generate migrations." >&2
    echo "  If this is a data-only migration, update schema.sql to match the final state." >&2
    errors=$((errors + 1))
  fi
}

# ── Check 3: atlas migrate validate (hash integrity) ─────────────────
check_atlas_hash() {
  if ! command -v atlas &>/dev/null; then
    return 0
  fi

  local output
  if ! output=$(cd "$REPO_ROOT" && atlas migrate validate --env local 2>&1); then
    # Skip if the failure is due to Docker/DB not being available
    if echo "$output" | grep -qE "timeout|connect:|connection refused|no route to host"; then
      echo "SKIP: atlas migrate validate (dev database not available)" >&2
      return 0
    fi
    echo "FAIL: atlas migrate validate failed:" >&2
    echo "$output" >&2
    errors=$((errors + 1))
  fi
}

# ── Run all checks ───────────────────────────────────────────────────
check_kustomization
check_schema_in_sync
check_atlas_hash

if [ "$errors" -gt 0 ]; then
  echo "" >&2
  echo "Migration drift detected ($errors issue(s)). Use /db-migration to fix." >&2
  exit 1
fi

echo "OK: No migration drift detected."
exit 0
