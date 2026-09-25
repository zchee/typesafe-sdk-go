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
# The header records date, load average, go version and ToolTags, all taken
# inside the lock in the same shell as the measurement; the footer records
# the exit status, date and load again.
set -u
host=$1 out=$2 lock=$3 name=$4
shift 4
mkdir -p "$out"
{
	if [ "$lock" != none ]; then
		exec 9>"$lock"
		"${FLOCK:-flock}" 9
	fi
	echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host lock=$lock GOEXPERIMENT=${GOEXPERIMENT:-} go test $*"
	echo "# load before: $(uptime)"
	go version
	go list -f '{{context.ToolTags}}' runtime
	go test "$@" 2>&1
	status=$?
	echo "# exit $status at $(date '+%Y-%m-%d %H:%M:%S %Z')"
	echo "# load after: $(uptime)"
} >"$out/$name.txt"
