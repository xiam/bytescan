# bytescan documentation

This directory is the documentation home for `bytescan`.

- [specs/core-contract.md](specs/core-contract.md) is the index of the
  **canonical prose specification**: the project purpose, the status
  convention, the terminology, and a map from every former section to its
  document. The specification is split into eight documents under
  [`specs/`](specs/core-contract.md#documents):
  - [consumer-contract.md](specs/consumer-contract.md): what `Next()`
    returns, the scanner/consumer boundary, and illustrative scenarios;
  - [rule-vm-contract.md](specs/rule-vm-contract.md): how the engine
    decides, and what a custom rule must do;
  - [composition.md](specs/composition.md): the construction capabilities of
    `rules/` and the composition contract of `rules/core`;
  - [resources-and-streams.md](specs/resources-and-streams.md): bounds,
    memory, long-running streams and concurrency;
  - [layers.md](specs/layers.md): filters, lexical modes and byte reads
    above `Next()`;
  - [design-principles.md](specs/design-principles.md): the VM design
    principles and the decision log;
  - [open-questions.md](specs/open-questions.md): the open design questions;
  - [cursor.md](specs/cursor.md): the positioned cursor, `bytescan/cursor`.

  Every substantive statement carries an explicit status — **design
  principle**, **current contract**, **intended capability**, or **open design
  question** — so the engine available today never reads as if it includes
  capabilities that are still to be designed.
- [rule-authoring.md](rule-authoring.md) is the **rule-authoring standard**:
  how to write a rule and how to check one, with a reviewer checklist. It is
  normative for every rule in this repository and is guidance for consumers.
- [rules.md](rules.md) is the **rule tier model**: what belongs in
  `rules/core`, `rules/base`, `rules/special` and `rules/octet`, and the
  import rules.
- [writing.md](writing.md) is the **writing guide**: the three depths of the
  documentation (README, `docs/`, `docs/specs/`), what belongs in each, and
  the plain-language rules for READMEs.
- [cursor.md](cursor.md) is the **cursor how-to**: reading ahead and going
  back, limiting memory, reusing checkpoints, rune sources and byte mode.
- [guides/README.md](guides/README.md) is the **technique chooser**: given a
  format, which technique to use (rules only, a filter, a mode driver, a
  driver read, piped scanners), and one guide per use case, from programming
  languages to nested formats, with an
  [operations page](guides/operations.md) on bounds, `Release`, retention and
  soak tests, and a [how-it-works page](guides/how-it-works.md) on the parts,
  the constructors, token positions and context.
- The example programs live in a separate repository,
  [bytescan-examples](https://github.com/xiam/bytescan-examples). Its README
  is the **example catalogue**, grouped by use case, and the **example
  standard**: the layout and conventions of the example scanners.
- The [root README](../README.md) is the project announcement: what the
  project does, installation, a text and a binary example, what you can
  build, the packages, and links here. [writing.md](writing.md) says what
  belongs in it.

## How to read the status labels

The core contract gives every statement one of four statuses: a design
principle, which binds the engine, and a three-level promise ladder below it:

| Status | Meaning |
| --- | --- |
| **Design principle** | Non-negotiable rule for the design of the engine. It binds every engine change; current conformance is recorded separately, as current contract or as a **Known deviation**. |
| **Current contract** | Exists in code at `master`, exercised by committed tests. Consumers may rely on it. A named test or a source symbol is cited; a gap is marked **no test yet**. |
| **Intended capability** | Owner-intended, recorded at the semantic level so later work is decidable. **Not** available API; no signature, data structure, or construction order is approved by the statement. |
| **Open design question** | Genuinely undecided. The document names the question and the constraint it must satisfy; it does not choose an answer and does not pretend an answer has owner approval. |

A substantive promise with no status is a documentation bug — flag it.

## Where things live

- **Specification.** [specs/core-contract.md](specs/core-contract.md), the index of
  the specification documents.
- **Rule-authoring standard.** [rule-authoring.md](rule-authoring.md).
- **Runnable examples.** The two README examples, one for text and one for
  binary data, run as `Example` functions in
  [`example_readme_test.go`](../example_readme_test.go); the examples of
  [`cursor/README.md`](../cursor/README.md) and [cursor.md](cursor.md) run in
  [`cursor/example_readme_test.go`](../cursor/example_readme_test.go) and
  [`cursor/example_docs_test.go`](../cursor/example_docs_test.go).
  [hello](https://github.com/xiam/bytescan-examples/tree/main/hello) in the
  [bytescan-examples](https://github.com/xiam/bytescan-examples) repository is
  the text example as a complete program, and the README of that repository
  catalogues every example program. The code blocks of the [guides](guides/README.md) run as
  `Example` functions in
  [`example_guides_test.go`](../example_guides_test.go).
- **Tests that pin the contract.** The public-API test suite under `tests/` is
  the executable evidence the specification cites by test name: the scanner,
  cursor, context, error and source contracts (the `scanner_*_test.go` files,
  `cursor_test.go`, `context_test.go`, `errors_test.go`, `source_test.go`);
  the oracle tests that check each rule against its documented regular
  expression (`*_oracle_test.go`, `regexp_test.go`); and the memory tests
  (`soak_test.go`, `alloc_test.go`, `open_token_test.go`, `reuse_test.go`).
  [`tests/README.md`](../tests/README.md) says which file tests which
  behaviour. Tests of a package live next to it, for example in `modes`,
  `bytesource` and `streamtest`.
- **Upstream cursor.** Stream mechanics (reading, buffering, positions,
  checkpoints, replay) come from
  [`github.com/xiam/bytescan/cursor`](../cursor), which reads text, or bytes
  through [`bytesource`](../bytesource); the scanner owns
  no stream mechanics. The cursor's contract is
  [specs/cursor.md](specs/cursor.md).

Keep the README and the core contract as paired documentation: when one changes,
review the other in the same change for consistency.
