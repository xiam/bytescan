package core

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

func TestSingleRuneLiteral(t *testing.T) {
	for _, tc := range []struct {
		literal string
		want    rune
	}{
		{"a", 'a'},
		{"\x00", 0},
		{"語", '語'},
		{"\xff", utf8.RuneError},
	} {
		t.Run(tc.literal, func(t *testing.T) {
			rule := Literal(tc.literal)
			// A combinator must recover the structured matcher through the probe.
			n := nodeOf("test", 0, rule)
			require.Equal(t, nodeLeaf, n.kind)
			require.Equal(t, literalMatcher{lit: []rune{tc.want}}, n.leaf.m)

			for _, step := range []struct {
				symbol bytescan.Symbol
				state  bytescan.State
			}{
				{bytescan.NewSymbol(tc.want, bytescan.FlagNone), bytescan.StateAccept},
				{bytescan.NewSymbol(tc.want, bytescan.FlagBOF|bytescan.FlagEOL|bytescan.FlagBOL), bytescan.StateAccept},
				{bytescan.NewSymbol(tc.want, bytescan.FlagEOF), bytescan.StateReject},
				{bytescan.NewSymbol('z', bytescan.FlagNone), bytescan.StateReject},
				{bytescan.NewSymbol(bytescan.RuneEOF-1, bytescan.FlagNone), bytescan.StateReject},
			} {
				next, state := rule(step.symbol)
				require.Nil(t, next)
				require.Equal(t, step.state, state, "symbol %#v", step.symbol)
			}
		})
	}
}
