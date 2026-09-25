#!/bin/sh
# Runs one W0.4 spike measurement and keeps its raw output.
#
# Usage: run.sh HOST OUT LOCK NAME GO_TEST_ARGS...
#   HOST  (M) or (L), written into the header.
#   OUT   directory for NAME.txt.
#   LOCK  lock file shared with the other spike lanes, or "none" for
#         connection-count and classification runs; the lock is taken with
#         flock(1) on a file descriptor (FLOCK overrides the binary).
#   NAME  output file name without .txt.
# MAXLOAD, when set with a lock, is the highest 1-minute load average at
# which the run may start: above it the lock is released, the runner waits
# 60 s and tries again, up to 5 times, and then runs anyway (the ledger
# marks such a row noisy). The header records date, load average, the
# attempts, the tree, go version and ToolTags, all taken inside the lock in
# the same shell as the measurement; the footer records the exit status,
# date and load again.
set -u
host=$1 out=$2 lock=$3 name=$4
shift 4
# The tree measured: TREE when set (a copy without .git), otherwise the
# worktree's HEAD, with +changes when the worktree differs from it.
tree=${TREE:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}
if [ -z "${TREE:-}" ] && [ -n "$(git status --porcelain 2>/dev/null)" ]; then
	tree="$tree+changes"
fi
mkdir -p "$out"
{
	tries=0
	if [ "$lock" != none ]; then
		while :; do
			exec 9>"$lock"
			"${FLOCK:-flock}" 9
			load=$(uptime | sed -E 's/.*load averages?: *([0-9.]+).*/\1/')
			if [ -z "${MAXLOAD:-}" ] || [ "$tries" -ge 5 ] || awk "BEGIN { exit !($load <= $MAXLOAD) }"; then
				break
			fi
			"${FLOCK:-flock}" -u 9
			exec 9>&-
			tries=$((tries + 1))
			sleep 60
		done
	fi
	echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host tree=$tree lock=$lock GOEXPERIMENT=${GOEXPERIMENT:-} go test $*"
	echo "# load before: $(uptime) (MAXLOAD=${MAXLOAD:-none}, waited $tries x 60 s)"
	go version
	go list -f '{{context.ToolTags}}' runtime
	go test "$@" 2>&1
	status=$?
	echo "# exit $status at $(date '+%Y-%m-%d %H:%M:%S %Z')"
	echo "# load after: $(uptime)"
} >"$out/$name.txt"
