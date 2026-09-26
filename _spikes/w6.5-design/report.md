# W6.5 design: engine relocation (lane `w6-5-design`, architect/opus)

| Item | Value |
| --- | --- |
| Charter | `.omc/handoffs/charters/w6.5-design.md` (owner instruction G9) |
| Base | `origin/main` = `93e9db1` (W6.3 landed) |
| Spike branch | `spike/w6.5-design` (evidence only, never merged): `fa26bfa` = D1, `61b457a` = D2 (built on the base tree, not on D1), evidence head = see the SendMessage first line |
| Raw files | `_spikes/w6.5-design/results/` on the spike branch; scripts `_spikes/w6.5-design/{allocrun.sh,ab.sh,batch.sh}`, D2's facade generator `_spikes/w6.5-design/d2gen/main.go`, the type-name probe `typeprobe.go.txt` |
| Hosts | (M) go1.27.1 darwin/arm64, `GOEXPERIMENT=nosimd,noruntimesecret` (ToolTags `[… jsonv2 greenteagc randomizedheapbase64 sizespecializedmalloc arm64.v8.0]`), under `/opt/homebrew/opt/util-linux/bin/flock <SP>/bench.lock`, each run started only after `flock -n` succeeded and the 1-minute load was under 12; (L) go1.27.1 linux/amd64 (`/tmp/ts-spike/go/bin/go`, ToolTags with `dwarf5`, `amd64.v1`), under `flock /tmp/ts-spike/bench.lock`, trees in `/tmp/ts-spike/w6.5-design/{base,d1,d2}` by tar pipe |

## 0. Recommendation in brief

**D1** — move the stages into `internal/engine`, and keep every public type declared in the root package, with its own methods and docs. Three of those types (`Client`, `Prepared`, `SystemOneResponse`) are declared as *defined types over engine state* (`type Client engine.Client`). A test then converts a public value to its state for free, compile-checked and without `unsafe`. Cost:

- **Allocations:** 0 and 0 B on every pin, on both hosts.
- **Time:** within noise on both hosts (`call/sdk` +0.2 % (M), +0.07 % (L); both p > 0.4).
- **API presentation:** the API golden is byte-identical; `go doc -all .` changes 3 declaration lines (`type Client engine.Client` instead of `type Client struct { // Has unexported fields. }`); `%T`/`reflect`/error texts are unchanged; the R116 seam (the one root `unsafe` file) does not move.

**D2** (aliases) moves the whole package. It keeps every pin too, but:

- The golden loses 173 lines (every field and method line) and gains 37 alias lines.
- `go doc -all .` shrinks from 1785 to 1048 lines, and `go doc . Client.SystemOne` answers "no method or field Client.SystemOne in package".
- `%T` prints `*engine.SystemOneResponse`, and a user's `PreparedFor[set[typesafe.NoulAnswer]]` error names `…/internal/engine.NoulAnswer`.
- R116's file moves to `internal/engine/decodeas_store.go`.

## 1. Facts reproduced, and three corrections to the charter's facts

A trial move of the 13 files (`package alloctest` with a dot import of the root package, `go test -c -gcflags=all=-e`) reproduces the lead's 29 identifiers:

- **The stages:** `encodeBody` ×13, `headerRedactor` ×7, `decodeSystemOne` ×6, `maxInlineAnswers` ×4, `falsyJSON` ×2, and one each of `readBody`, `request`, `systemOneAlloc`/`newSystemOneAlloc`, `credentials`/`requestCredentials`, `newHeaderRedactor`, `isSecretHeader`, `modelsPath`, `initialUndeclared`, `decodeSystemOneInto`, `callSettings`, `appendState`.
- **The root test helpers:** `newTestClient` ×11, `q3Questions` ×8, `reviewAnswers` ×6, `payloadOf` ×6, `mustPrepared`, `prepareCases`, `prepareSink`, `prettyLevels`, `prettyScale`, `testKey`, `clearEnv`.

Corrections:

1. **A second class of reference, not in the lead's list: reads and writes of unexported fields.**
   - `c.cfg.*` (model, header template, URL, timeout, transport, `maxResponseBytes`, logger, `redactor()`) in `alloc_call_test.go:177-222`, `alloc_call_cases_test.go:46-55`, `alloc_sequence_test.go:106` and `alloc_typed_test.go:82`.
   - `c.systemOneEndpoint` in `alloc_call_test.go:222`.
   - `c.cfg.transport.rt` read *and written* in `alloc_wire_test.go:117-118`.
   - `resp.meta`, `resp.res` and `call.resp` in `alloc_call_test.go:219-222`.
   - `SystemOneResponse{res: x}` literals in `alloc_functional_test.go:188,249,281`.
   - `qs.w.Questions` in `alloc_functional_test.go:51`, `&qs.w` in `alloc_typed_test.go:82`, and `prepareSink.w.Questions` in `alloc_prepare_test.go:78`.

   These need the *state* behind three public types (`Client`, `Prepared`, `SystemOneResponse`), not just stage functions. That is what decides between the designs (section 2).
2. **`alloc_typed_test.go` is not black-box.** It reads `&qs.w` and `c.cfg.model` (`:82`, the reference `Answers()` decode of AC-P3); the compiler hid both behind its undefined-helper errors. Only `alloc_json_test.go` is black-box.
3. **Every shared helper is also used by root white-box tests**, for example `newTestClient` in 13 root test files and `testKey` in 12, so none can simply move. A root internal test cannot import a package that imports the root package (an import cycle), so `internal/alloctest` gets copies (`helpers_test.go` 163 lines, `prepare_cases_test.go` 181 lines; the bridge `bridge_test.go` is 92 more). The root keeps its own.

Other facts:

