package bench

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/scanner"
)

// tool is an outside reference: a lexer from outside bytescan that runs on
// the input of a text case, in the same rounds as the lanes (owner decision,
// 2026-11-20, "Approve as proposed"). A reference is not a lane: no
// reference has the instruments of the lanes.
type tool string

// The outside references of docs/specs/measurement.md, "The outside
// references".
const (
	// textScanner is text/scanner of the standard library, with its default
	// mode: the Go tokens, white space and comments skipped.
	textScanner tool = "textscanner"
	// regexpLexer is one regular expression of the standard library: an
	// anchored alternation of the token classes of the case, with
	// leftmost-longest matching.
	regexpLexer tool = "regexp"
)

// allTools lists the references in the order of the report.
var allTools = []tool{textScanner, regexpLexer}

// tokenClasses holds, for each text case, one regular expression for each
// rule of its rule set, in the order of registration. The classes match the
// rules of rules.go on the input of the case: TestReferencesLex checks the
// token types and the byte offsets of the regexp lexer against bytescan on
// json, code, large and the first 1 MiB of stream, and the runner checks the
// token count on every whole input. slip is not a text case, so it has no
// entry.
var tokenClasses = map[string][]string{
	"json": {
		`\[`, `\{`, `\]`, `\}`, `:`, `,`, `true`, `false`, `null`,
		`"(?:[^"\\\x00-\x1f]|\\(?:["\\/bfnrt]|u[0-9A-Fa-f]{4}))*"`,
		`-?(?:0|[1-9][0-9]*)(?:\.[0-9]+(?:[eE][+-]?[0-9]+)?|[eE][+-]?[0-9]+)?`,
		`[ \t\n\r]+`,
	},
	"code": {
		`[0-9]+\.[0-9]*|\.[0-9]+`,
		`'[^']*'`,
		`/\*(?:[^*]|\*[^/])*\*/`,
		`[A-Za-z_][A-Za-z0-9_]*`,
		`[ \t\n\r]+`,
		`[-+*/=;()]`,
		`SELECT|FROM|WHERE`,
	},
	"stream": {`[a-z0-9]{1,1000}`, `=`, ` `, `\n`},
	"large":  {`[A-Za-z_][A-Za-z0-9_]*`, `[ \t\n\r]+`},
}

// references returns the outside references of case c: both tools for a
// text case, none for slip.
func references(c Case) []tool {
	if _, ok := tokenClasses[c.name]; ok {
		return allTools
	}
	return nil
}

// refPattern returns the regular expression of the regexp lexer of case c:
// one anchored alternation with a group for each token class.
func refPattern(c Case) string {
	groups := make([]string, len(tokenClasses[c.name]))
	for i, class := range tokenClasses[c.name] {
		groups[i] = "(" + class + ")"
	}
	return `\A(?:` + strings.Join(groups, "|") + `)`
}

// refResult is what one run of a reference counted.
type refResult struct {
	// Tokens is the number of tokens that the reference returned.
	Tokens uint64
	// Errors is the number of errors that text/scanner reported. The
	// regexp lexer stops at its first error, so it is 0 for it.
	Errors uint64
}

// refSink keeps the compiler from dropping the token classes.
var refSink int

// refLex lexes in once with the reference t of case c. It calls start
// before the first token and stop after the last one, as lex does. The
// setup of the reference, such as the compilation of the regular expression,
// is outside the interval.
func refLex(c Case, t tool, in []byte, start, stop func()) (refResult, error) {
	var res refResult
	switch t {
	case textScanner:
		var s scanner.Scanner
		s.Init(bytes.NewReader(in))
		s.Error = func(*scanner.Scanner, string) {}
		start()
		for tok := s.Scan(); tok != scanner.EOF; tok = s.Scan() {
			res.Tokens++
			refSink = int(tok)
		}
		stop()
		res.Errors = uint64(s.ErrorCount)
		return res, nil
	case regexpLexer:
		re, err := regexp.Compile(refPattern(c))
		if err != nil {
			return res, fmt.Errorf("bench: case %s: %w", c.name, err)
		}
		re.Longest()
		start()
		for pos := 0; pos < len(in); {
			loc := re.FindSubmatchIndex(in[pos:])
			if loc == nil || loc[1] == 0 {
				stop()
				return res, fmt.Errorf("bench: case %s, reference %s: no token at byte %d", c.name, t, pos)
			}
			refSink = class(loc)
			res.Tokens++
			pos += loc[1]
		}
		stop()
		return res, nil
	}
	return res, fmt.Errorf("bench: unknown reference %q", t)
}

// class returns the index of the first group that matched in loc, the
// result of FindSubmatchIndex: the token class.
func class(loc []int) int {
	for g := 1; 2*g < len(loc); g++ {
		if loc[2*g] >= 0 {
			return g - 1
		}
	}
	return -1
}
