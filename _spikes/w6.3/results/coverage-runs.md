# W6.3 coverage runs at 67dcbb0

Statement coverage per package, computed from each profile (statements of blocks with a count above zero over all statements; a block listed twice keeps its largest count). "Codecov scope" leaves out the paths `.codecov.yaml` ignores. "zero blocks" counts the zero-count blocks in that scope; `stateWarm` is `internal/h2gate/transport.go:247.4,248.27`.

Runs (times from `date` in each runner's own output):

- (M) darwin/arm64, go1.27.1, GOEXPERIMENT=nosimd,noruntimesecret: base1 2026-09-26 17:02:51 JST → base4 race end 17:09:08 JST, non-race then -race each (base3's -race run failed on an edit made in the tree during the run and is excluded).
- (L) linux/amd64, go1.27.1, 44 CPUs: L-race-1/2 and L-nonrace-1 2026-09-26 08:05:17 → 08:07:31 UTC; L4-race-1..5 under `taskset -c 0-3` 08:09:57 → 08:15:07 UTC.
- Cross-package (M) run `go test -count=1 -coverpkg=./... -covermode=atomic ./...` on 48fca9e (same production files): 2026-09-26 17:32:36 → 17:32:55 JST; of the 86 zero-count blocks, 3 are covered by another package's tests: `internal/codec/visitor.go:505` (`errRootNotObject`), `internal/codec/visitor.go:635` (`output_tokens` null), `internal/h2gate/transport.go:247` (`stateWarm`).

| profile | (root) | internal/codec | internal/h2gate | internal/wire | internal/testsupport | internal/testsupport/naive | all | Codecov scope | zero blocks (scope) | stateWarm |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| L-nonrace-1-67dcbb0 | 98.6 | 96.4 | 96.4 | 99.7 | 90.9 | 93.1 | 96.3 | 98.0 | 85 | covered |
| L-race-1-67dcbb0 | 98.6 | 96.4 | 96.0 | 99.7 | 90.9 | 93.1 | 96.3 | 97.9 | 86 | zero |
| L-race-2-67dcbb0 | 98.6 | 96.4 | 96.0 | 99.7 | 91.0 | 93.1 | 96.3 | 97.9 | 86 | zero |
| L4-race-1-67dcbb0 | 98.6 | 96.4 | 96.0 | 99.7 | 90.9 | 93.1 | 96.3 | 97.9 | 86 | zero |
| L4-race-2-67dcbb0 | 98.6 | 96.4 | 96.0 | 99.7 | 91.1 | 93.1 | 96.3 | 97.9 | 86 | zero |
| L4-race-3-67dcbb0 | 98.6 | 96.4 | 96.0 | 99.7 | 90.9 | 93.1 | 96.3 | 97.9 | 86 | zero |
| L4-race-4-67dcbb0 | 98.6 | 96.4 | 96.4 | 99.7 | 90.9 | 93.1 | 96.3 | 98.0 | 85 | covered |
| L4-race-5-67dcbb0 | 98.6 | 96.4 | 96.0 | 99.7 | 90.9 | 93.1 | 96.3 | 97.9 | 86 | zero |
| base1-67dcbb0-nonrace | 98.6 | 96.4 | 96.0 | 99.7 | 90.8 | 93.1 | 96.2 | 97.9 | 86 | zero |
| base1-67dcbb0-race | 98.6 | 96.4 | 96.0 | 99.7 | 91.0 | 93.1 | 96.3 | 97.9 | 86 | zero |
| base2-67dcbb0-nonrace | 98.6 | 96.4 | 96.0 | 99.7 | 90.8 | 93.1 | 96.2 | 97.9 | 86 | zero |
| base2-67dcbb0-race | 98.6 | 96.4 | 96.4 | 99.7 | 91.3 | 93.1 | 96.4 | 98.0 | 85 | covered |
| base3-67dcbb0-nonrace | 98.6 | 96.4 | 96.0 | 99.7 | 90.8 | 93.1 | 96.2 | 97.9 | 86 | zero |
| base4-67dcbb0-nonrace | 98.6 | 96.4 | 96.4 | 99.7 | 91.1 | 93.1 | 96.4 | 98.0 | 85 | covered |
| base4-67dcbb0-race | 98.6 | 96.4 | 96.0 | 99.7 | 90.9 | 93.1 | 96.3 | 97.9 | 86 | zero |

## Rebased onto f73ab2b

The same measurement on the rebased W6.3 tree, whose production files are f73ab2b's (main after W5.3 and W5.4); W6.3 adds only test files. "Codecov scope" has two decimals here; "of" is the number of blocks in that scope.

Runs (times from `date` in each runner's own output; every run rc=0 with 0 FAIL lines):

- (M) darwin/arm64, 16 cores, go1.27.1, GOEXPERIMENT=nosimd,noruntimesecret, from a `git archive` copy, bench lock free and 1-minute load 6.5–6.8 at every start and end: non-race 2026-09-26 23:42:39 → 23:42:57 JST, -race → 23:44:01 JST.
- (L) linux/amd64, go1.27.1: L-race-1/2 on 44 CPUs 2026-09-26 14:49:08 → 14:51:47 UTC (load 1.45–2.96), L4-race-1/2 under `taskset -c 0-3` 14:51:47 → 14:54:11 UTC (load 2.67–3.17).
- Cross-package (M) run `go test -count=1 -coverpkg=./... -covermode=atomic ./...`: 2026-09-26 23:45:40 → 23:45:59 JST; of the 87 zero-count blocks of its non-race twin, 2 are covered by another package's tests: `internal/wire/prepared.go:198` (`(*Builder).GrowLevels`, 1395 times) and `internal/codec/visitor.go:648` (`output_tokens` null). `stateWarm` was covered in the run itself.

| profile | (root) | internal/codec | internal/h2gate | internal/wire | internal/testsupport | internal/testsupport/naive | all | Codecov scope | zero blocks (scope) | of | stateWarm |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| M-nonrace-f73ab2b | 98.6 | 96.6 | 96.4 | 99.5 | 90.9 | 93.1 | 96.4 | 97.99 | 87 | 3117 | covered |
| M-race-f73ab2b | 98.6 | 96.6 | 96.0 | 99.5 | 91.3 | 93.1 | 96.4 | 97.95 | 88 | 3117 | zero |
| L-race-1-f73ab2b | 98.6 | 96.6 | 96.4 | 99.5 | 90.9 | 93.1 | 96.4 | 97.99 | 87 | 3117 | covered |
| L-race-2-f73ab2b | 98.6 | 96.6 | 96.0 | 99.5 | 91.2 | 93.1 | 96.4 | 97.95 | 88 | 3117 | zero |
| L4-race-1-f73ab2b | 98.6 | 96.6 | 96.0 | 99.5 | 90.9 | 93.1 | 96.3 | 97.95 | 88 | 3117 | zero |
| L4-race-2-f73ab2b | 98.6 | 96.6 | 96.4 | 99.5 | 90.9 | 93.1 | 96.4 | 97.99 | 87 | 3117 | covered |
