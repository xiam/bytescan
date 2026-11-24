package bench

import (
	"strings"

	"github.com/xiam/bytescan/rules/core"
)

// The cold cases measure the first use of a rule set on a short input: the
// rule set is built anew for every run (Case.rules), and a run lexes one
// short input once. So every run pays what a rule does at its first symbol,
// such as the build of the table of a pure core rule
// (docs/specs/table-compiler.md, "The cost"), which the long reference cases
// spread over megabytes. They are not reference cases of
// docs/specs/measurement.md: a comparison names them in -cases, and the
// default run of the harness does not run them.

// coldCases returns the cold cases: the json rule set, whose pure core rules
// fit the build budget of a table, and a pure core rule set over that budget,
// which keeps the engine.
func coldCases() []Case {
	return []Case{
		{
			name:      "coldjson",
			ruleSet:   "the rule set of the case json, built anew for every run",
			source:    "one short JSON object of 64 bytes",
			rules:     jsonRules,
			newCursor: textCursor,
			input:     coldJSONInput,
		},
		{
			name:      "coldbudget",
			ruleSet:   "(?:a|b)*a(?:a|b){14}, a pure core rule over the build budget, and a run of spaces, built anew for every run",
			source:    "64 bytes of `ab` words and spaces",
			rules:     overBudgetRules,
			newCursor: textCursor,
			input:     coldBudgetInput,
		},
	}
}

// coldJSONInput returns the input of coldjson: one JSON object of 64 bytes
// with every token class of the json rule set.
func coldJSONInput() ([]byte, error) {
	return []byte(`{"id": 12, "name": "x\"y", "ok": true, "v": [1.5e3, -25, null]}` + "\n"), nil
}

// coldBudgetInput returns the input of coldbudget: words of the rule OVER,
// each longer than 15 runes, separated by spaces, 64 bytes.
func coldBudgetInput() ([]byte, error) {
	word := strings.Repeat("ab", 10)
	return []byte(strings.Repeat(word+" ", 3) + "b"), nil
}

// overBudgetRules is the rule set of coldbudget. The rule OVER must remember
// the last 15 runes, so a deterministic automaton of it has 2^15 states.
func overBudgetRules() []rule {
	ab := core.Rune(core.RuneSet("ab"))
	return []rule{
		{"OVER", core.Sequence(core.ZeroOrMore(ab), core.Literal("a"), core.Repeat(ab, 14, 14))},
		{"AB", core.RuneRun(core.RuneSet("ab"))},
		{"SPACE", core.RuneRun(core.RuneSet(" "))},
	}
}
