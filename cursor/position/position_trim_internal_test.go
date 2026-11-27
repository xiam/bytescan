package position

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTrimBeforeBoundsTable checks the table stays proportional to the kept
// lines and that oversized backing storage is reclaimed.
func TestTrimBeforeBoundsTable(t *testing.T) {
	p := New()

	for i := 0; i < 100000; i++ {
		p.Scan([]byte("\n"))
		p.TrimBefore(p.offset - 100)

		{
			n := len(p.bytesPerLine)
			require.Falsef(t, n > 2*101+1, "after %d lines the table holds %d entries", i+1, n)
		}
	}

	{
		c := cap(p.bytesPerLine)
		require.Falsef(t, c > 4*202, "table capacity %d is not bounded by the kept lines", c)
	}

	// Steady trimming reuses the storage: no allocation per trim.
	allocs := testing.AllocsPerRun(100, func() {
		for i := 0; i < 100; i++ {
			p.Scan([]byte("\n"))
		}
		p.TrimBefore(p.offset)
	})
	require.Truef(t, allocs == 0, "steady trimming allocates %v times per run", allocs)

	// A burst of lines grows the table. The trim right after it keeps the
	// storage; the next trim, over a short table, reclaims it.
	big := make([]byte, 1<<16)
	for i := range big {
		big[i] = '\n'
	}
	p.Scan(big)
	p.TrimBefore(p.offset)
	{
		n := len(p.bytesPerLine)
		require.Truef(t, n == 1, "after a trim to the end the table has len %d", n)
	}

	p.Scan([]byte("\n\n"))
	p.TrimBefore(p.offset)

	{
		n, c := len(p.bytesPerLine), cap(p.bytesPerLine)
		require.Falsef(t, n != 1 || c > 2*minTrimCap, "after the burst the table has len %d cap %d", n, c)
	}
	{
		n, c := len(p.runesPerLine), cap(p.runesPerLine)
		require.Falsef(t, n != 1 || c > 2*minTrimCap, "after the burst the rune table has len %d cap %d", n, c)
	}
	{
		got, want := p.line(), 100000+100*101+(1<<16)+2+1
		require.Truef(t, got == want, "line %d, want %d", got, want)
	}
}
