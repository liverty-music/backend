#!/usr/bin/env bash
# check-migration-drift.sh — Verify schema.sql ↔ migration consistency.
#
# Performs four fast, offline checks (no Docker/DB required):
#   1. kustomization.yaml lists every .sql file in migrations/
#   2. If migrations/ changed in git, schema.sql must also be changed
#   3. atlas migrate validate (hash integrity) — skipped if atlas not available
#   4. Out-of-order migration timestamp detection (compares branch files against origin/main)
#
# Exit codes:
#   0  all checks passed
#   1  drift detected (details on stderr)
#
# Usage:
#   scripts/check-migration-drift.sh            # check staged + unstaged
#   scripts/check-migration-drift.sh --staged    # only staged changes (pre-commit)
#   scripts/check-migration-drift.sh --fix       # detect and auto-fix out-of-order migrations

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MIGRATIONS_DIR="$REPO_ROOT/k8s/atlas/base/migrations"
KUSTOMIZATION="$REPO_ROOT/k8s/atlas/base/kustomization.yaml"
MODE="${1:-}"
errors=0

# ── Rebase-state guard ────────────────────────────────────────────────
# Exit early if a git rebase is in progress (incomplete rebase).
if [ -d "$REPO_ROOT/.git/rebase-merge" ] || [ -d "$REPO_ROOT/.git/rebase-apply" ]; then
  echo "SKIP: git rebase in progress, skipping migration checks." >&2
  exit 0
fi

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

# ── Check 4: out-of-order migration timestamps ────────────────────────
# Detects migration files added on this branch whose timestamps are
# earlier than the latest migration already on origin/main.
check_migration_ordering() {
  # Skip if origin/main is not resolvable (e.g., shallow clone, missing remote)
  if ! git -C "$REPO_ROOT" rev-parse --verify origin/main >/dev/null 2>&1; then
    echo "SKIP: origin/main not found, skipping migration ordering check." >&2
    return 0
  fi

  # Find migration files added on this branch (not on origin/main)
  local added_files
  added_files=$(cd "$REPO_ROOT" && git diff --name-only --diff-filter=A origin/main -- k8s/atlas/base/migrations/*.sql 2>/dev/null || true)

  if [ -z "$added_files" ]; then
    return 0
  fi

  # Get the latest migration timestamp on origin/main
  local main_latest=""
  local main_file
  while IFS= read -r main_file; do
    [ -n "$main_file" ] || continue
    local basename
    basename="$(basename "$main_file")"
    local ts="${basename%%_*}"
    if [ -z "$main_latest" ] || [ "$ts" \> "$main_latest" ]; then
      main_latest="$ts"
    fi
  done < <(cd "$REPO_ROOT" && git ls-tree --name-only origin/main -- k8s/atlas/base/migrations/*.sql 2>/dev/null || true)

  if [ -z "$main_latest" ]; then
    return 0
  fi

  # Check each added file's timestamp against main's latest
  local out_of_order=()
  local file
  while IFS= read -r file; do
    [ -n "$file" ] || continue
    local basename
    basename="$(basename "$file")"
    local ts="${basename%%_*}"
    if [ "$ts" \< "$main_latest" ]; then
      out_of_order+=("$basename")
    fi
  done <<< "$added_files"

  if [ ${#out_of_order[@]} -eq 0 ]; then
    return 0
  fi

  if [ "$MODE" = "--fix" ]; then
    fix_migration_ordering
  else
    echo "FAIL: Out-of-order migration timestamps detected:" >&2
    for f in "${out_of_order[@]}"; do
      echo "  - $f (timestamp < main's latest: $main_latest)" >&2
    done
    echo "  Run 'scripts/check-migration-drift.sh --fix' to auto-fix with atlas migrate rebase." >&2
    errors=$((errors + 1))
  fi
}

# ── Fix: atlas migrate rebase loop ────────────────────────────────────
# Iteratively rebases out-of-order migration files, re-scanning after
# each invocation since atlas migrate rebase renames files and rewrites atlas.sum.
fix_migration_ordering() {
  if ! command -v atlas &>/dev/null; then
    echo "FAIL: atlas CLI not found, cannot auto-fix migration ordering." >&2
    errors=$((errors + 1))
    return
  fi

  echo "Fixing out-of-order migration timestamps..." >&2

  while true; do
    # Re-scan for out-of-order files (filenames change after each rebase)
    local added_files
    added_files=$(cd "$REPO_ROOT" && git diff --name-only --diff-filter=A origin/main -- k8s/atlas/base/migrations/*.sql 2>/dev/null || true)

    if [ -z "$added_files" ]; then
      break
    fi

    local main_latest=""
    local main_file
    while IFS= read -r main_file; do
      [ -n "$main_file" ] || continue
      local basename
      basename="$(basename "$main_file")"
      local ts="${basename%%_*}"
      if [ -z "$main_latest" ] || [ "$ts" \> "$main_latest" ]; then
        main_latest="$ts"
      fi
    done < <(cd "$REPO_ROOT" && git ls-tree --name-only origin/main -- k8s/atlas/base/migrations/*.sql 2>/dev/null || true)

    if [ -z "$main_latest" ]; then
      break
    fi

    local found_out_of_order=false
    local target_file=""
    local target_version=""
    local file
    while IFS= read -r file; do
      [ -n "$file" ] || continue
      local basename
      basename="$(basename "$file")"
      local ts="${basename%%_*}"
      if [ "$ts" \< "$main_latest" ]; then
        found_out_of_order=true
        target_file="$basename"
        target_version="$ts"
        break
      fi
    done <<< "$added_files"

    if ! $found_out_of_order; then
      break
    fi

    echo "  Rebasing: $target_file (version $target_version)" >&2
    local rebase_output
    if ! rebase_output=$(cd "$REPO_ROOT" && atlas migrate rebase "$target_version" --env local 2>&1); then
      echo "FAIL: atlas migrate rebase $target_version failed:" >&2
      echo "$rebase_output" >&2
      errors=$((errors + 1))
      return
    fi

    # Extract new filename from the rebase output or find it by diffing
    # atlas migrate rebase renames the file with a new timestamp
    local new_file=""
    local sql_file
    for sql_file in "$MIGRATIONS_DIR"/*.sql; do
      [ -f "$sql_file" ] || continue
      local sql_basename
      sql_basename="$(basename "$sql_file")"
      local name_part="${target_file#*_}"
      if [ "$sql_basename" != "$target_file" ] && [[ "$sql_basename" == *"_$name_part" ]]; then
        new_file="$sql_basename"
        break
      fi
    done

    # Update kustomization.yaml if we found the new filename
    if [ -n "$new_file" ] && [ "$new_file" != "$target_file" ]; then
      echo "  Renamed: $target_file -> $new_file" >&2
      if grep -qF "migrations/$target_file" "$KUSTOMIZATION"; then
        sed -i "s|migrations/$target_file|migrations/$new_file|g" "$KUSTOMIZATION"
        echo "  Updated kustomization.yaml" >&2
      fi
    fi
  done

  echo "Migration ordering fixed." >&2
}

# ── Run all checks ───────────────────────────────────────────────────
check_kustomization
check_schema_in_sync
check_atlas_hash
check_migration_ordering

if [ "$errors" -gt 0 ]; then
  echo "" >&2
  echo "Migration drift detected ($errors issue(s)). Use /db-migration to fix." >&2
  exit 1
fi

echo "OK: No migration drift detected."
exit 0
