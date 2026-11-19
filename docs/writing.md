# Writing the documentation

This page says where a piece of documentation goes and how to write it. It
applies to every Markdown file in this repository.

## Three depths

A reader goes as deep as they need, and no deeper. Each depth links to the
next one instead of repeating it.

| Depth | Where | What it holds | Who reads it |
| --- | --- | --- | --- |
| 1. Announcement | `README.md`, and the README of an important package: [`cursor`](../cursor/README.md), [`rules`](../rules/README.md), [`bytesource`](../bytesource/README.md) | What the code does, one or two short examples with their output, what you can build, and links. | Someone deciding whether to use bytescan, with a minute to spare. |
| 2. How to use it | `docs/`: the [guides](guides/README.md), [rules.md](rules.md), [rule-authoring.md](rule-authoring.md), [cursor.md](cursor.md), [how-it-works.md](guides/how-it-works.md) | How to build a scanner, which rules to use, how to write a rule, the traps, and what to test. | Someone building a scanner. |
| 3. Exact contract | `docs/specs/` | What the code does exactly, with a status for every statement and the tests that check it. | Someone who relies on an edge case, or changes the engine. |

When a fact is long, rare or exact, it belongs one depth further down. A
README links to it: "To know more about rules, see [rules.md](rules.md)."

## READMEs

A README is how the project introduces itself. Write it for a person with
little time.

Only the most important import paths get a README: the root, `cursor/`,
`rules/` and `bytesource/`. The other packages rely on their package
documentation (`go doc`) and on the guides. Add a README to a package only when
a reader is likely to open its directory first.

- **A signpost.** Every README says what is here and where to look next. A
  directory README, such as [`rules/README.md`](../rules/README.md), is a map
  of its subdirectories: one or two sentences each, then links to the deeper
  pages.
- **Plain language.** Short sentences. Everyday words. One idea per sentence.
  Explain a technical word the first time, or link to a page that does.
- **Show, then tell.** Every example is short, complete and followed by its
  output. The README examples run as `Example` functions
  (`example_readme_test.go` next to each README, such as
  [the root one](../example_readme_test.go)), so a change to a README example
  is a change to its test. A package README may show only the body of `main`;
  its `Example` function holds the same code with its imports.
- **Show what it can do.** Say what a reader can build, and link to a program
  that does it.
- **Link, do not explain.** A complicated topic gets one sentence and a link to
  `docs/` or `docs/specs/`, not a section.
- **No history.** No old names, no renames, no migration steps. The release
  notes carry those.
- **No specification.** No full lists of capabilities or limits, no exact edge
  cases, no internal design. Those live in `docs/` and `docs/specs/`.
- **Short.** The root README stays near 150 lines, a package README near 100,
  and a signpost README such as `rules/README.md` near 40, plus its one
  example.

## How-to pages in `docs/`

- Lead with the task: what the reader wants to do, and the shortest way to do
  it.
- Use the same plain language as a README. A how-to page may be longer, and it
  may name the API.
- Every Go code block compiles and runs. The function that holds it is named
  next to the block, most of them in
  [`example_guides_test.go`](../example_guides_test.go).
- Link to the exact contract in `docs/specs/` instead of restating it.

## Specifications in `docs/specs/`

- Every substantive statement carries a status: design principle, current
  contract, intended capability or open design question. The
  [status convention](specs/core-contract.md#status-convention) defines them.
- A current-contract statement cites the test or the source symbol that backs
  it.
- A spec section and the prose that restates it change together: when one
  changes, review the other in the same change.

## Links in published files

Published files are every file that the release exports: the README, `docs/`,
`docs/specs/`, the code and the tests. They link to no internal host, and
internal context goes into tickets, not into published text
(`TestPublishedFilesLinkNoInternalHost`).

## Moving a fact

When you remove a fact from a README, keep it: move it to the page of the
right depth, or check that such a page already holds it. Say where it went in
the merge request.
