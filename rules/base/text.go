package base

import (
	"fmt"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// wordJoiners are the runes that join two parts of a word: the apostrophes '
// (U+0027) and ’ (U+2019), the hyphen-minus - (U+002D) and the hyphen ‐
// (U+2010). A joiner never starts or ends a word.
const wordJoiners = "'\u2019-\u2010"

// wordStartClass is the class of the first rune of a word part: a letter,
// \pL.
var wordStartClass = core.UnicodeLetter

// wordInnerClass is the class of the runes of a word part after its first
// rune: a letter or a combining mark, [\pL\pM].
var wordInnerClass = core.UnicodeLetter.Or(core.UnicodeMark)

// Word returns a rule for a word of human text:
//
//	\pL[\pL\pM]*(?:['’\-‐]\pL[\pL\pM]*)*
//
// A word is a letter followed by letters and combining marks, and an
// apostrophe or a hyphen joins two such parts: "don't", "l'été", "well-known",
// and "e" + U+0301 (an NFD "é"). A joiner never starts or ends a word: in
// "'word'" the word is "word", and in "well--known" the words are "well" and
// "known". At end of input after a letter or a mark it ends with the word read
// so far; at end of input after a joiner it ends with the word before the
// joiner, which it excludes with a pushback chain.
//
// See the package documentation for how Word approximates UAX #29.
//
// Cost: linear in the token length. The joiners and marks are a repetition
// of core rules with no lookahead, so the runs of each part are merged. On
// one word of 1,025 and of 16,385 runes, with or without marks or joiners,
// Word takes about twice the time per rune of core.RuneRun(UnicodeLetter),
// and the time per rune grows by less than a quarter (BenchmarkWord).
func Word() bytescan.Rule {
	part := core.Sequence(
		core.Rune(wordStartClass),
		core.ZeroOrMore(core.Rune(wordInnerClass)),
	)
	return core.Sequence(
		part,
		core.ZeroOrMore(core.Sequence(
			core.Rune(core.RuneSet(wordJoiners)),
			part,
		)),
	)
}

// Punctuation returns a rule for one punctuation rune: \pP. It accepts that
// rune and stops. At end of input before that rune it matches nothing.
func Punctuation() bytescan.Rule {
	return core.Rune(core.UnicodePunct)
}

// PunctuationRun returns a rule for a run of punctuation runes: \pP+, such as
// "...", "?!" or "»,". At end of input it ends with the run read so far.
func PunctuationRun() bytescan.Rule {
	return core.RuneRun(core.UnicodePunct)
}

// Symbol returns a rule for one symbol rune: \pS, such as "$", "+" or "©".
// It accepts that rune and stops. At end of input before that rune it matches
// nothing.
func Symbol() bytescan.Rule {
	return core.Rune(core.UnicodeSymbol)
}

// ScriptRun returns a rule for a run of text in one script, for scripts that
// write words without spaces, such as Han or Thai:
//
//	\p{script}[\p{script}\pM]*
//
// script is a name of unicode.Scripts, such as "Han" or "Thai". The run starts
// with a rune of the script and continues with runes of the script and
// combining marks of any script. It includes the digits and punctuation that
// Unicode assigns to the script, such as the Thai digits; it does not include
// the punctuation that scripts share, such as "。" (U+3002), which is in the
// Common script. It does not segment the run into words. At end of input it
// ends with the run read so far. It panics if script is not a name of
// unicode.Scripts.
//
// Each class of extra may continue the run but never starts it, so with
// extra classes the run is \p{script}[\p{script}\pM<extra>...]*. For
// example, the prolonged sound mark "ー" (U+30FC) is in the Common script,
// and ScriptRun("Katakana", core.RuneSet("ー")) keeps it inside "コーヒー",
// while "ー" alone is not a run. ScriptRun panics if a class of extra is nil.
// Each class of extra must give the same answer for the same rune and have
// no side effect (see core.Class).
func ScriptRun(script string, extra ...core.Class) bytescan.Rule {
	scriptClass := core.UnicodeScript(script)
	rest := scriptClass.Or(core.UnicodeMark)
	for i, c := range extra {
		if c == nil {
			panic(fmt.Sprintf("base.ScriptRun: extra class %d is nil", i))
		}
		rest = rest.Or(c)
	}
	return core.Sequence(
		core.Rune(scriptClass),
		core.ZeroOrMore(core.Rune(rest)),
	)
}
