#!/bin/sh
# Usage: series.sh HOST OUT LOCK BASE SUFFIX
# Runs W4.3's measurements in the current directory (a tree at BASE), each
# with _spikes/s-c1/run.sh under the lock (flock(1) on LOCK; FLOCK
# overrides the binary), one lock hold per invocation, each output in
# OUT/<name>-SUFFIX.txt:
#   bench  S-D2 timing: BenchmarkSD2, -count=5 (deliverable A); MAXLOAD
#          (the host's core count) gates the start as run.sh says
#   alloc  S-D2 allocation counts and the PreparedFor first calls
#          (deliverables A and B), with the agreement tests
#   typed  the root package's TestAllocTypedDecode: AC-P3 and the Ask row
#          (deliverable C)
#   profile  (only when PROFILE is set) the call sites of the allocations
#          of twenty's first PreparedFor call: TestFirstCallChild with
#          SD2_FIRST_CALL_PROFILE, the test binary built with -trimpath,
#          then go tool pprof -lines -top; TMP names a scratch directory
# MAXLOAD gates only the bench run; allocation counts do not depend on
# load (ruling R17). HOST is (M) or (L).
set -u
host=$1 out=$2 lock=$3 base=$4 sfx=$5
R=_spikes/s-c1/run.sh
BASE=$base sh $R "$host" "$out" "$lock" "bench-$sfx" -run '^$' -bench '^BenchmarkSD2$' -benchmem -count=5 ./_spikes/w4.3/
MAXLOAD= BASE=$base sh $R "$host" "$out" "$lock" "alloc-$sfx" -count=1 -run '^(TestVariantsAgree|TestReplicasRefuse|TestShapesMatchByHand|TestSD2Allocs|TestPreparedForFirstCall)$' -v ./_spikes/w4.3/
MAXLOAD= BASE=$base sh $R "$host" "$out" "$lock" "typed-$sfx" -count=1 -run '^TestAllocTypedDecode$' -v .
if [ -n "${PROFILE:-}" ]; then
	exec 9>"$lock"
	"${FLOCK:-flock}" 9
	{
		echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host lock=$lock GOEXPERIMENT=${GOEXPERIMENT:-} first-call allocation sites of twenty"
		echo "# base: $base"
		go version
		go list -f '{{context.ToolTags}}' runtime
		go test -c -trimpath -o "$TMP/sd2.test" ./_spikes/w4.3/ &&
			SD2_FIRST_CALL=twenty SD2_FIRST_CALL_PROFILE="$TMP/twenty.pprof" "$TMP/sd2.test" -test.run='^TestFirstCallChild$' &&
			go tool pprof -sample_index=alloc_objects -lines -top "$TMP/sd2.test" "$TMP/twenty.pprof"
		echo "# exit $? at $(date '+%Y-%m-%d %H:%M:%S %Z')"
	} >"$out/profile-$sfx.txt" 2>&1
	exec 9>&-
fi
grep -hE '^# (20|load|base|exit)|^(ok|FAIL|PASS)|SD2ALLOC|FIRST |TYPED' "$out/bench-$sfx.txt" "$out/alloc-$sfx.txt" "$out/typed-$sfx.txt"
