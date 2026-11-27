package position

import (
	"math/rand"
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scanOneRuneAtATime is the loop of Scan before 2026-11-23: one utf8.DecodeRune
// and one update of the tables per rune. TestScanMatchesOneRuneAtATime compares
// Scan with it.
func scanOneRuneAtATime(p *Position, in []byte) (line, column, offset int) {
	line, column, offset = p.line(), p.column(), p.offset
	zl := len(p.runesPerLine) - 1
	if zl < 0 {
		p.runesPerLine = []int{0}
		p.bytesPerLine = []int{0}
		zl = 0
	}
	for len(in) > 0 {
		r, size := utf8.DecodeRune(in)
		if r == newLine {
			p.runesPerLine = append(p.runesPerLine, 0)
			p.bytesPerLine = append(p.bytesPerLine, 0)
			zl++
		} else {
			p.runesPerLine[zl]++
			p.bytesPerLine[zl] += size
		}
		p.offset += size
		in = in[size:]
	}
	return line, column, offset
}

// TestScanMatchesOneRuneAtATime scans random inputs in random chunks: ASCII,
// newlines, CRLF, multi-byte runes, invalid bytes and sequences cut at a chunk
// boundary. After each chunk, the state that Scan returns, the line, column and
// offset, and both line tables must be those of one decode per rune.
func TestScanMatchesOneRuneAtATime(t *testing.T) {
	pieces := []string{"a", "z", " ", "\n", "\r\n", "\t", "é", "世", "🙂", "\xff", "\xe2\x28", "\xf0\x9f", "\xc3", "\x80"}
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 2000; iter++ {
		var in []byte
		for k := rng.Intn(40); k > 0; k-- {
			in = append(in, pieces[rng.Intn(len(pieces))]...)
		}
		got, want := New(), New()
		for len(in) > 0 {
			n := 1 + rng.Intn(len(in))
			gl, gc, gotOff := got.Scan(in[:n])
			wl, wc, wo := scanOneRuneAtATime(want, in[:n])
			if gl != wl || gc != wc || gotOff != wo {
				require.FailNowf(t, "unexpected test state", "Scan(%q) returned %d:%d@%d, want %d:%d@%d", in[:n], gl, gc, gotOff, wl, wc, wo)
			}
			if got.line() != want.line() || got.column() != want.column() || got.offset != want.offset ||
				!slices.Equal(got.runesPerLine, want.runesPerLine) || !slices.Equal(got.bytesPerLine, want.bytesPerLine) {
				require.FailNowf(t, "unexpected test state", "after Scan(%q): %+v, want %+v", in[:n], got, want)
			}
			in = in[n:]
		}
	}
}

// TestScanEmptyAfterScanRunes checks that an empty Scan after ScanRunes, which
// fills only the rune table, returns the state and changes nothing, as the
// loop of one decode per rune did.
func TestScanEmptyAfterScanRunes(t *testing.T) {
	p := New()
	p.AdvanceBytes(1)
	p.ScanRunes([]byte("x"))
	{
		line, column, offset := p.Scan(nil)
		assert.Falsef(t, line != 1 || column != 1 || offset != 1, "Scan(nil) = %d:%d@%d, want 1:1@1", line, column, offset)
	}
	{
		line, column, offset := p.State()
		assert.Falsef(t, line != 1 || column != 1 || offset != 1, "after Scan(nil): %d:%d@%d, want 1:1@1", line, column, offset)
	}
}

// scanPanics runs scan and reports whether it panicked.
func scanPanics(scan func()) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	scan()
	return false
}

// TestScanAfterScanRunesMatchesOneRuneAtATime starts each run from a state
// that the other public methods build: AdvanceBytes and ScanRunes, which fill
// the offset and the rune table only, and Scan. Then it scans random chunks.
// Scan must panic exactly when one decode per rune panics, and otherwise
// return the same state and leave the same tables. A newline-only input after
// ScanRunes succeeded at master (cross-eval round 2 of !126).
func TestScanAfterScanRunesMatchesOneRuneAtATime(t *testing.T) {
	pieces := []string{"a", "\n", "\r\n", "é", "世", "\xff", "\xe2\x28"}
	runesOnly := []string{"x", "é", "世界"}
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 3000; iter++ {
		got, want := New(), New()
		broken := false
		for k := rng.Intn(3); k > 0 && !broken; k-- {
			switch rng.Intn(3) {
			case 0:
				gp := scanPanics(func() { got.Scan([]byte("ab\n")) })
				wp := scanPanics(func() { scanOneRuneAtATime(want, []byte("ab\n")) })
				require.Truef(t, gp == wp, "setup Scan: panicked %v, one decode per rune panicked %v", gp, wp)
				broken = gp
			default:
				r := runesOnly[rng.Intn(len(runesOnly))]
				got.AdvanceBytes(len(r))
				want.AdvanceBytes(len(r))
				got.ScanRunes([]byte(r))
				want.ScanRunes([]byte(r))
			}
		}
		if broken {
			continue
		}
		for chunks := 1 + rng.Intn(3); chunks > 0; chunks-- {
			var in []byte
			for k := rng.Intn(4); k > 0; k-- {
				in = append(in, pieces[rng.Intn(len(pieces))]...)
			}
			var gl, gc, gotOff, wl, wc, wo int
			gp := scanPanics(func() { gl, gc, gotOff = got.Scan(in) })
			wp := scanPanics(func() { wl, wc, wo = scanOneRuneAtATime(want, in) })
			require.Truef(t, gp == wp, "Scan(%q): panicked %v, one decode per rune panicked %v", in, gp, wp)
			if gp {
				break
			}
			require.Falsef(t, gl != wl || gc != wc || gotOff != wo, "Scan(%q) returned %d:%d@%d, want %d:%d@%d", in, gl, gc, gotOff, wl, wc, wo)
			require.Falsef(t, got.line() != want.line() || got.column() != want.column() || got.offset != want.offset ||
				!slices.Equal(got.runesPerLine, want.runesPerLine) || !slices.Equal(got.bytesPerLine, want.bytesPerLine), "after Scan(%q): %+v, want %+v", in, got, want)
		}
	}
}

// TestScanNewlineAfterScanRunes is the sequence of cross-eval round 2 of !126:
// a newline-only Scan after ScanRunes returns 1:1@1 and leaves 2:0@2.
func TestScanNewlineAfterScanRunes(t *testing.T) {
	p := New()
	p.AdvanceBytes(1)
	p.ScanRunes([]byte("x"))
	{
		line, column, offset := p.Scan([]byte("\n"))
		assert.Falsef(t, line != 1 || column != 1 || offset != 1, "Scan(newline) = %d:%d@%d, want 1:1@1", line, column, offset)
	}
	{
		line, column, offset := p.State()
		assert.Falsef(t, line != 2 || column != 0 || offset != 2, "after Scan(newline): %d:%d@%d, want 2:0@2", line, column, offset)
	}
}
