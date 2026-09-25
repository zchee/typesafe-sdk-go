#!/bin/sh
# The injected-delay proof of the two F-3 harness races (R86): throwaway
# sleeps widen each race in a detached worktree, never committed, and the
# old and the new tests run against them. Every run goes through contend.sh
# with no yes loop, so its raw file has the usual header.
#
# Usage, from the repository: flakefix-inject.sh HOST OUT LOCK BASE HEAD
#   BASE  the commit with the old tests (45fd018).
#   HEAD  the commit with the fixes.
# The worktrees live under ${TMPDIR:-/tmp} and are removed at the end.
#
# The delays:
#   I1  proxy.go: 200 ms after a failed Handshake, before the record write.
#   I2  loopback.go: 500 ms after an HTTP/2 connection's reader stopped,
#       before LiveH2Conns forgets it.
#   I3  token_test.go: 200 ms before the limit is raised back to 8, so the
#       first connection's close has finished (kept listed by I2).
#   I4  h2conn.go: 300 ms in SetMaxConcurrentStreams between its closed
#       check and its SETTINGS write.
#   M2  h2conn.go: a mutant that drops the check after a failed write.
# The close-during-write harness (TestZZCloseDuringSetMax, written below)
# closes the client side inside I4's sleep.
set -eu
host=$1 out=$2 lock=$3 base=$4 head=$5
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/flakefix-inject.XXXXXX")

inject() {
	d=$1
	shift
	for k in "$@"; do
		case $k in
		I1) perl -0pi -e 's/(\t\terr := tc\.Handshake\(\)\n)(\t\t_ = tc\.SetDeadline\(time\.Time\{\}\)\n\t\tp\.mu\.Lock\(\))/$1\t\tif err != nil {\n\t\t\ttime.Sleep(200 * time.Millisecond) \/\/ INJECT I1\n\t\t}\n$2/' "$d/internal/testsupport/proxy.go" ;;
		I2) perl -0pi -e 's/(\t\tc\.serve\(\)\n)(\t\ts\.mu\.Lock\(\)\n\t\tdelete\(s\.h2, c\))/$1\t\ttime.Sleep(500 * time.Millisecond) \/\/ INJECT I2\n$2/' "$d/internal/testsupport/loopback.go" ;;
		I3) perl -0pi -e 's/(\n)(\t\t\tfor _, c := range srv\.LiveH2Conns\(\) \{\n)/$1\t\t\ttime.Sleep(200 * time.Millisecond) \/\/ INJECT I3\n$2/' "$d/internal/h2gate/token_test.go" ;;
		I4) perl -0pi -e 's/(\tc\.maxStreams = n\n\tc\.mu\.Unlock\(\)\n)/$1\ttime.Sleep(300 * time.Millisecond) \/\/ INJECT I4\n/' "$d/internal/testsupport/h2conn.go" ;;
		M2) perl -0pi -e 's/\t\tif closing \{\n/\t\tif false \&\& closing { \/\/ MUTANT M2\n/' "$d/internal/testsupport/h2conn.go" ;;
		esac
	done
	# Every requested edit must have landed: a pattern that no longer
	# matches would otherwise run the tests unmodified.
	test "$(grep -rc 'INJECT \|MUTANT ' "$d/internal" | awk -F: '{s += $2} END {print s}')" -eq "$#"
}

harness() {
	cat >"$1/internal/testsupport/zz_flakefix_test.go" <<'EOF'
package testsupport

import (
	"errors"
	"testing"
	"time"
)

// TestZZCloseDuringSetMax closes the client side while
// SetMaxConcurrentStreams sleeps between its closed check and its write
// (INJECT I4), so the reader's Close lands before the write.
func TestZZCloseDuringSetMax(t *testing.T) {
	srv := NewLoopbackServer(t, ServerConfig{MaxConcurrentStreams: 8})
	c := dialRaw(t, srv.Addr())
	c.serverSettings()
	conn := srv.LiveH2Conns()[0]
	errc := make(chan error, 1)
	go func() { errc <- conn.SetMaxConcurrentStreams(2) }()
	time.Sleep(50 * time.Millisecond) // inside I4's 300 ms
	_ = c.conn.Close()
	err := <-errc
	t.Logf("SetMaxConcurrentStreams = %v; errors.Is(ErrConnClosing) = %v", err, errors.Is(err, ErrConnClosing))
	if !errors.Is(err, ErrConnClosing) {
		t.Fatalf("got %v, want ErrConnClosing", err)
	}
}
EOF
}

# run NAME COMMIT KINDS GO_TEST_ARGS...
run() {
	name=$1 commit=$2 kinds=$3
	shift 3
	d=$tmp/$name
	git worktree add --detach "$d" "$commit" >/dev/null
	# shellcheck disable=SC2086 # KINDS is a word list
	inject "$d" $kinds
	case $kinds in *I4*) harness "$d" ;; esac
	(cd "$d" && TREE="$(git rev-parse --short HEAD)+inject($kinds)" sh "$here/contend.sh" "$host" "$out" "$lock" "$name" 0 "$@")
	git worktree remove --force "$d"
}

races='^(TestProxyModes|TestTokenResidualK21)$/(strict_ALPN_refuses|GOAWAY)'
run m-flakefix-inject-before "$base" 'I1 I2 I3' -count=20 -run "$races" -v ./internal/testsupport/ ./internal/h2gate/
run m-flakefix-inject-after "$head" 'I1 I2 I3' -count=20 -run "$races" -v ./internal/testsupport/ ./internal/h2gate/
run m-flakefix-inject-write-after "$head" 'I4' -count=20 -run '^TestZZCloseDuringSetMax$' -v ./internal/testsupport/
run m-flakefix-inject-write-m2 "$head" 'I4 M2' -count=20 -run '^TestZZCloseDuringSetMax$' -v ./internal/testsupport/
rmdir "$tmp"
