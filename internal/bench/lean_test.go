package bench

import (
	"bytes"
	"hash/fnv"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/internal/leanread"
	"github.com/xiam/bytescan/internal/lextest"
)

// publicCursor embeds a *cursor.Reader. It is not a *cursor.Reader, so the
// scanner reads it through ReadLocatedRune, the public read path.
type publicCursor struct{ *cursor.Reader }

// leanToken is what the lean path test compares for each token.
type leanToken struct {
	typ  bytescan.TokenType
	text string
	span cursor.Span
	la   bytescan.Lookahead
}

// leanHash folds every symbol shown to a rule into a hash: its rune, its
// flags and its size.
type leanHash struct{ sum uint64 }

func (h *leanHash) add(s bytescan.Symbol) {
	var flags uint64
	for i, set := range []bool{s.IsEOF(), s.IsBOF(), s.IsEOL(), s.IsBOL()} {
		if set {
			flags |= 1 << i
		}
	}
	h.sum = h.sum*1099511628211 ^ uint64(uint32(s.Rune())) ^ flags<<32 ^ uint64(s.Size())<<40
}

func leanWrap(h *leanHash, r bytescan.Rule) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		h.add(s)
		next, st := r(s)
		if next != nil {
			next = leanWrap(h, next)
		}
		return next, st
	}
}

// leanLex lexes in through cur with the rules of c, and returns a hash of the
// tokens, their count, and the hash of the symbols shown to the rules.
func leanLex(t *testing.T, c Case, cur bytescan.Cursor) (tokens uint64, count int, shown uint64) {
	t.Helper()
	sc := bytescan.NewWithCursor(cur)
	var h leanHash
	for _, rr := range c.rules() {
		sc.MustAddRule(rr.typ, leanWrap(&h, rr.rule))
	}
	th := fnv.New64a()
	for {
		tok, la, err := sc.NextLookahead()
		if err == io.EOF {
			return th.Sum64(), count, h.sum
		}
		require.NoErrorf(t, err, "%s: token %d: %v", c.name, count, err)
		lt := leanToken{typ: tok.Type(), text: tok.Text(), span: tok.Span(), la: la}
		s, e := lt.span.Start(), lt.span.End()
		_, _ = th.Write([]byte(lt.typ))
		_, _ = th.Write([]byte{0})
		_, _ = th.Write([]byte(lt.text))
		for _, v := range []int{
			s.ByteOffset(), s.RuneOffset(), s.Line(), s.Column(),
			e.ByteOffset(), e.RuneOffset(), e.Line(), e.Column(),
			lt.la.After,
		} {
			_, _ = th.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24), byte(v >> 32)})
		}
		if lt.la.ReadEnd {
			_, _ = th.Write([]byte{1})
		}
		count++
	}
}

// TestLeanReadPathReferenceCases lexes every reference case twice, on the
// input of TestReferencesLex: through the lean read path of the case's
// *cursor.Reader, and through ReadLocatedRune on a cursor that hides the lean
// path. Every token must have
// the same type, text, span, line, column and Lookahead record, and the rules
// must be shown the same symbols with the same flags.
func TestLeanReadPathReferenceCases(t *testing.T) {
	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) {
			if lextest.RaceDetector && c.name == "large" {
				t.Skip("too slow under the race detector")
			}
			// The input of TestReferencesLex: stream is cut to 1 MiB, and
			// code is skipped under the race detector.
			in, ok := refInput(t, c)
			if !ok {
				t.Skip("too slow under the race detector")
			}

			leanCur := c.newCursor(bytes.NewReader(in))
			{
				_, ok := leanread.Of(leanCur)
				require.Truef(t, ok, "%s: the cursor of the case has no lean read path", c.name)
			}
			gotTok, gotN, gotShown := leanLex(t, c, leanCur)
			wantTok, wantN, wantShown := leanLex(t, c, publicCursor{c.newCursor(bytes.NewReader(in))})

			assert.Truef(t, gotN == wantN, "%s: %d tokens on the lean path, %d on the public path", c.name, gotN, wantN)
			assert.Truef(t, gotTok == wantTok, "%s: the tokens differ between the lean path and the public path", c.name)
			assert.Truef(t, gotShown == wantShown, "%s: the symbols shown to the rules differ between the lean path and the public path", c.name)
		})
	}
}
