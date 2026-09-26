#!/bin/sh
# Runs go test under CPU contention and keeps the raw output (W2.2).
#
# Usage: contend.sh HOST OUT LOCK NAME LOOPS GO_TEST_ARGS...
#   HOST   (M) or (L), written into the header.
#   OUT    directory for NAME.txt.
#   LOCK   the bench lock shared with the other lanes: the loops load the
#          whole host, so they run inside it and no timing run overlaps them.
#          FLOCK names the flock(1) binary; the default is util-linux's, at
#          Homebrew's keg-only path on macOS, where no flock is on PATH, and
#          flock from PATH elsewhere. The script stops, running nothing, when
#          the lock cannot be taken or a second flock -n on LOCK still
#          succeeds (ruling R17: with no flock on PATH, the old `flock 9`
#          failed, and the loops and the tests ran without the lock).
#   NAME   output file name without .txt.
#   LOOPS  number of `nice -n 19 yes` loops (one per core). They close fd 9,
#          the lock's: a script killed before its own kill leaves them
#          running, and they must not keep the lock for the next lane.
#          go test keeps fd 9, so the lock lasts while it loads the host.
# The header and footer record date, the tree, load average, go version and
# ToolTags, taken inside the lock in the same shell as the run; the footer
# also records that no yes loop survived (pgrep -x yes prints nothing).
set -u
host=$1 out=$2 lock=$3 name=$4 loops=$5
shift 5
if [ -z "${FLOCK:-}" ]; then
	FLOCK=flock
	if [ -x /opt/homebrew/opt/util-linux/bin/flock ]; then
		FLOCK=/opt/homebrew/opt/util-linux/bin/flock
	fi
fi
# The tree measured: TREE when set (a copy without .git), otherwise the
# worktree's HEAD, with +changes when the worktree differs from it.
tree=${TREE:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}
if [ -z "${TREE:-}" ] && [ -n "$(git status --porcelain 2>/dev/null)" ]; then
	tree="$tree+changes"
fi
mkdir -p "$out"
{
	exec 9>"$lock"
	if ! "$FLOCK" 9; then
		echo "contend.sh: cannot lock $lock with $FLOCK" >&2
		exit 1
	fi
	if "$FLOCK" -n "$lock" true; then
		echo "contend.sh: $lock is not locked after $FLOCK 9" >&2
		exit 1
	fi
	echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host tree=$tree lock=$lock held with $FLOCK (a second flock -n failed) loops=$loops GOEXPERIMENT=${GOEXPERIMENT:-} go test $*"
	echo "# load before: $(uptime)"
	go version
	go list -f '{{context.ToolTags}}' runtime
	pids=""
	i=0
	while [ "$i" -lt "$loops" ]; do
		nice -n 19 yes >/dev/null 9>&- &
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
