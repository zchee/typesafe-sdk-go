#!/bin/sh
# Runs go test under CPU contention and keeps the raw output (W2.2).
#
# Usage: contend.sh HOST OUT LOCK NAME LOOPS GO_TEST_ARGS...
#   HOST   (M) or (L), written into the header.
#   OUT    directory for NAME.txt.
#   LOCK   the bench lock shared with the other lanes: the loops load the
#          whole host, so they run inside it and no timing run overlaps them
#          (FLOCK overrides the flock(1) binary).
#   NAME   output file name without .txt.
#   LOOPS  number of `nice -n 19 yes` loops (one per core).
# The header and footer record date, the tree, load average, go version and
# ToolTags, taken inside the lock in the same shell as the run; the footer
# also records that no yes loop survived (pgrep -x yes prints nothing).
set -u
host=$1 out=$2 lock=$3 name=$4 loops=$5
shift 5
# The tree measured: TREE when set (a copy without .git), otherwise the
# worktree's HEAD, with +changes when the worktree differs from it.
tree=${TREE:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}
if [ -z "${TREE:-}" ] && [ -n "$(git status --porcelain 2>/dev/null)" ]; then
	tree="$tree+changes"
fi
mkdir -p "$out"
{
	exec 9>"$lock"
	"${FLOCK:-flock}" 9
	echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host tree=$tree lock=$lock loops=$loops GOEXPERIMENT=${GOEXPERIMENT:-} go test $*"
	echo "# load before: $(uptime)"
	go version
	go list -f '{{context.ToolTags}}' runtime
	pids=""
	i=0
	while [ "$i" -lt "$loops" ]; do
		nice -n 19 yes >/dev/null &
		pids="$pids $!"
		i=$((i + 1))
	done
	sleep 5
	echo "# load with $loops loops: $(uptime)"
	go test "$@" 2>&1
	status=$?
	echo "# load at the end: $(uptime)"
	kill $pids
	wait $pids 2>/dev/null
	echo "# exit $status at $(date '+%Y-%m-%d %H:%M:%S %Z'); yes loops left: [$(pgrep -x yes | tr '\n' ' ')]"
} >"$out/$name.txt"
