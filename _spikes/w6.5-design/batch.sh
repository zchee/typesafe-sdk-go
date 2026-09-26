#!/bin/sh
# Runs the W6.5 design lane's measurement batch on one host, one run at a
# time (never two at once): the allocation budgets of each design from
# internal/alloctest, then BenchmarkCall and BenchmarkDecode base against
# each design, interleaved (ab.sh).
#
# Usage: batch.sh HOST OUT LOCK BASE D1 D2
#   HOST (M) or (L); OUT the results directory; LOCK the shared bench lock;
#   BASE, D1, D2 the source trees of 93e9db1, D1 and D2.
# FLOCK names flock(1); on (M) GOEXPERIMENT=nosimd,noruntimesecret is set
# by the caller. Every run waits until `flock -n LOCK true` succeeds and the
# 1-minute load is under 12 before it takes the lock.
set -u
host=$1 out=$2 lock=$3 base=$4 d1=$5 d2=$6
here=$(cd "$(dirname "$0")" && pwd)
flockbin=${FLOCK:-flock}
list='TestAllocPrepare|TestAllocFalsyJSON|TestAllocEncode|TestAllocScratchSequence|TestAllocDecodeFixtures|TestLinearityFlood|TestLinearityFloodTime|TestAllocWholeCall|TestAllocAnswersInlineBound|TestMemStatsCap|TestAllocResponseJSON|TestAllocTypedDecode|TestAllocTypedFailure|TestAllocLoggedCall|TestAllocRequestID|TestAllocSecretHeaderName'
tag=M
[ "$host" = "(L)" ] && tag=L
waitfree() {
	while :; do
		load=$(uptime | sed -E 's/.*load averages?: *([0-9.]+).*/\1/')
		if "$flockbin" -n "$lock" true && awk "BEGIN { exit !($load < 12) }"; then
			return
		fi
		sleep 60
	done
}
mkdir -p "$out"
for d in d1:"$d1":fa26bfa d2:"$d2":61b457a; do
	name=${d%%:*} rest=${d#*:}
	tree=${rest%:*} commit=${rest##*:}
	sh "$here/allocrun.sh" "$host" "$out/alloc-$name-$tag.txt" "$lock" "$tree" internal/alloctest "$commit" \
		-test.run "^($list)\$" -test.v -test.count=1
	echo "alloc $name exit $?"
done
for d in d1:"$d1":fa26bfa d2:"$d2":61b457a; do
	name=${d%%:*} rest=${d#*:}
	tree=${rest%:*} commit=${rest##*:}
	waitfree
	BASE=93e9db1 CAND=$commit MAXLOAD=12 sh "$here/ab.sh" "$host" "$out" "$lock" "call-$name-$tag" internal/benchmark 5 "$base" "$tree" \
		-test.run '^$' -test.bench '^BenchmarkCall$/^(sdk|naive)(-q20)?$' -test.benchmem -test.count 2
	echo "call $name exit $?"
	waitfree
	BASE=93e9db1 CAND=$commit MAXLOAD=12 sh "$here/ab.sh" "$host" "$out" "$lock" "decode-$name-$tag" internal/codec 5 "$base" "$tree" \
		-test.run '^$' -test.bench '^BenchmarkDecode$/^(result|result-20)$' -test.benchmem -test.count 2
	echo "decode $name exit $?"
done
