#!/usr/bin/env bash
# A Claude Code PostToolUse hook: whenever Claude edits go.mod or runs
# `go get`, weigh what changed since HEAD. If a new or changed direct
# dependency is heavy or an inline candidate, exit 2 so Claude sees the
# report and can reconsider (copy the few lines it needs, or pick a lighter
# module).
#
# Needs jq and heft on PATH. Install with: see examples/claude-code/README.md
set -uo pipefail

input=$(cat)
file=$(jq -r '.tool_input.file_path // empty' <<<"$input")
cmd=$(jq -r '.tool_input.command // empty' <<<"$input")

case "$file" in
*/go.mod | go.mod) ;;
*) [[ "$cmd" =~ (^|[[:space:]\;\&])go[[:space:]]+(get|mod[[:space:]]+tidy) ]] || exit 0 ;;
esac

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
git rev-parse --verify --quiet HEAD >/dev/null || exit 0

report=$(heft -base HEAD -format md -fail-on "${HEFT_FAIL_ON:-heavy,inline}" 2>&1)
case $? in
0) exit 0 ;;
1)
	echo "heft: this change adds dependency weight worth a second look." >&2
	echo "$report" >&2
	echo "Use \`heft why <module>\` or \`heft extract <module>\` to see what you use; copying a few functions (keeping the license) may beat a dependency." >&2
	exit 2
	;;
*) exit 0 ;; # doesn't build yet, or not a program: don't block the edit
esac