- **Doc references.** `docs/perf/frozen-budgets.md` names alloc files 24 times on 20 lines, and `docs/perf/ledger.md` 5 times. `docs/port-test-matrix.md` names none in a Go cell; `TestAllocTypedDecode` appears once, in heading prose.
- **Coverage.** CI's `-race -coverprofile ./...` is per-package, with no `-coverpkg` (`ci.yaml:151`).
- **The owner-ratified root changes** that land before W6.5:
  - The R54 (b) per-architecture validator lives in `internal/codec/encode.go:134,168` and is independent of both designs.
  - The Q3 per-write bound (`decodeas_store.go`) and the `Answers` marker (`answers.go`) are root files that D1 leaves in place. D2 moves both with the rest (a `git mv`, so the bound and the marker travel unchanged).

## 2. The candidate designs

### 2.1 D1: `internal/engine` for the stages; public types stay in root (spike `fa26bfa`)

**Package map** (`git diff --stat 93e9db1 fa26bfa`: 65 files, +2 491 −2 119):

| Package | Holds | From |
| --- | --- | --- |
| root `typesafe` | every public type, with its methods and doc comments; the options, errors, retry policy and loop (`send`, `attempt`, `attemptError`), question builder, answers, typed decode (`typed.go`, `decodeas.go`), the unsafe store `decodeas_store.go`; thin unexported shims (`encodeBody`, `readBody`, `safeName`, `headerRedactor = engine.HeaderRedactor`, …) so root call sites barely change | unchanged apart from the shims and accessors |
| `internal/engine` (new, 11 files, 2 115 lines, 236 of them the shield's moved test file) | `text.go` (safe text, `Credentials`, the scrub, `ScrubUserinfo`), `redact.go` (`HeaderRedactor`, `IsSecretHeader`, `RedactedHeaders`), `read.go` (`ReadBody`, NF5 constants, `ErrTooLarge`), `falsy.go` (`FalsyJSON`), `body.go` (`EncodeBody[R ~[]byte, C content[R]]`, `AppendState`, `AppendValue`, `BodyMember`, `Failure`), `decode.go` (`DecodeSystemOne(Into)`, the WARN lines), `client.go` (`Client`, `Config`, `ConfigRef`, `Prepared`, `Response`, `SystemOneAlloc`/`NewSystemOneAlloc`, `Request`/`AttemptHeader`), `transport.go` (`Transport` + `RoundTrip`, the trace shield), `constants.go` | root `text.go`, `redact.go`, `client.go`, `questions.go`, `body.go`, `decode.go`, `config.go`, `transport.go`, `retry.go` |
| `internal/alloctest` (new, test-only) | the 13 files, `bridge_test.go` (the engine bridge), `helpers_test.go` + `prepare_cases_test.go` (helper copies) | root |

**The bridge.** Root `client.go`: `type Client engine.Client`; `prepared.go`: `type Prepared engine.Prepared`; `response.go`: `type SystemOneResponse engine.Response`.

- A defined type shares its underlying struct with the engine type, so `(*engine.Client)(c)` is a free, compile-checked conversion. The public type keeps its method set, which does not inherit the engine's.
- The engine structs keep their fields unexported and give accessor *methods* (`Config()`, `Wire()`, `Result()`, `Meta()`). Those methods are not part of the root type's method set, so no API is added.
- Root reaches its own state through the conversion (`c.eng().Config()`, `p.wirePrepared()`, `r.result()`), each inlined.
- **The body encoder** needs the root's `RawJSON` and `Content` in its type switch, and the engine cannot name them. It is therefore generic: `EncodeBody[RawJSON, Content]`, with cases on the type parameters, and `Content` read through its exported methods (`IsZero`, `Text`, `JSON`; `wire.Content` is exactly `{Text, JSON}`). Root and tests instantiate the same `[RawJSON, Content]`, so both run one compiled function.
- **Failures.** A body failure comes back as `engine.Failure` *by value*. The root wraps it into the same `*ConfigError`/`*InvalidRequestError` it built before, with the same allocations. The engine never constructs a public error type.
- **The retry policy.** `engine.Config.Retry` is `any` holding the root's `RetryPolicy`: one allocation at `NewClient`, a type assertion per call (`call.go:152`). This is the one wart. The alternative is to declare `RetryPolicy` over an engine type too (a 10-method delegation in `retry.go`), which I did not build.

**Which of the 13 files each variant frees without a conversion copy:**

| Variant | Freed | Blocked, and why |
| --- | --- | --- |
| D1 as built (defined-type bridge) | **all 13**, no conversion copy anywhere: production allocates what it allocated (`engine.NewSystemOneAlloc` returns an `engine.Response` inside the call's one block; root converts the pointer), and the tests read the same objects production uses | – |
| The charter's literal D1 (public types stay *struct wrappers* `struct{ w wire.Prepared }`, no bridge) | 3: `alloc_json` (already black-box), `alloc_telemetry` (only pure stages), `alloc_prepare` if its log column `len(prepareSink.w.Questions)` is dropped | 10: every other file needs `*wire.Prepared` from a `*Prepared` (decode, encode, states, sequence, functional, typed, call), the client's config (call, call_cases, wire, sequence, typed) or the response's state (call, functional); no exported, API-neutral route exists. A conversion copy does not help: the *production* call would need one only if the engine allocated the response (root `*SystemOneResponse` from an engine result: +1 allocation, N 12 → 13 against a zero-headroom pin); D1 avoids that by keeping the one allocation. The blocker is test access, not production cost |

**Seam and `unsafe` (R116, K40, STANDING 3).**

- The store stays root `decodeas_store.go`, the only root non-test `unsafe` importer. **R116's location does not move.**
- `internal/engine` has no `unsafe` import and no raw-pointer route. `TestSeamRootRawPointers` already walks every package the root imports (`internal/codec/seam_rawptr_test.go:160-176`), so it checks the engine with no change; it passed on `fa26bfa`.
- STANDING 3's wording should name `internal/engine` explicitly among the no-`unsafe` packages; the test already enforces it.

**K38 / STANDING 1** (`ci.yaml` in `fa26bfa`):

- The glob becomes `grep -l '^//go:build !race' -- *_test.go internal/alloctest/*_test.go`, and the run becomes `run_budgets ./internal/alloctest/ "${root[@]}"`.
- A `!race` test left in the root package is still required, then fails the `-list` guard, which counts `internal/alloctest` only.
- The 16 names are unchanged.
- The §11 lines `go test -list … .` and `go test -run … .` change to `./internal/alloctest/`, as does the (L) command.

**Collateral: root tests that test moved code.** 21 root test files needed edits:

- Accessor spellings and capitalised fields: `p.w` → `p.wirePrepared()`, `rq.url` → `rq.URL`, `.stats()` → `.Stats()`.
- Private helpers of the moved code (`jsonForm`, `quotedForm`, `scrubbedError`, `maxDetailChars`, `msgSkippedAnswer`, `secretHeaderNames`, `hidesText`). The spike exports these and aliases them in a root test file (`engine_shims_test.go`).
- The shield's three unit tests moved to `internal/engine/shield_test.go`.
- One test-only accessor, `HeaderRedactor.Key()`, exists for `redact_test.go:444-447`.

The execution wave should instead **move those tests into `internal/engine`** (list in section 5, commit 1). That keeps the shims out of the root and the accessor out of the engine's production API.

### 2.2 D2: engine and public types in `internal/engine`; root re-exports (spike `61b457a`)

**Package map** (`git diff --stat -M 93e9db1 61b457a`: 79 files, +2 239 −728):

- **Root** is `doc.go`, `api_surface_test.go` and a generated `api.go`: 37 `type X = engine.X` with the engine's doc comments, 23 constants typed by their alias (`const APIErrorAuthentication APIErrorKind = engine.APIErrorAuthentication`), 2 variables, and 35 function wrappers with the original parameter names (generics included: `func Ask[T any](ctx context.Context, c *Client, state any, opts ...CallOption) (T, error) { return engine.Ask[T](ctx, c, state, opts...) }`).
- **Everything else** of the root package, the white-box tests included, is in `internal/engine`.
- **`internal/alloctest`** reaches state through package-level engine functions (`ConfigOf`, `SystemOneEndpointOf`, `ResponseMetaOf`, `ResponseResultOf`, `WireOf`), never methods: a method of an aliased type joins the public API.

**API golden** (`results/golden-d2.diff`): −173 +37. Each of the 37 types becomes one line, for example `type Client = github.com/zchee/typesafe-sdk-go/internal/engine.Client, incomparable`, and every field line and every method line disappears. The golden generator returns early for an alias (`api_surface_test.go:189-191`).

- The comparability markers survive (24 incomparable, 13 comparable, as before; `types.Comparable` looks through the alias). STANDING 10 is met, but its check now sees only the alias line.
- The generator needed a fix: `reflect.TypeFor[Client]().PkgPath()` is the engine's under an alias, so the golden would silently describe the wrong package. The spike names the root path literally.
- `client-options.txt` is unchanged.

**What `go doc` and pkg.go.dev show** (`results/godoc-d2.diff`, `results/godoc-method-probe.txt`):

- `go doc -all .` goes from 1 785 to 1 048 lines. Each type shows `type Client = engine.Client` and its doc comment, and **no method and no field**.
- `go doc . Client.SystemOne` → `doc: no method or field Client.SystemOne in package github.com/zchee/typesafe-sdk-go`.
- Doc links such as `[Client.Close]` in the root's comments become dead text. pkg.go.dev renders from the same `go/doc` model, so every method is documented only on the `internal/engine` page, which a user cannot import.
- Every exported identifier keeps its documented *signature*: function wrappers use the alias names, so `NewClient(opts ...ClientOption) (*Client, error)` reads as before. But the methods' signatures are shown only under the engine.

**User-visible identity** (`results/typeprobe.txt`, same program in each tree):

| Probe | base and D1 | D2 |
| --- | --- | --- |
| `%T` of a response | `*typesafe.SystemOneResponse` | `*engine.SystemOneResponse` |
| `%T` of a `NewClient` error | `*typesafe.ConfigError` | `*engine.ConfigError` |
| `reflect.TypeFor[Client]().PkgPath()` | `github.com/zchee/typesafe-sdk-go` | `…/internal/engine` |
| a user's `set[typesafe.NoulAnswer]` | `main.set[github.com/zchee/typesafe-sdk-go.NoulAnswer]` | `main.set[github.com/zchee/typesafe-sdk-go/internal/engine.NoulAnswer]` |
| `PreparedFor` error text for it | `PreparedFor[main.set[…typesafe-sdk-go.NoulAnswer]]: …` | `PreparedFor[main.set[…/internal/engine.NoulAnswer]]: …` |
| `%#v` of `Answers` | `typesafe.Answers{…}` | `engine.Answers{…}` |

`errors.As` still works, since the types are identical. Logs, panics and error texts that print a type now name an unimportable package.

**Seam and `unsafe`.**

- The store moves to `internal/engine/decodeas_store.go`, and `rootStoreFile`, the importer check (`f.dir == "." || f.dir == "internal/engine"`) and the vacuity check (`internal/engine/decodeas.go`) change (`internal/codec/seam_rawptr_test.go`).
- `TestSeamTransitiveImports`' R41 row, "the root package imports `internal/wire`", must look at `./internal/engine`: the root now imports only the engine (`internal/codec/seam_test.go:375`).
- **R116 ("exactly one root non-test file") must be re-worded and re-ratified**: the root package would have none; the engine exactly one.

**Collateral.**

- All root white-box tests move into the engine unchanged, except 5 whose expected texts name test-local types (`typesafe.celsius` → `engine.celsius`: `body_test.go`, `typed_test.go`, `decodeas_store_test.go`).
- One of those 5 exposes the real user-facing change: an SDK type inside a *user's* generic type name.
- K38 is the same change as D1's. The seam changes are above.

## 3. Measured pin impact (both designs, both hosts)

**Commands.**

- **Allocations:** the frozen rows' own command, ci.yaml's root list of 16 tests, run as one test binary per tree: `-test.run '^(TestAllocPrepare|TestAllocFalsyJSON|TestAllocEncode|TestAllocScratchSequence|TestAllocDecodeFixtures|TestLinearityFlood|TestLinearityFloodTime|TestAllocWholeCall|TestAllocAnswersInlineBound|TestMemStatsCap|TestAllocResponseJSON|TestAllocTypedDecode|TestAllocTypedFailure|TestAllocLoggedCall|TestAllocRequestID|TestAllocSecretHeaderName)$' -test.v -test.count=1`. It runs from package `.` for base and from `internal/alloctest` for D1 and D2 (`_spikes/w6.5-design/allocrun.sh`).
- **Time:** W5.3's interleaved `ab.sh`, base and design binaries alternating, 5 rounds × `-test.count 2`: `-test.bench '^BenchmarkCall$/^(sdk|naive)(-q20)?$' -test.benchmem` (`internal/benchmark`) and `'^BenchmarkDecode$/^(result|result-20)$'` (`internal/codec`, unchanged code: a control).
- **Timestamps:** every file carries its `date`, load, `go version` and ToolTags in its header.

| Run | Host | Started (`date`) | Load 1/5/15 | Result |
| --- | --- | --- | --- | --- |
| alloc base 93e9db1 | (M) | 2026-09-27 02:37:28 JST | 9.05 6.79 5.69 | 16/16 PASS (`alloc-base-M.txt`) |
| alloc D1 fa26bfa | (M) | 2026-09-27 03:01:30 JST | 4.89 7.88 8.01 | 16/16 PASS (`alloc-d1-M.txt`) |
| alloc D2 61b457a | (M) | 2026-09-27 03:01:39 JST | 5.38 7.88 8.01 | 16/16 PASS (`alloc-d2-M.txt`) |
| alloc base | (L) | 2026-09-26 17:38:11 UTC | 0.00 0.00 0.00 | 16/16 PASS (`alloc-base-L.txt`) |
| alloc D1 | (L) | 2026-09-26 18:01:49 UTC | 0.00 0.00 0.00 | 16/16 PASS (`alloc-d1-L.txt`) |
| alloc D2 | (L) | 2026-09-26 18:01:59 UTC | 0.15 0.03 0.01 | 16/16 PASS (`alloc-d2-L.txt`) |
| call base↔D1 | (M) | 2026-09-27 03:01:47 JST | 5.35 → 8.35 | `call-d1-M-{base,cand}.txt` |
| call base↔D2 | (M) | 2026-09-27 03:08:13 JST | 6.86 → 6.91 | `call-d2-M-*` |
| decode base↔D1 | (M) | 2026-09-27 03:03:24 JST | 8.35 → 6.52 | `decode-d1-M-*` |
| decode base↔D2 | (M) | 2026-09-27 03:11:51 JST | 6.50 → 7.49 | `decode-d2-M-*` |
| call base↔D1 | (L) | 2026-09-26 18:02:08 UTC | 0.28 → 0.50 | `call-d1-L-*` |
| call base↔D2 | (L) | 2026-09-26 18:04:35 UTC | 0.78 → 1.26 | `call-d2-L-*` |
| decode base↔D1 | (L) | 2026-09-26 18:03:46 UTC | 0.54 → 0.78 | `decode-d1-L-*` |
| decode base↔D2 | (L) | 2026-09-26 18:06:12 UTC | 1.26 → 1.43 | `decode-d2-L-*` |

**Pins.** Base, D1 and D2 give the same value on (M) and on (L) for every row below. Every value equals the frozen row.

| Pin (frozen) | Value, all three trees, both hosts |
| --- | --- |
| AC-P6 own (12/2 008, zero headroom) | **own = 12/2 008**; floor 8/640 (E_sonic 1/16 + floorRT 7/624); call 20/2 648 |
| AC-P6 composition | header 0/0, `systemOneAlloc` 1/704, `WithTimeout` 4/272, Request 1/320, `body.Open` 1/64, `GetBody` 1/24, `readBody` 1/384, decode 3/240: item for item as W5.3/V68 |
| AC-P6 inline bound | 3 q 21/2 696, 4 q 21/2 888, 5 q 22/2 696 |
| AC-P6 logging (R102) | default 20/2 648, INFO +1/48, DEBUG +3/96 |
| AC-P1 single size, sequence, g₆/g₉ | `TestAllocEncode` and `TestAllocScratchSequence` pass (every asserted `E(kind)+B` and the sequence rows) |
| AC-P2 (4/688, every fixture pin) | `result` 4/688, misses 5/752, naive 26/3 960 (M) / 40/3 672 (L); every pinned fixture equal |
| AC-P3 (0/0 < 4/688) | `DecodeAs` 0/0, `Answers()` decode 4/688, `Ask` 20/2 648 = `SystemOne` 20/2 648 + 0; typed failure 5 |
| AC-P5 minima (i)…(vii) | 32/264 408, 15/1 784, 28/33 560 312, 24/33 302 456, 30/33 560 504, 18/2 360, 18/6 072; every run within its bound |
| AC-P8 allocations | 90 → 685 (members 1 011 → 10 011), ratio 7.61 |
| AC-P8 time ratio (≤ 15) | (M) 9.51 / 9.46 / 9.45; (L) 9.19 / 9.21 / 9.22 (base / D1 / D2) |
| JSON payloads, Prepare, falsy, redaction | every pin equal (`JSON result.json` 1/704, 5/752; `SECRET` 3 and 0; Prepare c1 6 … c5 14) |

**Non-pin differences.** The normalised diff of all 429 measurement lines per run (`n-*` files) shows only:

- Single-run K32 outliers inside a 5-run series. For example, (M) D1 `iv-declared-16MiB` runs 25/33 302 504 once against 24/33 302 456; (L) base `i-…` runs 33/264 472 once. The pins take 3-of-5, so these do not count.
- The recorded `nested-map` scratch capacity, which depends on map order, and recorded `SEQ … maximum` rows.
- In D1, the text of fixtures that do not decode ("does not decode, no budget: …"). D1's alloctest decode bridge returns the codec's error unwrapped, where the root wraps it into `*ResponseValidationError` (recorded, never pinned).

**Time** (benchstat, interleaved, n = 10 per side):

| Benchmark | (M) D1 | (M) D2 | (L) D1 | (L) D2 |
| --- | --- | --- | --- | --- |
| `Call/sdk` (q3) | 4.461 → 4.470 µs, ~ (p = 0.53) | 4.588 → 4.567 µs, ~ (p = 0.67) | 5.551 → 5.555 µs, ~ (p = 0.47) | 5.559 → 5.552 µs, ~ (p = 0.44) |
| `Call/naive` (same run) | 3.924 → 3.793, ~ | 3.843 → 3.812, −0.8 % (p = 0.045) | 6.870 → 6.815, −0.8 % (p = 0.002) | 6.854 → 6.814, ~ |
| `Call/sdk-q20` | 21.45 → 21.51 µs, ~ | 21.61 → 21.81 µs, +0.95 % (p = 0.023) | 22.17 → 22.05 µs, −0.55 % (p < 0.001) | 22.16 → 22.23 µs, +0.33 % (p = 0.037) |
| `Call/*` allocs/op (sdk / naive / sdk-q20 / naive-q20) | 20 / 54 / 41 / 127, all equal | equal | 20 / 68 / 41 / 247, all equal | equal |
| `Decode/result` (control) | 2.954 → 2.970 µs, ~ | 3.007 → 3.020 µs, ~ | 2.792 → 2.790 µs, ~ | 2.792 → 2.791 µs, ~ |
| `Decode/result-20` | 18.39 → 18.28, ~ | 18.76 → 18.75, ~ | 17.83 → 17.83, ~ | 17.81 → 17.82, ~ |

**Reading.**

- Neither design moves `call/sdk` on either host.
- The q20 deltas are under 1 % with opposite signs across designs and hosts, and the naive comparator, which neither design touches, moved as much in the same runs. They are host drift, not a cost.
- AC-P7/G8-b (sdk/naive mean < 1.0 on the CodSpeed runner, amd64) is unaffected: (L) q3 sdk/naive is 0.815 (D1) and 0.815 (D2), against 0.808 and 0.811 for base in the same runs.
- The decode control confirms that the harness sees no difference where the code is identical.

**Seam tests and full suites** (`results/k38-seam-race-M.txt`):

- **Seam tests.** `go test -count=1 -run Seam -v ./internal/codec/` passes in base, D1 and D2 ((M) 2026-09-27 03:23:05–03:23:06 JST). D1 needed no change to the seam tests; D2 needed the two changes of section 2.2.
- **Normal build.** `go test -count=1 ./...` passed on both heads (9/9 packages each; not stamped).
- **`-race`.** `go test -race -count=1 ./...` passed on D1 (finished 2026-09-27 02:54:47 JST) and on D2 (03:23:07 → 03:23:40 JST), 9/9 packages each.

## 4. Coverage consequence (AC-Q2, STANDING 8), measured

All runs on (M), CI's own command (`go test -race -count=1 -coverprofile=… -covermode=atomic ./...`), then `.github/scripts/uncovered-lines.py --profile … --doc docs/uncovered-lines.md` (`results/coverage-M.txt`, `results/coverage-coverpkg-M.txt`, `results/unc*-{d1,d2}.txt`):

| Tree | Scope | Started (`date`) | Total | Checker | What fails |
| --- | --- | --- | --- | --- | --- |
| base | per package (CI today) | 2026-09-27 03:13:17 JST | 96.3 % | rc 0 | – |
| D1 | per package | 03:14:18 JST | **88.6 %** | rc 1, 325 failures | 340 zero-count blocks unlisted (engine code that root tests run counts for no package: `internal/engine/text.go` 107, `body.go` 63, `redact.go` 49, `transport.go` 25, `client.go` 22, `read.go` 19, `falsy.go` 19, `decode.go` 10, `constants.go` 3) + 5 stale rows |
| D2 | per package | 03:14:55 JST | 95.6 % | rc 1, 106 failures | 78 blocks unlisted (the facade's wrappers, which no in-package test calls, and moved code) + 32 stale rows (every row's file moved under `internal/engine/`) |
| base | `-coverpkg=./...` | 03:18:41 JST | 96.6 % | rc 1 | 2 stale rows: blocks now covered by another package's tests (`internal/codec/visitor.go` `OnNull`, `internal/wire/prepared.go` `GrowLevels`) |
| D1 | `-coverpkg=./...` | 03:19:54 JST | 96.7 % | rc 1 | 7 stale + 10 unlisted: 5 rows re-keyed one for one (`readBody`, `falsyJSON`, `credentials.detail`, `credentials.inChain`, `var traceKeys` → their `internal/engine/` names), base's 2, three root shims no production code calls any more (`appendState`, `isCredential`, `retryCountValue`: delete them), and **one new gap**: `EncodeBody`'s failure literal for an extra `"questions"` member (the base shared one `appendMember` site for the three built-in members; restoring a shared site keeps the block structure) |
| D2 | `-coverpkg=./...` | 03:20:38 JST | 96.2 % | rc 1 | 34 stale + 61 unlisted (22 of them facade wrappers in `api.go`; the rest moved rows) |

**Consequence.**

- Under today's per-package scope, D1 moves 340 blocks out of every profile unless the tests of the moved code move with it and cover them from inside the engine. Code reached only through a client call would still need new engine tests or rows. The count is unknown until done, which is the risk.
- Under `-coverpkg=./...`, any test of the module counts toward any package. D1's change is then one-for-one row re-keying plus one real gap.

**Recommendation.** Switch CI's coverage step to `-coverpkg=./...` as W6.5's *first* commit, before any code moves (its cost on base: 2 stale rows, total 96.3 → 96.6 %). Every later commit then carries its own row changes (STANDING 8).

- **Cost:** every test binary instruments every package. The −race coverage run took about 73 s against 61 s, from the start stamps of consecutive runs, one sample each.
- **Meaning:** AC-Q2 comes to mean "covered by some test of the module" rather than "by the package's own tests". That is a ruling for the lead or the owner (section 6).

## 5. Plan amendment draft (for `.omc/plans/typesafe-sdk-go-port.md`; rulings stay authoritative, R104 (2))

- **§4 (layout):**
  - Add to the tree:
    - `internal/engine/`: the call's stages (body encode entry, body read, decode entry, redaction, credential scrub, falsiness check, request and call allocation, transport container and trace shield) and the state behind `Client`, `Prepared` and `SystemOneResponse`. It imports `internal/{codec,wire,h2gate}`, never root, never `unsafe`.
    - `internal/alloctest/` (test-only): the root package's allocation budgets.
  - Package graph: `typesafe → internal/engine → {internal/codec, internal/wire, internal/h2gate}`, and `typesafe → internal/{codec,wire,h2gate}` as before.
  - Replace "Root types are wrapper structs … (aliases would force methods into `wire`)" with: "Root types are wrapper structs over `internal/wire` values, except `Client`, `Prepared` and `SystemOneResponse`, which are defined types over `internal/engine`'s state types (`type Client engine.Client`). The underlying struct is shared, so the conversion is free and compile-checked. The public method set and docs are the root's; the engine's accessor methods do not join the public API. Aliases are not used (W6.5 design D2 rejected: every method would leave the root's documentation and every `%T` would name the internal package)."
  - Seam sentence: "`unsafe` only under `internal/codec`, `internal/testsupport/naive` and the root package's typed store; `internal/engine` has no `unsafe` import and no raw-pointer route (K40)".
- **§6 (performance design; the AC-P6 composition as frozen-budgets.md states it, numbers unchanged):** `context.WithTimeout` 4/272, decode 3/240 (`engine.DecodeSystemOneInto` → the visitor's three fold slices), `engine.ReadBody` 1/384, `*http.Request` from `WithContext` 1/320, the call's own allocation 1/704 (`engine.NewSystemOneAlloc`: the `engine.Response` behind the root's `SystemOneResponse`, the first attempt's URL copy, up to four answer entries), `codec.Body.Open` 1/64, the `GetBody` method value 1/24, header map 0 (R77): N = 12, 2 008 B. §6.1.2's "root body assembly" reads "`engine.EncodeBody`, instantiated with the root's `RawJSON` and `Content`".
- **§7, Phase 6 table, new row:**

  | W6.5 | Engine relocation (owner G9, design D1 of `.omc/handoffs/w6.5-design.md`) | The 13 root `alloc_*_test.go` files run from `internal/alloctest` against the stages in `internal/engine`; the API golden and `client-options.txt` byte-identical; `go doc -all .` differs only in the declaration lines of `Client`, `Prepared`, `SystemOneResponse`; every frozen pin equal on (M) and (L) (AC-P6 own 12/2 008 and its composition item for item, AC-P1, AC-P2 4/688, AC-P3 0/0, AC-P5 bounds, AC-P8), `BenchmarkCall/sdk` within noise, interleaved on both hosts; the seam tests unchanged and green; the K38 step lists `internal/alloctest` and fails with a name removed; `uncovered-lines.py` rc 0 (every commit, STANDING 8); each commit alone (R111) |

- **§8:**
  - AC-P1, AC-P2, AC-P3, AC-P5, AC-P6 and AC-P8 name their tests unchanged; their location reads "`internal/alloctest`" wherever a file is named (frozen-budgets.md's Test column, 24 references on 20 lines).
  - AC-Q2 reads "coverage of the module's tests over each package (`-coverpkg=./...`)" if the ruling of section 6 adopts it.
  - AC-Q4's seam clause adds "`internal/engine` free of `unsafe`".
- **§11:**
  - `ALLOC` lines: `go test -list "^($ALLOC)$" ./internal/alloctest/` and `go test -run "^($ALLOC)$" -count=1 -v ./internal/alloctest/`; likewise for `TestAllocScratchSequence`.
  - The (L) command's alloc part ends `./internal/alloctest/`.
  - The coverage line adds `-coverpkg=./...`.
- **§12:**
  - ci.yaml's non-race step runs `run_budgets ./internal/alloctest/ …`, and its K38 glob reads `*_test.go internal/alloctest/*_test.go` (as in `fa26bfa`).
  - The `-race` coverage step adds `-coverpkg=./...` if ruled.

## 6. Rulings the owner (or lead) must re-ratify

1. **R116 (the one root `unsafe` file).** Under D1 the location does not move: the root package still has exactly one non-test `unsafe` importer, `decodeas_store.go`. Confirm the wording "every package the root imports (except `internal/codec` and `internal/testsupport/naive`)" now covers `internal/engine`; `TestSeamRootRawPointers` already walks it. Under D2 the file would move to `internal/engine/decodeas_store.go` and R116 would need re-wording and re-ratification.
2. **STANDING 3 (K40 as R116 amended).** Add `internal/engine` by name to the packages with no `unsafe` import and no raw-pointer selector. No test change is needed.
3. **R104 (pins with rows).** No pin moves under D1, measured on both hosts. The frozen rows' *Test* cells change only the file they name (`alloc_*_test.go` → `internal/alloctest/alloc_*_test.go`). Rule that such a path edit is not a pin move, so W6.5's commit can update frozen-budgets.md's Test cells without a frozen-value change.
4. **STANDING 1 / K38.** The list's package becomes `internal/alloctest`, and the `!race` glob covers both the root and `internal/alloctest`, so a `!race` test left in the root fails the guard. The 16 names are unchanged.
5. **AC-Q2 / STANDING 8.** Choose the coverage scope:
   - (a) `-coverpkg=./...` (recommended; measured cost above), or
   - (b) keep per-package: W6.5 moves the tests of every moved stage into `internal/engine` and adds rows for what only client-level tests reach (340 blocks to account for; count unknown).
6. **Plan v3's "wrapper structs instead of aliases".** Amended for three types by the defined-type rule of section 5. The owner sees the go doc change: `type Client engine.Client` in place of `type Client struct { // Has unexported fields. }`, same for `Prepared` and `SystemOneResponse`.

## 7. Alternatives considered and rejected

| Alternative | What it is | Why not |
| --- | --- | --- |
| D1 with struct wrappers (the charter's literal D1) | stages in `internal/engine`, public types stay `struct{ w … }` | frees 3 of 13 files (section 2.1); no API-neutral route from a public value to its state |
| D1δ: accessors registered at init | root's `init` stores `func(any) *wire.Prepared` and the like in an engine variable; tests call them | `go doc` byte-identical, but it puts test-only hooks and a mutable global in the production binary, checked only at run time (a type assertion inside the hook) |
| ρ: reflect in the test package | `reflect.ValueOf(qs).Elem().FieldByName("w")` then `UnsafePointer()` in `internal/alloctest` | passes today's seam tests (they read non-test files for raw-pointer routes, and `TestSeamImports` forbids only an `unsafe` *import*), but it is K40's banned route by another door, and a field rename becomes a run-time failure |
| D1-min | a defined type for `Prepared` only; tests rebuild the client's config from a recorded request | one go doc line instead of three, but `alloc_wire_test.go` must stop wrapping the SDK's own transport (it writes `c.cfg.transport.rt`) and use `WithRoundTripper` around an `h2gate` transport instead, a semantic change to the test; `measureCallItems` would read a reconstruction, not the client |
| `RetryPolicy` over an engine type | removes `Config.Retry any` | a 10-method delegation in `retry.go` for one field the tests never read; offered as a follow-up if the owner dislikes `any` |

## 8. Recommendation

**D1.**

- **Allocations:** it costs 0 allocations and 0 B on every pin, on both hosts. `call/sdk` changes by +0.2 % (M) and +0.07 % (L), both p > 0.4; q20 is within the naive comparator's own drift.
- **API presentation:** it costs three `go doc` declaration lines naming `internal/engine`. Nothing else changes: the golden, `client-options.txt`, the comparability markers, `%T`, `reflect` names and error texts are all identical.
- **Seam:** R116's file does not move.
- **Why not D2:** D2 buys nothing D1 does not: the pins are equally unchanged. It costs every method's documentation at the import path users read, a 210-line golden rewrite that no later wave can take back once v0.1.0 ships, internal package names in `%T` and in `PreparedFor` errors, and an R116 re-ratification.
- **Carried by D1:**
  - `engine.Config.Retry` is an `any` (one allocation at `NewClient`, not pinned).
  - About 344 lines of test helpers are copied into `internal/alloctest`. A drift there moves a pinned count, so it fails loudly.
  - The coverage scope must be decided first (section 4).

### Execution plan for the W6.5 lane (after W6.1's test-only head, the Q3 per-write bound, the R54 (b) per-architecture validator and the `Answers` marker have landed; rebase onto that main)

Every commit is gated alone (R111): `go build ./... && go vet ./... && go test -count=1 ./...`, `-race`, the §11 lint chain, the allocation-budget list at the package that holds it at that commit (root until C7, `internal/alloctest` from C7) under the lock on (M), and `uncovered-lines.py` rc 0 on a fresh (M) profile (STANDING 8). All run under `set -o pipefail` with the FAIL count reported. The R104 statement in every commit message: no pin moves; if one does, stop and ask.

| # | Commit (intent line) | Content | STANDING duties |
| --- | --- | --- | --- |
| C0 | `ci: count every test of the module toward each package's coverage` | `ci.yaml` `-race` step `-coverpkg=./...`; `docs/uncovered-lines.md` drops the 2 rows now covered (`OnNull`, `GrowLevels`) | 8 (checker rc 0, both rows' classes); needs ruling 6.5 first |
| C1 | `engine: move the text, redaction and credential stages out of the root` | `internal/engine/{text,redact,falsy,constants}.go` (`SafeName`, `AppendSafeText`, `Credentials`, `ScrubUserinfo`, `HeaderRedactor`, `IsSecretHeader`, `RedactedHeaders`, `FalsyJSON`, retry-count table, paths); root keeps unexported shims; the tests of these stages (root `text_test.go` credential/scrub cases, `redact_test.go`, `redact_fold_test.go`, `falsy_test.go`) move to `internal/engine` instead of the spike's `engine_shims_test.go`, with no production accessor for tests (drop the spike's `HeaderRedactor.Key`) | 3 (seam test green, `internal/engine` free of `unsafe`), 8 (3 rows re-keyed) |
| C2 | `engine: move the response body read` | `internal/engine/read.go` (`ReadBody`, the NF5 constants, `ErrTooLarge`); `readBody`'s direct tests move | 8 (1 row re-keyed); AC-P5 run |
| C3 | `engine: move the transport container and its trace shield` | `engine.Transport` (+`RoundTrip`, `Close`, `Stats`, shield, `untracedContext`); root `roundTrip(t, req, timeout)` maps errors (`transportError` stays root); the shield's 3 unit tests move | 8 (`var traceKeys` row re-keyed); AC-P4 `internal/h2gate` list unchanged |
| C4 | `engine: encode the request body in engine for the root's RawJSON and Content` | `engine.EncodeBody[R, C]`, `AppendState`, `AppendValue`, `BodyMember`, `Failure` by value; root `encodeBody` maps `Failure` to the same `*ConfigError`/`*InvalidRequestError`; keep one shared failure site for the three built-in members, so no new zero block | AC-P1 list; 8 |
| C5 | `engine: move the decode entry` | `engine.DecodeSystemOne(Into)` + the WARN lines; root wraps into `*ResponseValidationError` | AC-P2/P8 lists |
| C6 | `root: keep the client's, question set's and response's state in engine` | `type Client engine.Client`, `type Prepared engine.Prepared`, `type SystemOneResponse engine.Response`; `engine.Config`/`ConfigRef` (R66's two pointers kept), `Request`/`AttemptHeader`, `NewSystemOneAlloc`; `Config.Retry any`; delete the 3 dead shims | **10**: the API golden and `client-options.txt` byte-identical (the test is the proof); a `types.Comparable` table before/after for the three types plus all 37 (golden markers); `go doc -all .` diff recorded (exactly the 3 declaration lines); R66's fmt-verb pins green; AC-P6 own = 12 with the composition's ITEM line |
| C7 | `alloctest: run the root allocation budgets from internal/alloctest` | `git mv` the 13 files; `bridge_test.go`; helper copies (`helpers_test.go`, `prepare_cases_test.go`) naming their originals; qualify root identifiers if `revive`'s dot-import rule fires (see the lint row in section 9); `ci.yaml` K38 glob and `run_budgets ./internal/alloctest/`; frozen-budgets.md Test cells (24 references) per ruling 6.3; port-test-matrix heading prose (optional) | **1**: the step's bash run locally, and again with one name removed (must fail with `::error::…missing from the list`), then an R90 dispatch at the head; every moved test names the CI step that runs it (16 in the budget step; the functional halves in the `-race` step) |
| C8 | `ledger: record W6.5's relocation with every pin unchanged` | `docs/perf/ledger.md` `## W6.5`: rows W6.5-01… for the allocation list and interleaved `BenchmarkCall`/`BenchmarkDecode` on (M) and (L) (this report's commands), raw files under `_spikes/w6.5/` | 5 (every row matches its raw file), 6 (`date` in the command, lock, experiments) |

The review charter should add the STANDING items above, plus this lane's section 4 table as the coverage check and section 2.2's type probe as a negative control (it must print `typesafe.` names after C6).

## 9. Other evidence, deviations, open questions

**K38 step (STANDING 1 (b)), D1** (`results/k38-seam-race-M.txt`, (M) 2026-09-27 03:22:57 JST, under the lock). ci.yaml's "go test without -race (allocation budgets)" step, extracted from `fa26bfa`'s ci.yaml and run as written:

- As written: rc 0, 20 PASS (16 from `internal/alloctest` and 4 from `internal/codec`).
- With `TestAllocRequestID` removed from the list: rc 1, `::error::tests built only without -race missing from the list: TestAllocRequestID`.

**AC-Q1 chain on D1** (`results/lint-d1-M.txt`, 2026-09-27 03:25:22 JST): gofumpt clean, modernize rc 0, `go mod tidy -diff` rc 0. golangci-lint reports 17 findings and staticcheck 6, all spike artefacts, and the execution plan removes each:

- `unused` ×6: five root shims that nothing calls once the tests have moved (`memberState`, `appendState`, `maxInlineAnswers`, `isCredential`, `retryCountValue`: delete them), plus one field of the moved shield test.
- `revive` ×8:
  - dot imports in `internal/alloctest`: qualify the names;
  - four `FailX` comment forms;
  - `NewShield` returning an unexported type: moving the shield tests keeps `newShield` unexported.
- `govet composites` ×3: unkeyed `bodyMember` literals in `body_test.go`, now an imported type.

`govulncheck` does not load packages here on base either ("possibly due to a mismatch between the Go version used to build govulncheck and the Go version on PATH"). It is an environment fault, not measured for either design.

**Deviations from the charter.**

1. D2 was built on the base tree and committed after D1 on the same branch. The branch holds `93e9db1 → fa26bfa (D1) → 61b457a (D2, whose tree is base + D2) → evidence`; `git diff 93e9db1 61b457a` is D2 alone.
2. The base, D1 and D2 trees used for measurement are `git archive` exports under my own scratchpad, so no extra worktree or branch touches the repository. They were copied to (L) by tar pipe as the charter says.
3. The (M) runs started at 1-minute loads of 4.9 to 12.3. The coverage runs are not timing runs, and one of them started at 12.32. Every timing run started under 12 (`waitfree` in `batch.sh`), was interleaved base/candidate, and carries its load before and after.
4. The coverage and lint runs took the bench lock although they are not timing runs, to keep them from disturbing other lanes' timing.
5. D1's spike keeps 21 root test files working through exported engine internals and a root test file of aliases (`engine_shims_test.go`), and adds one test-only accessor (`HeaderRedactor.Key`). That was spike expediency; the execution plan moves those tests instead (C1–C3).

**Open questions for the lead.**

- (a) Section 6.5's coverage scope. It decides whether C0 exists.
- (b) Keep `Config.Retry any`, or declare `RetryPolicy` over an engine type as well?
- (c) Can frozen-budgets.md's Test cells take the new paths under R104 without a frozen-value ruling (section 6.3)?
- (d) Should `docs/port-test-matrix.md`'s heading note that mentions `TestAllocTypedDecode` name the package?
