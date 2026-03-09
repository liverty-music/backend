#!/usr/bin/env bash
# lint-schema.sh — Enforce database-schema-designer policies on schema.sql.
#
# Checks:
#   1. No SERIAL / BIGSERIAL (use UUIDv7)
#   2. No bare TIMESTAMP (use TIMESTAMPTZ)
#   3. No audit columns (created_at, updated_at, deleted_at)
#   4. No VARCHAR (use TEXT + CHECK constraint)
#   5. COMMENT ON TABLE coverage
#   6. COMMENT ON COLUMN coverage
#
# Exit codes:
#   0  all checks passed
#   1  policy violation detected

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCHEMA="$REPO_ROOT/internal/infrastructure/database/rdb/schema/schema.sql"
errors=0

if [ ! -f "$SCHEMA" ]; then
  echo "FAIL: schema.sql not found at $SCHEMA" >&2
  exit 1
fi

# ── Check 1: SERIAL / BIGSERIAL ───────────────────────────────────────
check_serial() {
  local hits
  hits=$(grep -nE '\bSERIAL\b|\bBIGSERIAL\b' "$SCHEMA" || true)
  if [ -n "$hits" ]; then
    echo "FAIL: SERIAL/BIGSERIAL detected (use UUIDv7 instead):" >&2
    echo "$hits" >&2
    errors=$((errors + 1))
  fi
}

# ── Check 2: bare TIMESTAMP (not TIMESTAMPTZ) ─────────────────────────
check_timestamp() {
  local hits
  hits=$(grep -nE '\bTIMESTAMP\b' "$SCHEMA" || true)
  if [ -n "$hits" ]; then
    echo "FAIL: bare TIMESTAMP detected (use TIMESTAMPTZ instead):" >&2
    echo "$hits" >&2
    errors=$((errors + 1))
  fi
}

# ── Check 3: audit columns ────────────────────────────────────────────
check_audit_columns() {
  local hits
  hits=$(grep -nE '\b(created_at|updated_at|deleted_at)\b' "$SCHEMA" || true)
  if [ -n "$hits" ]; then
    echo "FAIL: Prohibited audit columns detected:" >&2
    echo "$hits" >&2
    errors=$((errors + 1))
  fi
}

# ── Check 4: VARCHAR ──────────────────────────────────────────────────
check_varchar() {
  local hits
  hits=$(grep -nE '\bVARCHAR\b' "$SCHEMA" || true)
  if [ -n "$hits" ]; then
    echo "FAIL: VARCHAR detected (use TEXT + CHECK constraint instead):" >&2
    echo "$hits" >&2
    errors=$((errors + 1))
  fi
}

# ── Check 5: COMMENT ON TABLE coverage ────────────────────────────────
check_table_comments() {
  local tables
  tables=$(grep -oP 'CREATE TABLE IF NOT EXISTS \K\w+' "$SCHEMA" || true)

  for table in $tables; do
    if ! grep -qP "COMMENT ON TABLE ${table}\b" "$SCHEMA"; then
      echo "FAIL: Missing COMMENT ON TABLE for '$table'" >&2
      errors=$((errors + 1))
    fi
  done
}

# ── Check 6: COMMENT ON COLUMN coverage ──────────────────────────────
check_column_comments() {
  local current_table=""
  local col_count=0
  local in_create=false

  while IFS= read -r line; do
    # Detect CREATE TABLE
    if [[ "$line" =~ CREATE\ TABLE\ IF\ NOT\ EXISTS\ ([a-zA-Z_]+) ]]; then
      current_table="${BASH_REMATCH[1]}"
      col_count=0
      in_create=true
      continue
    fi

    # Inside CREATE TABLE block: count column definitions
    if $in_create; then
      # End of CREATE TABLE
      if [[ "$line" =~ ^\) ]]; then
        in_create=false

        # Count COMMENT ON COLUMN for this table
        local comment_count
        comment_count=$(grep -cP "COMMENT ON COLUMN ${current_table}\." "$SCHEMA" || true)

        if [ "$col_count" -ne "$comment_count" ]; then
          echo "FAIL: COMMENT ON COLUMN mismatch for '$current_table': $col_count columns, $comment_count comments" >&2
          errors=$((errors + 1))
        fi
        continue
      fi

      # Skip blank lines, comments, constraints, and PRIMARY KEY lines
      if [[ "$line" =~ ^[[:space:]]*$ ]] || [[ "$line" =~ ^[[:space:]]*-- ]] || [[ "$line" =~ ^[[:space:]]*CONSTRAINT ]] || [[ "$line" =~ ^[[:space:]]*PRIMARY[[:space:]]KEY ]]; then
        continue
      fi

      # Column definition: indented line starting with a word (column name, may contain digits)
      if [[ "$line" =~ ^[[:space:]]+[a-zA-Z_][a-zA-Z0-9_]*[[:space:]]+ ]]; then
        col_count=$((col_count + 1))
      fi
    fi
  done < "$SCHEMA"
}

# ── Run all checks ────────────────────────────────────────────────────
check_serial
check_timestamp
check_audit_columns
check_varchar
check_table_comments
check_column_comments

if [ "$errors" -gt 0 ]; then
  echo "" >&2
  echo "Schema lint failed ($errors issue(s)). See database-schema-designer skill for policies." >&2
  exit 1
fi

echo "OK: Schema lint passed."
exit 0
