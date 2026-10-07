#!/usr/bin/env bash
# check-schema-sdk-commit.sh — Ensure every BSR-generated liverty-music/schema
# SDK module in go.mod is built from the same schema commit.
#
# The Connect and protocolbuffers SDKs are separate Go modules with separate
# versions (e.g. v1.21.0-20261006123206-e1f8da3822a6.1 and
# v1.36.12-20261006123206-e1f8da3822a6.2). Renovate does not manage them, so
# bumping one without the other silently mixes two schema builds. The
# 12-character segment before the plugin revision is the schema commit; all
# modules must share it.
#
# Exit codes:
#   0  all schema SDK modules reference one commit
#   1  the commits differ, or no schema SDK module was found

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GO_MOD="$REPO_ROOT/go.mod"

mapfile -t entries < <(
	grep -E '^\s*buf\.build/gen/go/liverty-music/schema/' "$GO_MOD" |
		awk '{ print $1, $2 }'
)

if [[ ${#entries[@]} -eq 0 ]]; then
	echo "ERROR: no buf.build/gen/go/liverty-music/schema module found in go.mod" >&2
	exit 1
fi

declare -A commits=()
for entry in "${entries[@]}"; do
	module="${entry%% *}"
	version="${entry##* }"
	# vX.Y.Z-<timestamp>-<commit>.<revision>
	commit="$(sed -E 's/^.*-[0-9]{14}-([0-9a-f]{12})\.[0-9]+$/\1/' <<<"$version")"
	if [[ ! "$commit" =~ ^[0-9a-f]{12}$ ]]; then
		echo "ERROR: cannot read the schema commit from $module $version" >&2
		exit 1
	fi
	commits["$commit"]+="  $module $version"$'\n'
done

if [[ ${#commits[@]} -ne 1 ]]; then
	echo "ERROR: liverty-music/schema SDK modules reference different schema commits:" >&2
	for commit in "${!commits[@]}"; do
		echo "commit $commit:" >&2
		printf '%s' "${commits[$commit]}" >&2
	done
	echo "Upgrade every module to the same schema release." >&2
	exit 1
fi

echo "OK: ${#entries[@]} liverty-music/schema SDK modules on schema commit ${!commits[*]}"
