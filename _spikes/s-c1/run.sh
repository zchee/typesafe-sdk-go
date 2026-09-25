#!/bin/sh
# Runs one W0.5 S-C1 measurement under the shared lock and keeps its raw
# output.
#
# Usage: run.sh HOST OUT LOCK NAME GO_TEST_ARGS...
#   HOST  (M) or (L), written into the header.
#   OUT   directory for NAME.txt.
#   LOCK  lock file shared with the other spike lanes; taken with flock(1)
#         on a file descriptor (FLOCK overrides the binary: macOS has none
#         on PATH, util-linux's is /opt/homebrew/opt/util-linux/bin/flock).
#   NAME  output file name without .txt.
# MAXLOAD, when set, is the highest 1-minute load average at which the run
# may start: above it the lock is released, the runner waits 60 s and tries
# again, up to 5 times, and then runs anyway (the ledger marks such a row
# noisy). The header records the date, the load (uptime, and the kernel's
# own figure: sysctl vm.loadavg on darwin, /proc/loadavg on linux), the
# waits, the base commit, go version and ToolTags, all taken inside the lock
# in the same shell as the measurement; the footer records the exit status,
# the date and the load again.
set -u
host=$1 out=$2 lock=$3 name=$4
shift 4
mkdir -p "$out"
kload() {
	if [ -r /proc/loadavg ]; then
		cat /proc/loadavg
	else
		sysctl -n vm.loadavg
	fi
}
{
	tries=0
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
	echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host lock=$lock GOEXPERIMENT=${GOEXPERIMENT:-} go test $*"
	echo "# load before: $(uptime) | kernel: $(kload) (MAXLOAD=${MAXLOAD:-none}, waited $tries x 60 s)"
	echo "# base: ${BASE:-unknown}"
	go version
	go list -f '{{context.ToolTags}}' runtime
	go test "$@" 2>&1
	status=$?
	echo "# exit $status at $(date '+%Y-%m-%d %H:%M:%S %Z')"
	echo "# load after: $(uptime) | kernel: $(kload)"
} >"$out/$name.txt"
