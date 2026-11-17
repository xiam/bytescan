# Tests

This directory holds the public-API tests of `bytescan`: they use only the
exported API, as a user does. The specification (the documents under
`docs/specs/`, indexed by
[`docs/specs/core-contract.md`](../docs/specs/core-contract.md)) cites them by
test name, so a test name is part of the contract. Keep a name when you move a
test, or change the citation in the same change.

## Where a behaviour is tested

| Behaviour | Files |
| --- | --- |
| Matching: longest match, rule priority, `UNKNOWN`, the BOL flag, many rules | `scanner_matching_test.go` |
| Buffer growth, compaction and resource limits | `scanner_buffer_test.go` |
| `Next` and `AddRule` from many goroutines | `scanner_concurrency_test.go` |
| Reader errors and `AddRule` errors; lexing after an error | `scanner_errors_test.go` |
| Error identities (`errors.Is`) and `Next` after `Release` | `errors_test.go` |
| Rules that panic | `scanner_panic_test.go` |
| Adversarial rules: deep backtracking, zero-length matches, chaos | `scanner_pathological_test.go` |
| End of input: the EOF symbol, rules still active at the end | `scanner_eof_test.go` |
| Rule states: `StatePushBack` order and scope, pushback chains, invalid states | `scanner_rule_states_test.go` |
| Lookahead built from pushback | `scanner_lookahead_test.go` |
| The cursor: spans, `MaxTokenBytes`, shared cursors, `Context` | `cursor_test.go` |
| Live heap of the line table on a long stream of newlines | `line_table_test.go` |
| Context: preceding tokens, retention, failed `Next` | `context_test.go` |
| Mark reuse and `Release` on a switch | `reuse_test.go` |
| One stack for the open token, and its live heap | `open_token_test.go` |
| `Source`, `Skip` and `WithType` | `source_test.go` |
| Allocations per token | `alloc_test.go` |
| Long-running streams (soak) | `soak_test.go`, `soak_gen_test.go` |
| Hand-written rules against Go's `regexp` | `regexp_test.go` |
| Rules under `rules/` against Go's `regexp` (oracle tests) | `core_oracle_test.go`, `core_combinator_oracle_test.go`, `base_*_oracle_test.go`, `special_oracle_test.go`, `octet_oracle_test.go` |
| Benchmarks | `bench_test.go`, and `BenchmarkScannerPerformance` in `scanner_buffer_test.go` |

The hand-written rules that the `scanner_*_test.go` files share are in
`scanner_rules_test.go`. They follow
[`docs/rule-authoring.md`](../docs/rule-authoring.md). `cursor_test.go` holds
more rules that the cursor tests and some other files share.

## Tests in other places

- **Next to the rules.** `rules/core`, `rules/base`, `rules/special` and
  `rules/octet` test their rules with complete token streams in their own
  `*_test.go` files. A test that needs Go's `regexp` as an oracle lives here
  instead, because no file under `rules/` imports `regexp` (see
  [`docs/rules.md`](../docs/rules.md)).
- **Internal tests at the root.** `processor_internal_test.go` and
  `symbol_internal_test.go` test unexported code of the root package.
- **Packages.** `bytesource`, `modes` and `streamtest` test themselves.
- **Example programs.** The examples live in the separate
  [bytescan-examples](https://github.com/xiam/bytescan-examples) repository
  and test themselves there.

## Shared helpers

[`internal/lextest`](../internal/lextest) holds the helpers that this package
and the packages under `rules/` share: a token stream as a list of
`"TYPE \"text\" @offset"` entries (`LexStream`, `Collect`, `Token`,
`Unknowns`), a table runner for complete streams (`RunStreamCases`), and
`RaceDetector`. A package binds them to short local names in its
`helpers_test.go`.

## Where a new test goes

1. Find the behaviour in the table above and add the test to that file. For a
   new matching case, add a case to the table of `TestScannerProcessor`; for an
   adversarial rule, add a subtest to `TestScannerPathologicalRules`.
2. Write a new rule in `scanner_rules_test.go`, following
   `docs/rule-authoring.md`, and add conformance cases for it (practice 9).
3. A test of one rule of `rules/` goes next to that rule, unless it needs the
   `regexp` oracle.
4. Check both what is expected and what must fail, and say in a comment which
   case the test pins.
5. Keep each file under 1,000 lines. When a file grows past that, split it by
   behaviour and update the table above.
