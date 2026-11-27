package tests

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// liveSource is a live io.Reader: it has delivered some input and then blocks
// without an error or io.EOF. Read returns the next chunk sent on chunks. When
// no chunk is ready, Read reports on blocked and then waits for one. Closing
// chunks ends the stream with io.EOF.
type liveSource struct {
	chunks  chan []byte
	blocked chan struct{}
	rest    []byte
}

func (s *liveSource) Read(p []byte) (int, error) {
	if len(s.rest) == 0 {
		var (
			chunk []byte
			ok    bool
		)
		select {
		case chunk, ok = <-s.chunks:
		default:
			select {
			case s.blocked <- struct{}{}:
			default:
			}
			chunk, ok = <-s.chunks
		}
		if !ok {
			return 0, io.EOF
		}
		s.rest = chunk
	}

	n := copy(p, s.rest)
	s.rest = s.rest[n:]

	return n, nil
}

// TestScannerReturnsTokensFromLiveSource covers #116 and #117 at the scanner
// level: over a live io.Reader in text mode, with the README rules, the input
// "say hello\n" yields all four tokens before the source has to deliver more.
// Before #117 the cursor held back the last 3 bytes and the scanner returned 2.
//
// The test does not use sleeps. The source reports on blocked when the scanner
// asks it for more input. Every token is sent before the next call to Next, so
// when that signal arrives, tokens holds every token returned before it. The
// timeout only stops a test that hangs.
func TestScannerReturnsTokensFromLiveSource(t *testing.T) {
	src := &liveSource{
		chunks:  make(chan []byte, 1),
		blocked: make(chan struct{}, 1),
	}
	t.Cleanup(func() { close(src.chunks) })
	src.chunks <- []byte("say hello\n")

	lx := bytescan.NewWithCursor(cursor.NewReader(src))
	lx.MustAddRule("KEYWORD", core.Keyword("say", core.UnicodeLetter))
	lx.MustAddRule("WORD", base.Word())
	lx.MustAddRule("WS", base.Whitespace())
	lx.MustAddRule("NL", base.Newline())

	want := []string{`KEYWORD"say"`, `WS" "`, `WORD"hello"`, `NL"\n"`}

	tokens := make(chan string, len(want))
	go func() {
		for range want {
			tok, err := lx.Next()
			if err != nil {
				tokens <- "error: " + err.Error()
				return
			}
			tokens <- fmt.Sprintf("%s%q", tok.Type(), tok.Text())
		}
	}()

	var got []string
	timeout := time.After(10 * time.Second)
	for len(got) < len(want) {
		select {
		case tok := <-tokens:
			got = append(got, tok)
		case <-src.blocked:
			for len(tokens) > 0 {
				got = append(got, <-tokens)
			}
			require.Falsef(t, len(got) < len(want), "the source blocked after %d of %d tokens: %q", len(got), len(want), got)
		case <-timeout:
			require.FailNowf(t, "unexpected test state", "timed out after %d of %d tokens: %q", len(got), len(want), got)
		}
	}

	assert.Equal(t, want, got)
}
