#!/bin/sh
# Usage: gates.sh HOST OUT LOCK BASE SUFFIX
# Runs, in the current directory (a tree at BASE), the AC-P3 measurement,
# go test -race ./... and ci.yaml's root allocation step, each output in
# OUT/<name>-SUFFIX.txt. HOST is (M) or (L).
set -u
host=$1 out=$2 lock=$3 base=$4 sfx=$5
ALLOC='^(TestAllocPrepare|TestAllocBodyKinds|TestAllocDecodeFixtures|TestLinearityFlood|TestAllocWholeCall|TestMemStatsCap|TestAllocResponseJSON|TestAllocTypedDecode)$'
BASE=$base sh _spikes/s-c1/run.sh "$host" "$out" "$lock" "alloc-$sfx" -count=1 -run '^TestAllocTypedDecode$' -v .
BASE=$base sh _spikes/s-c1/run.sh "$host" "$out" "$lock" "race-all-$sfx" -timeout 60m -race -count=1 ./...
{
	echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host base $base: ci.yaml root allocation step, GOEXPERIMENT=${GOEXPERIMENT:-}"
	n=$(go test -list "$ALLOC" . | grep -c '^Test')
	echo "guard: $n names"
	test "$n" -eq 8 && go test -count=1 -run "$ALLOC" -v .
	echo "# exit $? at $(date '+%Y-%m-%d %H:%M:%S %Z')"
} >"$out/root-alloc-step-$sfx.txt" 2>&1
grep -hE '^# (20|load|base|exit)|TYPED|^ok|FAIL|guard' "$out/alloc-$sfx.txt" "$out/race-all-$sfx.txt" "$out/root-alloc-step-$sfx.txt"
