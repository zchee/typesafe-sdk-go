#!/bin/sh
# Runs one W6.5 design allocation measurement under the shared bench lock.
#
# Usage: allocrun.sh HOST OUT LOCK TREE PKG COMMIT TEST_ARGS...
#   HOST    (M) or (L), written into the header.
#   OUT     the output file (overwritten).
#   LOCK    the lock file shared with the other lanes; FLOCK names flock(1)
#           (macOS: /opt/homebrew/opt/util-linux/bin/flock).
#   TREE    source tree; PKG the package directory inside it (. or
#           internal/alloctest).
#   COMMIT  the commit the tree is, for the header.
# The test binary is built with `go test -c` before the lock is taken and run
# from its package directory. The lock is probed with `flock -n` first; the
# run starts only when the probe succeeds and the 1-minute load is under
# MAXLOAD (default 12), retrying every 60 s. The header records the date,
# the host, go version, ToolTags, GOEXPERIMENT and the load, taken inside the
# lock in the same shell as the measurement; the footer the exit status, the
# date and the load again.
set -eu
host=$1 out=$2 lock=$3 tree=$4 pkg=$5 commit=$6
shift 6
bin=$(mktemp -d "${TMPDIR:-/tmp}/allocrun.XXXXXX")
trap 'rm -rf "$bin"' EXIT
(cd "$tree" && go test -c -o "$bin/pkg.test" "./$pkg")
flockbin=${FLOCK:-flock}
maxload=${MAXLOAD:-12}
load1() { uptime | sed -E 's/.*load averages?: *([0-9.]+).*/\1/'; }
while :; do
	if "$flockbin" -n "$lock" true && awk "BEGIN { exit !($(load1) < $maxload) }"; then
		exec 9>"$lock"
		if "$flockbin" -n 9; then
			break
		fi
		exec 9>&-
	fi
	sleep 60
done
{
	echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host lock=$lock GOEXPERIMENT=${GOEXPERIMENT:-} commit=$commit pkg=$pkg"
	echo "# args: $*"
	echo "# load before: $(uptime)"
	go version
	go list -f '{{context.ToolTags}}' runtime
} >"$out"
status=0
(cd "$tree/$pkg" && "$bin/pkg.test" "$@") >>"$out" 2>&1 || status=$?
{
	echo "# exit $status at $(date '+%Y-%m-%d %H:%M:%S %Z')"
	echo "# load after: $(uptime)"
} >>"$out"
exit "$status"
