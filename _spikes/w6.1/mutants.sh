#!/bin/bash
# Runs W6.1's mutants against a scratch tree and prints one line each.
#
# Usage: mutants.sh TREE
#   TREE  a checkout of the wave's head that this script may modify (a
#         detached worktree); every mutant is applied to a pristine copy of
#         its file and the file is restored afterwards.
#
# A mutant is killed when the named tests FAIL; "survived" means they
# passed, and "BUILD FAILED" that the mutant did not compile, which proves
# nothing (the script reports it apart instead of counting it as a kill).
# Run it with GOEXPERIMENT=nosimd,noruntimesecret on (M).
set -u
tree=$1
cd "$tree" || exit 1
fails=0

# mutant NAME FILE PERL_EXPR PACKAGE RUN [COUNT]
mutant() {
	local name=$1 file=$2 expr=$3 pkg=$4 run=$5 count=${6:-1}
	local orig out verdict
	orig=$(mktemp)
	cp "$file" "$orig"
	perl -0pi -e "$expr" "$file"
	if cmp -s "$file" "$orig"; then
		echo "$name: NOT APPLIED (the pattern no longer matches $file)"
		fails=$((fails + 1))
		cp "$orig" "$file"
		rm -f "$orig"
		return
	fi
	out=$(go test -count="$count" -run "$run" "$pkg" 2>&1)
	if printf '%s\n' "$out" | grep -q -E '\[build failed\]|^# .*\[.*\.test\]$'; then
		verdict="BUILD FAILED"
		fails=$((fails + 1))
	elif printf '%s\n' "$out" | grep -q -E -- '^--- FAIL|^FAIL'; then
		verdict="killed ($(printf '%s\n' "$out" | grep -c -- '--- FAIL') FAIL lines)"
	else
		verdict="SURVIVED"
		fails=$((fails + 1))
	fi
	echo "$name: $verdict :: $(printf '%s\n' "$out" | grep -m1 -E '_test\.go:[0-9]+: ' | sed 's/^ *//' | cut -c1-160)"
	cp "$orig" "$file"
	rm -f "$orig"
}

echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') mutants.sh at $(git rev-parse --short HEAD) $(go version)"

# A: the per-input bound.
mutant A-bound-never-fires internal/testsupport/fuzz.go \
	's/panic\(fmt.Sprintf\("testsupport: fuzz input %s ran past the %v per-input bound", name, d\)\)/_ = fmt.Sprintf("%s%v", name, d)/' \
	./internal/testsupport/ '^TestBoundFuzzInput$'

# B: the differential. Each lazy-pass mutant makes one last-wins choice
# first-wins; M5/M6 do so in the visitor; M7 drops skipped answers; M8 cuts
# a structured level's bytes.
B=./internal/codec/
mutant B-M1-lazy-answers-first-wins internal/codec/lazy.go \
	's/if p.Key == "answers" \{\n\t\t\tanswers = p.Value/if p.Key == "answers" \&\& !answers.Exists() {\n\t\t\tanswers = p.Value/' $B '^FuzzDecodePaths$'
mutant B-M2-lazy-answer-name-first-wins internal/codec/lazy.go \
	's/v.set\[i\].structured > 0 \{\n\t\t\td.nodes\[i\] = p.Value/v.set[i].structured > 0 \&\& !d.nodes[i].Exists() {\n\t\t\td.nodes[i] = p.Value/' $B '^FuzzDecodePaths$'
mutant B-M3-lazy-legend-first-wins internal/codec/lazy.go \
	's/if p.Key == "legend" \{\n\t\t\t\tlegend = p.Value/if p.Key == "legend" \&\& !legend.Exists() {\n\t\t\t\tlegend = p.Value/' $B '^FuzzDecodePaths$'
mutant B-M4-lazy-level-first-wins internal/codec/lazy.go \
	's/\t\t\td.raws\[base\+j\] = raw\n/\t\t\tif d.raws[base+j] == "" {\n\t\t\t\td.raws[base+j] = raw\n\t\t\t}\n/' $B '^FuzzDecodePaths$'
