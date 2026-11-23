package bench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/xiam/bytescan/internal/lextest"
	"github.com/xiam/bytescan/streamtest"
)

func nop() {}

// TestCaseInputs checks the input of every reference case against
// docs/specs/measurement.md, "The reference cases": its size and the boundary
// that it ends at.
func TestCaseInputs(t *testing.T) {
	const (
		mib = 1 << 20
		// The longest record of each generated case: a SLIP frame is at most
		// an END, 2,999 stuffed bytes of 2 bytes and an END; a statement and
		// a line are far shorter.
		maxSLIPFrame = 1 + 2*2999 + 1
		maxRecord    = 1000
	)
	want := []struct {
		name     string
		min, max int // the size is in [min, max]
		end      []byte
	}{
		{"json", 2 * golangSourceSize, 2 * golangSourceSize, []byte("}")},
		{"code", 4 * mib, 4*mib + maxRecord, []byte(";\n")},
		{"slip", 4 * mib, 4*mib + maxSLIPFrame, []byte{slipEnd}},
		{"stream", 64 * mib, 64*mib + maxRecord, []byte("\n")},
		{"large", 4*mib + 3, 4*mib + 3, []byte("a")},
	}
	if got := len(cases()); got != len(want) {
		t.Fatalf("%d cases, want %d", got, len(want))
	}
	for i, w := range want {
		c := cases()[i]
		if c.name != w.name {
			t.Fatalf("case %d is %s, want %s", i, c.name, w.name)
		}
		in := input(t, c)
		if len(in) < w.min || len(in) > w.max {
			t.Errorf("%s: %d bytes, want %d to %d", c.name, len(in), w.min, w.max)
		}
		if !bytes.HasSuffix(in, w.end) {
			t.Errorf("%s: ends with %q, want %q", c.name, in[len(in)-len(w.end):], w.end)
		}
	}

	// json is golang_source.json twice in a row.
	in := input(t, cases()[0])
	half := in[:golangSourceSize]
	sum := sha256.Sum256(half)
	if hex.EncodeToString(sum[:]) != golangSourceSHA256 || !bytes.Equal(half, in[golangSourceSize:]) {
		t.Errorf("json: the input is not golang_source.json twice")
	}

	// large is four runs of 1 MiB of 'a' between single spaces.
	tok := bytes.Repeat([]byte("a"), mib)
	if want := bytes.Join([][]byte{tok, tok, tok, tok}, []byte(" ")); !bytes.Equal(input(t, cases()[4]), want) {
		t.Errorf("large: not four tokens of 1 MiB of 'a'")
	}
}

// TestGeneratedInputs checks that each generated input is the stream of a
// streamtest.Generator with the seed and the budget of the contract, so it
// ends with the first record past the budget.
func TestGeneratedInputs(t *testing.T) {
	gens := map[string]*streamtest.Generator{
		"code":   streamtest.NewGenerator(1, codeRecord, streamtest.WithBudget(4<<20)),
		"slip":   streamtest.NewGenerator(55, slipRecord, streamtest.WithBudget(4<<20)),
		"stream": streamtest.NewGenerator(1, lineRecord, streamtest.WithBudget(64<<20)),
	}
	for name, gen := range gens {
		c, _ := findCase(name)
		in := input(t, c)
		var b bytes.Buffer
		if _, err := b.ReadFrom(gen); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(in, b.Bytes()) {
			t.Errorf("%s: the input is not the stream of its generator", name)
		}
	}
}

// TestCasesLex lexes every case in each lane of the build: control and
// wrapped in the normal build, instrumented with the tag bench. No token is
// unknown, the lanes count the same tokens, the wrappers count every byte of
// the input (lex checks that), and the instrumented lane has the engine
// counters. The stream case is lexed over the first 1 MiB of its generator,
// not over 64 MiB. Under the race detector, the code case does not run.
func TestCasesLex(t *testing.T) {
	lanes := []Lane{Control, Wrapped}
	if tagged() {
		lanes = []Lane{Instrumented}
	}
	for _, c := range cases() {
		in := []byte(nil)
		switch {
		case c.name == "stream":
			var err error
			if in, err = generate(streamtest.NewGenerator(1, lineRecord, streamtest.WithBudget(1<<20))); err != nil {
				t.Fatal(err)
			}
		case c.name == "code" && lextest.RaceDetector:
			continue
		default:
			in = input(t, c)
		}
		var tokens uint64
		for _, l := range lanes {
			res, err := lex(c, l, in, nop, nop)
			if err != nil {
				t.Fatalf("%s, %s: %v", c.name, l, err)
			}
			if res.Tokens == 0 || tokens != 0 && res.Tokens != tokens {
				t.Errorf("%s, %s: %d tokens, want %d", c.name, l, res.Tokens, tokens)
			}
			tokens = res.Tokens
			if l == Control && res.Counts != (result{}).Counts {
				t.Errorf("%s: the control lane counted %+v", c.name, res.Counts)
			}
			if l == Instrumented && (res.Scanner.Tokens != res.Tokens || res.Scanner.Unknown != 0 || res.Cursor.FillBytes != uint64(len(in))) {
				t.Errorf("%s: the counters %+v and %+v do not match %d tokens of %d bytes", c.name, res.Scanner, res.Cursor, res.Tokens, len(in))
			}
		}
	}
}

// TestLaneNeedsItsBuild checks that lex refuses a lane that does not match
// the build.
func TestLaneNeedsItsBuild(t *testing.T) {
	c, _ := findCase("large")
	in := []byte("a b")
	wrong := Instrumented
	if tagged() {
		wrong = Control
	}
	if _, err := lex(c, wrong, in, nop, nop); !errors.Is(err, errLaneBuild) {
		t.Errorf("lex in lane %s: %v, want errLaneBuild", wrong, err)
	}
	if _, err := parseLane("mean"); err == nil {
		t.Errorf("parseLane(mean) gives no error")
	}
}
