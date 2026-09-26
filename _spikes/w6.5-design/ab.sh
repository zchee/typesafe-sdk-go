#!/bin/sh
# Runs one W5.3 before/after measurement under the shared lock: the test
# binary built from the base tree and the one built from the candidate tree
# alternate ROUNDS times (base, candidate, base, ...), so that a drift of
# the host's speed during the hold reaches both sides alike.
#
# Usage: ab.sh HOST OUT LOCK NAME PKG ROUNDS BASETREE CANDTREE TEST_ARGS...
#   HOST      (M) or (L), written into the headers.
#   OUT       directory for NAME-base.txt and NAME-cand.txt.
#   LOCK      lock file shared with the other lanes (ruling R17), taken with
#             flock(1) on a file descriptor; FLOCK overrides the binary
#             (macOS: /opt/homebrew/opt/util-linux/bin/flock).
#   NAME      output file name prefix.
#   PKG       package directory relative to each tree, e.g. internal/codec.
#   ROUNDS    number of alternations.
#   BASETREE  source tree of the base commit; CANDTREE of the candidate.
#   TEST_ARGS arguments of the test binaries (-test.bench, -test.count, ...).
# The binaries are built with `go test -c` before the lock is taken, and run
# from their package directory (the fixtures are read from testdata). Each
# file's header records the date, the load (uptime, and the kernel's own
# figure) and the tree's commit, go version and ToolTags, taken inside the
# lock in the same shell as the measurement; the footer records the exit
# status, the date and the load again. BASE and CAND name the commits.
# MAXLOAD, when set, is the highest 1-minute load average at which the run
# may start, as in _spikes/s-c1/run.sh: above it the lock is released, the
# runner waits 60 s and tries again, up to 5 times, and then runs anyway.
set -eu
host=$1 out=$2 lock=$3 name=$4 pkg=$5 rounds=$6 basetree=$7 candtree=$8
shift 8
mkdir -p "$out"
bin=$(mktemp -d "${TMPDIR:-/tmp}/ab.XXXXXX")
trap 'rm -rf "$bin"' EXIT
(cd "$basetree" && go test -c -o "$bin/base.test" "./$pkg")
(cd "$candtree" && go test -c -o "$bin/cand.test" "./$pkg")
kload() {
	if [ -r /proc/loadavg ]; then
		cat /proc/loadavg
	else
		sysctl -n vm.loadavg
	fi
}
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
for side in base cand; do
	{
		echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host lock=$lock GOEXPERIMENT=${GOEXPERIMENT:-} $side.test $*"
		echo "# load before: $(uptime) | kernel: $(kload) (MAXLOAD=${MAXLOAD:-none}, waited $tries x 60 s)"
		if [ "$side" = base ]; then echo "# commit: ${BASE:-unknown}"; else echo "# commit: ${CAND:-unknown}"; fi
		echo "# rounds: $rounds, alternating base and cand"
		go version
		go list -f '{{context.ToolTags}}' runtime
	} >"$out/$name-$side.txt"
done
status=0
r=0
while [ "$r" -lt "$rounds" ]; do
	r=$((r + 1))
	for side in base cand; do
		if [ "$side" = base ]; then tree=$basetree; else tree=$candtree; fi
		echo "# round $r at $(date '+%Y-%m-%d %H:%M:%S %Z')" >>"$out/$name-$side.txt"
		(cd "$tree/$pkg" && "$bin/$side.test" "$@") >>"$out/$name-$side.txt" 2>&1 || status=$?
	done
done
for side in base cand; do
	{
		echo "# exit $status at $(date '+%Y-%m-%d %H:%M:%S %Z')"
		echo "# load after: $(uptime) | kernel: $(kload)"
	} >>"$out/$name-$side.txt"
done
exit "$status"
