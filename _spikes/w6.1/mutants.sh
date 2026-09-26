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

echo "# $(date '+%Y-%m-%d %H:%M:%S %Z') mutants not killed (survived, not applied or not built): $fails"
exit "$fails"
