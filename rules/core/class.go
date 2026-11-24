package core

import (
	"fmt"
	"unicode"
)

// A Class reports whether a rune belongs to a set of runes. Any
// func(rune) bool converts to a Class: core.Class(unicode.IsLetter).
//
// A Class sees only the rune. Positional conditions, such as the beginning of
// a line or the end of input, are not classes.
//
// A Class gives the same answer every time it is asked about the same rune,
// and it has no side effect: it writes no variable and performs no I/O. This
// holds for a predicate that the caller converts to a Class too. It is
// practice 6 of docs/rule-authoring.md, which the owner adopted on
// 2026-11-23 in the words "Adopt practice 6 (Recommended)". Nothing checks it
// at run time. A rule of this package may ask a class about a rune once and
// reuse the answer, so a class that breaks it can give other tokens.
type Class func(rune) bool

// Not returns the complement of c: [^c]. c must give the same answer for
// the same rune and have no side effect (see Class).
func (c Class) Not() Class {
	mustClass("Class.Not", c)
	return func(r rune) bool { return !c(r) }
}

// Or returns the union of c and o: [co]. c and o must give the same answer
// for the same rune and have no side effect (see Class).
func (c Class) Or(o Class) Class {
	mustClass("Class.Or", c)
	mustClass("Class.Or", o)
	return func(r rune) bool { return c(r) || o(r) }
}

// And returns the intersection of c and o: the runes in both. c and o must
// give the same answer for the same rune and have no side effect (see
// Class).
func (c Class) And(o Class) Class {
	mustClass("Class.And", c)
	mustClass("Class.And", o)
	return func(r rune) bool { return c(r) && o(r) }
}

// ASCII classes. They contain no rune outside ASCII.
var (
	// ASCIIDigit is [0-9].
	ASCIIDigit Class = func(r rune) bool { return r >= '0' && r <= '9' }

	// ASCIIOctalDigit is [0-7].
	ASCIIOctalDigit Class = func(r rune) bool { return r >= '0' && r <= '7' }

	// ASCIIBinaryDigit is [01].
	ASCIIBinaryDigit Class = func(r rune) bool { return r == '0' || r == '1' }

	// ASCIIHexDigit is [0-9A-Fa-f].
	ASCIIHexDigit Class = func(r rune) bool {
		return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') || (r >= 'a' && r <= 'f')
	}

	// ASCIILetter is [A-Za-z].
	ASCIILetter Class = func(r rune) bool { return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') }

	// ASCIILower is [a-z].
	ASCIILower Class = func(r rune) bool { return r >= 'a' && r <= 'z' }

	// ASCIIUpper is [A-Z].
	ASCIIUpper Class = func(r rune) bool { return r >= 'A' && r <= 'Z' }

	// ASCIIIdentifierStart is [A-Za-z_]: the runes that start an ASCII
	// identifier in most programming languages. ASCIIWord continues it.
	ASCIIIdentifierStart Class = func(r rune) bool {
		return r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
	}

	// ASCIIWord is \w, which is ASCII-only: [0-9A-Za-z_].
	ASCIIWord Class = func(r rune) bool {
		return r == '_' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
	}

	// ASCIISpace is \s, which is ASCII-only: [\t\n\f\r ]. It excludes '\v'.
	ASCIISpace Class = func(r rune) bool {
		return r == '\t' || r == '\n' || r == '\f' || r == '\r' || r == ' '
	}
)

// AnyRune is (?s:.): every rune, a line break included. It is neither an
// ASCII class nor a Unicode category, so it has no prefix. The EOF symbol is
// not a rune of the input, and no class sees it.
var AnyRune Class = func(rune) bool { return true }

// Unicode general categories.
var (
	// UnicodeLetter is \pL.
	UnicodeLetter Class = unicode.IsLetter

	// UnicodeMark is \pM.
	UnicodeMark Class = unicode.IsMark

	// UnicodeNumber is \pN.
	UnicodeNumber Class = unicode.IsNumber

	// UnicodePunct is \pP.
	UnicodePunct Class = unicode.IsPunct

	// UnicodeSymbol is \pS.
	UnicodeSymbol Class = unicode.IsSymbol

	// UnicodeSeparator is \pZ.
	UnicodeSeparator Class = func(r rune) bool { return unicode.Is(unicode.Z, r) }
)

// RuneSet returns the class of the runes in s: [s], with every rune of s taken
// literally. It panics if s is empty.
func RuneSet(s string) Class {
	if s == "" {
		panic("core.RuneSet: s must not be empty")
	}
	set := make(map[rune]bool)
	for _, r := range s {
		set[r] = true
	}
	return func(r rune) bool { return set[r] }
}

// RuneRange returns the class of the runes from lo to hi, both included: [lo-hi].
// It panics if hi < lo.
func RuneRange(lo, hi rune) Class {
	if hi < lo {
		panic(fmt.Sprintf("core.RuneRange: hi %U is below lo %U", hi, lo))
	}
	return func(r rune) bool { return r >= lo && r <= hi }
}

// UnicodeTables returns the class of the runes in any of the tables, for example
// UnicodeTables(unicode.Lu) for \p{Lu}. It panics if no table is given or a table is nil.
func UnicodeTables(tables ...*unicode.RangeTable) Class {
	if len(tables) == 0 {
		panic("core.UnicodeTables: no table")
	}
	for _, t := range tables {
		if t == nil {
			panic("core.UnicodeTables: nil table")
		}
	}
	tables = append([]*unicode.RangeTable(nil), tables...)
	return func(r rune) bool { return unicode.IsOneOf(tables, r) }
}

// UnicodeScript returns the class of the runes of the named Unicode script, for
// example UnicodeScript("Greek") for \p{Greek}. The names are the keys of
// unicode.Scripts. It panics if the script is unknown.
func UnicodeScript(name string) Class {
	table, ok := unicode.Scripts[name]
	if !ok {
		panic(fmt.Sprintf("core.UnicodeScript: unknown script %q", name))
	}
	return UnicodeTables(table)
}

// mustClass panics if c is nil. fn names the caller in the message.
func mustClass(fn string, c Class) {
	if c == nil {
		panic("core." + fn + ": nil class")
	}
}
