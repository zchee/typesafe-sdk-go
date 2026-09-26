root=(
  TestAllocPrepare         # Prepare's pinned counts (ruling R50)
  TestAllocFalsyJSON       # Prepare P2: truthy criteria not copied (W5.3)
  TestAllocEncode          # AC-P1 single size, every state kind (W5.2)
  TestAllocScratchSequence # AC-P1 mixed-size sequence (W5.2)
  TestAllocDecodeFixtures  # AC-P2 (W2.0)
  TestLinearityFlood       # AC-P8 allocation ratio and members visited (W2.0)
  TestLinearityFloodTime   # AC-P8 time, every build (W5.2)
  TestAllocWholeCall       # AC-P6, N = 12 (W5.3; 14 at W3.4)
  TestAllocAnswersInlineBound # AC-P6, the inline entries' bound (W5.3)
  TestMemStatsCap          # AC-P5 (W2.3)
  TestAllocResponseJSON    # response payloads (W2.4)
  TestAllocTypedDecode     # AC-P3 (W4.2)
  TestAllocTypedFailure    # a typed failure's error, its path rendered once (W5.3)
  TestAllocLoggedCall      # what logging costs a call (W3.3, ruling R102)
  TestAllocRequestID       # a logged request id's cost (R103 revert)
  TestAllocSecretHeaderName # redaction by name without a copy (W5.3)
)
codec=(
  TestEncodeStateAllocations          # AC-P1, the state encode alone (W1.2)
  TestLazyPassAllocations             # AC-P8, c0 + c1 x members visited (W2.0, ruling R110)
  TestNewBodyAllocations              # AC-P1, the sequence's call 23 (W1.2)
  TestPretouchFirstCallCostsAWarmCall # after Pretouch a first encode costs a warm one (W2.3)
)
# require_listed WHAT NAMES LIST...: every name of NAMES (one per
# line) must be in LIST, or no step shows its output.
require_listed() {
  local what=$1 names=$2
  shift 2
  local missing
  missing="$(comm -23 <(printf '%s\n' "$names" | sort -u) <(printf '%s\n' "$@" | sort -u))"
  if [ -n "$missing" ]; then
    echo "::error::$what missing from the list: ${missing//$'\n'/ }"
    exit 1
  fi
}
# A //go:build !race root file is left out of the -race step, so its
# tests run only here. internal/codec cannot carry that constraint
# (TestSeamBuildConstraints); its tests skip themselves under -race
# by calling raceEnabled().
# The root package's allocation tests live in internal/alloctest
# (W6.5, owner G9); a !race test left in the root package is
# required here too, and then fails the -list guard below, which
# counts internal/alloctest only.
require_listed "tests built only without -race" \
  "$(grep -l '^//go:build !race' -- *_test.go internal/alloctest/*_test.go | xargs grep -ho '^func Test[A-Za-z0-9_]*' | sed 's/^func //' | tr -d '\r')" \
  "${root[@]}"
require_listed "internal/codec tests that skip under -race" \
  "$(awk '/^func / { name = ($2 ~ /^Test[A-Za-z0-9_]*\(/) ? $2 : ""; sub(/\(.*/, "", name) } /raceEnabled\(\)/ && name != "" { print name }' internal/codec/*_test.go | tr -d '\r')" \
  "${codec[@]}"
# run_budgets PACKAGE TEST... guards and runs one list.
run_budgets() {
  local pkg=$1
  shift
  local pattern listed
  pattern="^($(IFS='|'; echo "$*"))\$"
  listed="$(go test -list "$pattern" "$pkg" | grep -c '^Test')" || true
  if [ "$listed" != "$#" ]; then
    echo "::error::go test -list matched ${listed:-no} tests of the $# listed in $pkg: $pattern"
    exit 1
  fi
  go test -count=1 -run "$pattern" -v "$pkg"
}
run_budgets ./internal/alloctest/ "${root[@]}"
run_budgets ./internal/codec/ "${codec[@]}"
