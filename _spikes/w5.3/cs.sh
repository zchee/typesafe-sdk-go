#!/bin/sh
# Runs CodSpeed's walltime runner locally (codspeed run --skip-upload) on
# internal/benchmark's BenchmarkCall rows sdk, naive, sdk-q20 and naive-q20,
# in the base tree and the candidate tree alternately, ROUNDS times, under
# the shared lock, and appends each run's per-iteration minimum, median and
# mean from the runner's raw results: CodSpeed shows the minimum (K36),
# AC-P7 reads the mean (ruling R108). A proxy for the CodSpeed run on a
# hosted amd64 runner, which this cannot replace.
#
# Usage: cs.sh HOST OUT LOCK NAME ROUNDS BASETREE CANDTREE
# FLOCK, MAXLOAD, BASE and CAND as in ab.sh; jq must be on PATH.
set -eu
host=$1 out=$2 lock=$3 name=$4 rounds=$5 basetree=$6 candtree=$7
mkdir -p "$out"
work=$(mktemp -d "${TMPDIR:-/tmp}/cs.XXXXXX")
# The runner leaves a read-only module cache under its TMPDIR.
trap 'chmod -R u+w "$work" 2>/dev/null; rm -rf "$work"' EXIT
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
f="$out/$name.txt"
{
	echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host lock=$lock GOEXPERIMENT=${GOEXPERIMENT:-} codspeed run --skip-upload -m walltime -- go test -bench='^BenchmarkCall\$/^(sdk|naive)(-q20)?\$' -run='^\$' ./internal/benchmark/"
	echo "# load before: $(uptime) (MAXLOAD=${MAXLOAD:-none}, waited $tries x 60 s)"
	echo "# base: ${BASE:-unknown} cand: ${CAND:-unknown}; rounds: $rounds, alternating"
	codspeed --version
	go version
	echo "# side round benchmark min_ns median_ns mean_ns rounds"
} >"$f"
status=0
r=0
while [ "$r" -lt "$rounds" ]; do
	r=$((r + 1))
	for side in base cand; do
		if [ "$side" = base ]; then tree=$basetree; else tree=$candtree; fi
		dir="$work/$side-$r"
		mkdir -p "$dir"
		# The runner joins its arguments into one shell line unquoted, so the
		# command goes as one string with its own quoting.
		(cd "$tree" && TMPDIR="$dir/" codspeed run --skip-upload -m walltime -- "go test -bench='^BenchmarkCall\$/^(sdk|naive)(-q20)?\$' -run='^\$' ./internal/benchmark/") >"$dir/log.txt" 2>&1 || status=$?
		for j in "$dir"/profile.*/results/*.json; do
			[ -e "$j" ] || { echo "# $side $r: no raw results (runner exit $status)" >>"$f"; continue; }
			jq -r --arg side "$side" --arg r "$r" '.benchmarks[] | [$side, $r, .name, .stats.min_ns, .stats.median_ns, (.stats.mean_ns | floor), .stats.rounds] | @tsv' "$j" >>"$f"
		done
	done
done
{
	echo "# exit $status at $(date '+%Y-%m-%d %H:%M:%S %Z')"
	echo "# load after: $(uptime)"
} >>"$f"
exit "$status"
