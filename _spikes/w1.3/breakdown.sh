#!/bin/sh
# Allocation call sites of Questions.Prepare per W1.3 case: memprofile at
# rate 1 of TestAllocPrepare/<case> (MeasureMin: 5 measured calls), traces
# focused on (*Questions).Prepare, testing/testsupport frames dropped.
set -u
wt=$1 lock=$2 out=$3 tmp=$4
cd "$wt"
exec 9>"$lock"
/opt/homebrew/opt/util-linux/bin/flock 9
{
echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') (M) lock=$lock GOEXPERIMENT=$GOEXPERIMENT go test -c; ./ts.test -test.run '^TestAllocPrepare\$/^<case>\$' -test.memprofile mem-<case>.out -test.memprofilerate=1; go tool pprof -sample_index=alloc_objects -focus='Questions..Prepare\$' -traces"
echo "# load before: $(uptime) | kernel: $(sysctl -n vm.loadavg)"
echo "# base: $(git rev-parse --short HEAD) (production code as b227e5b)"
go version
go list -f '{{context.ToolTags}}' runtime
echo "# counts are over the 5 measured calls of MeasureMin (divide by 5); an 8-byte"
echo "# pointer-free object that shares a 16-byte tiny block is not sampled, so"
echo "# MemStats.Mallocs (alloc-*-base*.txt) is the count of record"
go test -c -o "$tmp/ts.test" . || exit 1
for c in c1-sketch c2-noul-short c3-choice-20x10 c4a-score-20x8-text c4b-score-20x8-json c5-raw-100x3 c6-escapes n8a-array-control n8a-array-score n8b-map-control n8b-map-score; do
	echo "=== $c"
	(cd "$tmp" && ./ts.test -test.run "^TestAllocPrepare\$/^$c\$" -test.count=1 -test.memprofile "mem-$c.out" -test.memprofilerate=1 >/dev/null) || echo "# run failed"
	go tool pprof -sample_index=alloc_objects -focus='Questions..Prepare$' -traces "$tmp/ts.test" "$tmp/mem-$c.out" 2>&1 |
		grep -vE '^[[:space:]]+(testing\.|runtime\.goexit|github.com/zchee/typesafe-sdk-go/internal/testsupport\.|github.com/zchee/typesafe-sdk-go\.TestAllocPrepare)' |
		grep -vE '^(File|Build ID|Type|Time):'
done
echo "# exit 0 at $(date '+%Y-%m-%d %H:%M:%S %Z')"
echo "# load after: $(uptime) | kernel: $(sysctl -n vm.loadavg)"
} >"$out"
