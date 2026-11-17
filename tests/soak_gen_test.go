package tests

// The soak test generates its own input: it does not use the stream
// harness of the examples, which lives with the example programs in the
// bytescan-examples repository. Every source here is endless and deterministic (a fixed seed),
// and none of them allocates per Read, so the allocations the soak test
// counts are the scanner's and the driver's only.

// soakRand is a xorshift64* generator: deterministic, and free of
// allocations.
type soakRand uint64

func (r *soakRand) next() uint64 {
	x := uint64(*r)
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	*r = soakRand(x)
	return x * 2685821657736338717
}

// intn returns a number in [0, n).
func (r *soakRand) intn(n int) int {
	return int(r.next() % uint64(n))
}

// soakFragmentSource is an endless text stream: a random fragment, then a
// random separator, again and again. Every fragment is a complete token or a
// complete run of tokens, so the stream never holds a token open for longer
// than one fragment.
type soakFragmentSource struct {
	rng       soakRand
	fragments [][]byte
	seps      [][]byte
	pending   []byte
	sep       bool
}

func newSoakFragmentSource(seed uint64, fragments, seps []string) *soakFragmentSource {
	s := &soakFragmentSource{rng: soakRand(seed)}
	for _, f := range fragments {
		s.fragments = append(s.fragments, []byte(f))
	}
	for _, f := range seps {
		s.seps = append(s.seps, []byte(f))
	}
	return s
}

func (s *soakFragmentSource) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(s.pending) == 0 {
			if s.sep {
				s.pending = s.seps[s.rng.intn(len(s.seps))]
			} else {
				s.pending = s.fragments[s.rng.intn(len(s.fragments))]
			}
			s.sep = !s.sep
		}
		k := copy(p[n:], s.pending)
		s.pending = s.pending[k:]
		n += k
	}
	return n, nil
}

// soakMixedFragments covers the token families of the mixed-tier workload:
// identifiers, numbers, strings and comments (rules/base, code), words in
// several scripts and punctuation (rules/base, text), and URLs, email
// addresses and emoji (rules/special).
var soakMixedFragments = []string{
	"foo_1", "bar", "_tmp9", "camelCase", "x",
	"0", "42", "123456", "3.5", "0.25", "1e9",
	`"str"`, `"esc\"aped"`, `"hola mundo"`,
	"// note\n", "/* c */", "/* multi\nline */",
	"palabra", "señor", "café", "naïve", "straße", "Ελλάδα", "мир",
	"https://example.com/a?b=1", "http://example.org/x/y", "user@example.org",
	"😀", "👍🏽", "🇲🇽", "👩‍💻",
	",", ";", ".", "(", ")", "{", "}", "=", "+", "¿", "!",
}

var soakMixedSeps = []string{" ", " ", " ", " ", "\n", "\t", "  "}

// soakModeFragments is the input of the mode-driver workload: code with
// strings that switch to the string mode and comments that switch to the
// comment mode. Each string and comment is complete, so the driver switches
// back.
var soakModeFragments = []string{
	"a", "b1", "count", "i", "n_2",
	"0", "7", "4096",
	"=", "+", "(", ")", ",", ";", "{", "}",
	`"x"`, `"hola"`, `"a\"b"`, `"tab\tand\\slash"`, `"línea uno"`, `""`,
	"# note\n", "# a longer comment, with words\n", "#\n",
}

var soakModeSeps = []string{" ", " ", "", "\n", "\t"}

// Frames of the byte-stream workload: a 5-byte header (two sync bytes, a type
// byte and a big-endian 16-bit payload length), the payload, and a 2-byte
// trailer.
const (
	soakFrameSync0   = 0x7E
	soakFrameSync1   = 0xA5
	soakFrameHeader  = 5
	soakFrameTrailer = "\r\n"
	soakFrameMaxLen  = 511
)

// soakFrameSource is an endless byte stream of frames with random payloads
// of 0 to soakFrameMaxLen bytes. It builds one frame at a time in a fixed
// buffer.
type soakFrameSource struct {
	rng     soakRand
	buf     [soakFrameHeader + soakFrameMaxLen + len(soakFrameTrailer)]byte
	pending []byte
}

func newSoakFrameSource(seed uint64) *soakFrameSource {
	return &soakFrameSource{rng: soakRand(seed)}
}

func (s *soakFrameSource) frame() []byte {
	n := s.rng.intn(soakFrameMaxLen + 1)
	b := s.buf[:0]
	b = append(b, soakFrameSync0, soakFrameSync1, byte(s.rng.intn(256)), byte(n>>8), byte(n))
	for i := 0; i < n; i++ {
		b = append(b, byte(s.rng.next()>>56))
	}
	return append(b, soakFrameTrailer...)
}

func (s *soakFrameSource) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(s.pending) == 0 {
			s.pending = s.frame()
		}
		k := copy(p[n:], s.pending)
		s.pending = s.pending[k:]
		n += k
	}
	return n, nil
}
