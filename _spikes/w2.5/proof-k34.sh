#!/bin/sh
# proof-k34.sh HOST LOCK LOOPS PIN: the p2-cifix-3 proof runs (K34, D),
# sequential, each under the host's bench lock via _spikes/w2.2/contend.sh;
# run from the tree's root. PIN is a go test -exec wrapper ("" for none).
set -u
host=$1 lock=$2 loops=$3 pin=$4
tag=$(echo "$host" | tr -d '()' | tr 'A-Z' 'a-z')
O=_spikes/w2.5/results C=_spikes/w2.2/contend.sh
export TREE=${TREE:?}
# The charter's four h2gate tests and the testsupport tests that end a
# connection server-side (the audit's rows).
K34='^(TestReplay|TestGoAway|TestConnClose|TestTokenResidualK21|TestLoopbackActionsDrain|TestLoopbackCloseConnsDrains|TestLoopbackGoAway|TestLoopbackConnEnd|TestLoopbackRefuseCloseHold|TestLoopbackSetMaxConcurrentStreams)$'
ALLOC='^(TestAllocPrepare|TestAllocBodyKinds|TestAllocDecodeFixtures|TestLinearityFlood|TestAllocWholeCall|TestMemStatsCap|TestAllocResponseJSON)$'
set -- ${pin:+-exec "$pin"}
sh $C "$host" $O "$lock" "$tag-k34-race-unloaded" 0 "$@" -timeout 60m -race -count=50 -cpu 1,2,4 -run "$K34" -v ./internal/testsupport/ ./internal/h2gate/
sh $C "$host" $O "$lock" "$tag-k34-race-contended" "$loops" "$@" -timeout 60m -race -count=50 -cpu 1,2,4 -run "$K34" -v ./internal/testsupport/ ./internal/h2gate/
sh $C "$host" $O "$lock" "$tag-k34-race-all" 0 -timeout 60m -race -count=1 ./...
{
	exec 9>"$lock"
	"${FLOCK:-flock}" 9
	echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') $host tree=$TREE lock=$lock GOEXPERIMENT=${GOEXPERIMENT:-} ci.yaml 'go test without -race (root allocation tests)', both lines"
	echo "# load before: $(uptime)"
	go version
	go list -f '{{context.ToolTags}}' runtime
	n=$(go test -list "$ALLOC" . | grep -c '^Test')
	echo "# -list guard: $n names (want 7)"
	test "$n" -eq 7 && go test -count=1 -run "$ALLOC" -v . 2>&1
	status=$?
	echo "# load at the end: $(uptime)"
	echo "# exit $status at $(date '+%Y-%m-%d %H:%M:%S %Z')"
} >"$O/$tag-k34-root-alloc-step.txt"
for f in $O/$tag-k34-*.txt; do printf '%s: %s\n' "$f" "$(grep -E '^# exit' "$f")"; done
