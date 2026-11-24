package bench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"

	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/streamtest"
)

// The case cjk measures text whose runes are mostly above U+00FF: generated
// CJK prose with punctuation and numbers, under a pure core rule set. A rule
// of a table steps such a rune outside the full rows of its table
// (docs/specs/table-compiler.md, "The symbol classes"). It is not a reference
// case of docs/specs/measurement.md: a comparison names it in -cases, and the
// default run of the harness does not run it.

// unicodeCases returns the cases whose input is rich in runes above U+00FF.
func unicodeCases() []Case {
	return []Case{
		{
			name:      "cjk",
			ruleSet:   "runs of letters, numbers, punctuation, symbols and spaces of the Unicode categories (cjkRules)",
			source:    "CJK prose generated in memory from seed 7 by cjkRecord, 512 KiB, read four times in a row",
			rules:     cjkRules,
			newCursor: textCursor,
			input:     cjkInput,
		},
	}
}

// cjkRules is the rule set of the case cjk: one pure core rule for each
// Unicode category of the input.
func cjkRules() []rule {
	number := core.RuneRun(core.UnicodeNumber)
	return []rule{
		{"WORD", core.RuneRun(core.UnicodeLetter)},
		{"NUMBER", core.Sequence(number, core.Optional(core.Sequence(core.Literal("."), number)))},
		{"PUNCT", core.RuneRun(core.UnicodePunct)},
		{"SYMBOL", core.RuneRun(core.UnicodeSymbol)},
		{"SPACE", core.RuneRun(core.UnicodeSeparator.Or(core.ASCIISpace))},
	}
}

// The prose of the case cjk is the output of cjkRecord from seed 7 with a
// budget of cjkProseBudget bytes. It is generated in memory, not committed,
// and its size and SHA-256 pin it, so a change of the generator shows
// (TestCJKInputPinned).
const (
	cjkProseBudget = 1 << 19
	cjkProseSize   = 524628
	cjkProseSHA256 = "1bf8d987fb0e71644d25831d140b87e534c0414711d995034815fae194066c96"
)

// cjkInput returns the prose of the case cjk four times in a row, after it
// checks the size and the SHA-256 of the prose.
func cjkInput() ([]byte, error) {
	doc, err := io.ReadAll(streamtest.NewGenerator(7, cjkRecord, streamtest.WithBudget(cjkProseBudget)))
	if err != nil {
		return nil, fmt.Errorf("bench: the prose of cjk: %w", err)
	}
	sum := sha256.Sum256(doc)
	if len(doc) != cjkProseSize || hex.EncodeToString(sum[:]) != cjkProseSHA256 {
		return nil, fmt.Errorf("bench: the prose of cjk has %d bytes and SHA-256 %x, want %d bytes and %s",
			len(doc), sum, cjkProseSize, cjkProseSHA256)
	}
	return bytes.Repeat(doc, 4), nil
}

// The runes of cjkRecord.
const (
	// cjkHan is the number of ideographs, from U+4E00 on, that the prose
	// uses. A Zipf law picks them, as the characters of real prose are
	// distributed: a few are frequent, most are rare.
	cjkHan = 2500
	// cjkKana is the hiragana U+3041 to U+3096.
	cjkKanaFirst, cjkKanaLast = 0x3041, 0x3096
)

// cjkRecord appends one paragraph of CJK prose: an ideographic space, then
// sentences of clauses of ideographs and kana, separated by CJK commas and
// ended by a CJK full stop, question or exclamation mark, with quotes,
// parentheses, numbers (ASCII, decimal and fullwidth), percent signs, a few
// Latin words and symbols, and a line feed.
func cjkRecord(rng *rand.Rand, _ int, dst []byte) []byte {
	han := rand.NewZipf(rng, 1.07, 2, cjkHan-1)
	b := bytes.NewBuffer(dst)
	text := func(n int) {
		for i := 0; i < n; i++ {
			if rng.Intn(6) == 0 {
				b.WriteRune(rune(cjkKanaFirst + rng.Intn(cjkKanaLast-cjkKanaFirst+1)))
				continue
			}
			b.WriteRune(rune(0x4E00 + han.Uint64()))
		}
	}
	b.WriteRune('　')
	for s, n := 0, 2+rng.Intn(6); s < n; s++ {
		for c, m := 0, 1+rng.Intn(4); c < m; c++ {
			if c > 0 {
				b.WriteString([]string{"，", "、", "；", "："}[rng.Intn(4)])
			}
			text(2 + rng.Intn(12))
			switch rng.Intn(14) {
			case 0:
				fmt.Fprintf(b, "%d", 1900+rng.Intn(200))
				text(1)
			case 1:
				fmt.Fprintf(b, "%d.%d%%", rng.Intn(100), rng.Intn(10))
			case 2:
				b.WriteString("「")
				text(2 + rng.Intn(6))
				b.WriteString("」")
			case 3:
				b.WriteString("（")
				text(1 + rng.Intn(5))
				b.WriteString("）")
			case 4:
				b.WriteString([]string{"AI", "Go", "HTTP", "Unicode", "CPU"}[rng.Intn(5)])
			case 5:
				for _, d := range fmt.Sprint(rng.Intn(1000)) {
					b.WriteRune('０' + d - '0')
				}
				text(1)
			case 6:
				b.WriteString([]string{"〒", "￥", "→", "×"}[rng.Intn(4)])
			}
		}
		b.WriteString([]string{"。", "。", "。", "？", "！", "……"}[rng.Intn(6)])
	}
	b.WriteByte('\n')
	return b.Bytes()
}