mutant B-M5-visitor-level-first-value internal/codec/commit.go \
	's/(mixed = mixed \|\| v.legend\[folds\[j\].first\].key != l.key\n)\t\t\tlegend\[j\].Description = desc\n/$1/' $B '^FuzzDecodePaths$'
mutant B-M6-visitor-answer-first-value internal/codec/commit.go \
	's/if i := v.find\(e.name\); i >= 0 \{\n\t\tv.set\[i\] = e\n/if i := v.find(e.name); i >= 0 {\n/' $B '^FuzzDecodePaths$'
mutant B-M7-skipped-last-only internal/codec/decode.go \
	's/if !e.err.set && e.ans.Kind == wire.KindUnknown \{\n\t\t\tskipped.add\(e.name, e.typ\)/if !e.err.set \&\& e.ans.Kind == wire.KindUnknown \&\& i == len(v.set)-1 {\n\t\t\tskipped.add(e.name, e.typ)/' $B '^FuzzDecodePaths$'
mutant B-M8-lazy-raw-cut internal/codec/lazy.go \
	's/\t\t\td.raws\[base\+j\] = raw\n/\t\t\td.raws[base+j] = raw[:len(raw)-1]\n/' $B '^FuzzDecodePaths$'

# D/K39: refusedConn hands out a closed listener's address again, or
# closes the connection that holds its port. TestRefusedAddr checks that the
# address is the held connection's local end and that the connection is
# still open, which does not depend on the OS (D-W6.1-k39-shape).
mutant D-K39-closed-listener internal/testsupport/refused.go \
	's/return held.LocalAddr\(\).String\(\), held/return ln.Addr().String(), held/' ./internal/testsupport/ '^TestRefusedAddr$'
mutant D-K39-held-closed internal/testsupport/refused.go \
	's/(\treturn held.LocalAddr\(\).String\(\), held\n)/\t_ = held.Close()\n$1/' ./internal/testsupport/ '^TestRefusedAddr$'

# D/K25: the D-TSflake GoAway, which publishes its state without the write
# lock and takes it only for the frame.
mutant D-K25-pre-fix-goaway internal/testsupport/h2conn.go \
	's/(\t\treturn fmt.Errorf\("%w: GOAWAY after close_notify", errConnClosed\)\n\t\}\n)/$1\tc.wmu.Unlock()\n/; s/(\terr := c.fr.WriteGoAway\(lastStreamID)/\tc.wmu.Lock()\n$1/' \
	./internal/testsupport/ '^TestGoAwayRaceWithFinish$' 20

# D/n-2: a token left held before the probe.
mutant D-n2-leaked-token internal/h2gate/token_test.go \
	's/(\t\t\tprobeCtx, cancel := context.WithTimeout\(t.Context\(\), k21Deadline\)\n)/\t\t\ttr.token <- struct{}{}\n$1/' \
	./internal/h2gate/ '^TestTokenResidualK21$'
# D/n-2: a Transport that lingers 300 ms in a call whose context has ended
# (D-W6.1-n2-major). The control runs no h2gate code, so the slack stays at
# its floor and the late calls fail; calibrated through the Transport, the
# slack grew to about 1.2 s and the test passed.
mutant D-n2-linger-on-timeout internal/h2gate/transport.go \
	's/(func \(t \*Transport\) send\(req \*http.Request, gen \*generation\) \(\*http.Response, error\) \{\n\tctx := req.Context\(\)\n)/$1\tdefer func() {\n\t\tif ctx.Err() != nil {\n\t\t\ttime.Sleep(300 * time.Millisecond)\n\t\t}\n\t}()\n/' \
	./internal/h2gate/ '^TestTokenResidualK21$'

echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') mutants not killed (survived, not applied or not built): $fails"
exit "$fails"
