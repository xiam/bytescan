# Unicode emoji data

These files are unmodified copies of the Unicode emoji data files of
Unicode 18.0.0 (UTS #51, Unicode Emoji, version 18.0):

| File | Source |
|------|--------|
| `emoji-data.txt` | https://www.unicode.org/Public/18.0.0/ucd/emoji/emoji-data.txt |
| `emoji-sequences.txt` | https://www.unicode.org/Public/18.0.0/emoji/emoji-sequences.txt |
| `emoji-zwj-sequences.txt` | https://www.unicode.org/Public/18.0.0/emoji/emoji-zwj-sequences.txt |
| `LICENSE.txt` | https://www.unicode.org/license.txt |

`internal/gen` reads `emoji-data.txt` and writes `rules/special/emoji_table.go`;
`go generate ./rules/special/...` reproduces that file from this copy without
network access. The tests of package `special` read `emoji-sequences.txt` and
`emoji-zwj-sequences.txt` and check that `Emoji` matches every RGI emoji
sequence of this version as one token.

To update, download the four files of the new version from the same paths,
run `go generate ./rules/special/...`, and run the tests.

The files are distributed under the Unicode License v3 in `LICENSE.txt`.
