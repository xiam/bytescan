// Command gen generates the emoji property tables of package special from the
// Unicode emoji data file emoji-data.txt.
//
// It reads only the committed copy of the file in the data directory, so
// "go generate ./rules/special/..." reproduces the tables byte for byte without
// network access. To move to a new Unicode version, replace the files in the
// data directory with the files of that version (see data/README.md) and run
// go generate again.
//
// Usage:
//
//	go run ./internal/gen -data internal/gen/data/emoji-data.txt -o emoji_table.go
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// property is one emoji property that gen writes as a table.
type property struct {
	name string // the property name in emoji-data.txt
	vari string // the name of the generated table variable
	doc  string // the comment of the generated table variable, after its name
}

// properties are the tables that gen writes, in output order.
var properties = []property{
	{"Emoji", "emojiTable", "is the Emoji property, \\p{Emoji}:\n// the emoji characters (UTS #51 ED-3)."},
	{"Emoji_Presentation", "emojiPresentationTable", "is the Emoji_Presentation property, \\p{EPres}:\n// the characters with default emoji presentation (UTS #51 ED-6)."},
	{"Emoji_Modifier", "emojiModifierTable", "is the Emoji_Modifier property, \\p{EMod}:\n// the skin tone modifiers (UTS #51 ED-11)."},
	{"Emoji_Modifier_Base", "emojiModifierBaseTable", "is the Emoji_Modifier_Base property, \\p{EBase}:\n// the characters that a skin tone modifier can follow (UTS #51 ED-12)."},
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("gen: ")
	dataPath := flag.String("data", "internal/gen/data/emoji-data.txt", "path of emoji-data.txt")
	outPath := flag.String("o", "emoji_table.go", "path of the generated Go file")
	flag.Parse()

	in, err := os.Open(*dataPath)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	version, sets, err := parseEmojiData(in)
	if err != nil {
		log.Fatalf("%s: %v", *dataPath, err)
	}
	src, err := render(version, sets)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*outPath, src, 0o644); err != nil {
		log.Fatal(err)
	}
}

// parseEmojiData reads emoji-data.txt and returns its version, from the
// "# Version:" header line, and the code points of each property of
// properties.
func parseEmojiData(r io.Reader) (string, map[string]map[rune]bool, error) {
	sets := make(map[string]map[rune]bool)
	for _, p := range properties {
		sets[p.name] = make(map[rune]bool)
	}
	version := ""
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if v, ok := strings.CutPrefix(text, "# Version:"); ok && version == "" {
			version = strings.TrimSpace(v)
			continue
		}
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		fields := strings.Split(text, ";")
		if len(fields) != 2 {
			return "", nil, fmt.Errorf("line %d: want 2 fields, got %d", line, len(fields))
		}
		set, ok := sets[strings.TrimSpace(fields[1])]
		if !ok {
			continue
		}
		lo, hi, err := parseRange(strings.TrimSpace(fields[0]))
		if err != nil {
			return "", nil, fmt.Errorf("line %d: %v", line, err)
		}
		for c := lo; c <= hi; c++ {
			set[c] = true
		}
	}
	if err := sc.Err(); err != nil {
		return "", nil, err
	}
	if version == "" {
		return "", nil, fmt.Errorf("no \"# Version:\" line")
	}
	for _, p := range properties {
		if len(sets[p.name]) == 0 {
			return "", nil, fmt.Errorf("no code point has the property %s", p.name)
		}
	}
	return version, sets, nil
}

// parseRange parses "XXXX" or "XXXX..YYYY" in hexadecimal.
func parseRange(s string) (rune, rune, error) {
	loText, hiText, isRange := strings.Cut(s, "..")
	lo, err := strconv.ParseUint(loText, 16, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("bad code point %q", loText)
	}
	hi := lo
	if isRange {
		if hi, err = strconv.ParseUint(hiText, 16, 32); err != nil {
			return 0, 0, fmt.Errorf("bad code point %q", hiText)
		}
	}
	if hi < lo || hi > unicode.MaxRune {
		return 0, 0, fmt.Errorf("bad range %q", s)
	}
	return rune(lo), rune(hi), nil
}

// span is a run of consecutive code points, both ends included.
type span struct{ lo, hi rune }

// spans returns the code points of set as sorted maximal runs. A run never
// crosses U+FFFF, so that each run fits a unicode.Range16 or a Range32.
func spans(set map[rune]bool) []span {
	cps := make([]rune, 0, len(set))
	for c := range set {
		cps = append(cps, c)
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i] < cps[j] })
	var out []span
	for _, c := range cps {
		if n := len(out); n > 0 && out[n-1].hi == c-1 && c != 0x10000 {
			out[n-1].hi = c
			continue
		}
		out = append(out, span{c, c})
	}
	return out
}

// render returns the formatted Go source of the generated file.
func render(version string, sets map[string]map[rune]bool) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by internal/gen from emoji-data.txt, version %s. DO NOT EDIT.\n\n", version)
	b.WriteString("package special\n\nimport \"unicode\"\n\n")
	b.WriteString("// UnicodeEmojiVersion is the version of the Unicode emoji data (emoji-data.txt\n")
	b.WriteString("// of UTS #51) that the emoji tables of Emoji are generated from.\n")
	fmt.Fprintf(&b, "const UnicodeEmojiVersion = %q\n", version)
	for _, p := range properties {
		ss := spans(sets[p.name])
		fmt.Fprintf(&b, "\n// %s %s\n// It has %d code points in %d ranges.\n", p.vari, p.doc, len(sets[p.name]), len(ss))
		fmt.Fprintf(&b, "var %s = &unicode.RangeTable{\n", p.vari)
		var r16, r32 []span
		latinOffset := 0
		for _, s := range ss {
			if s.hi <= 0xFFFF {
				r16 = append(r16, s)
				if s.hi <= unicode.MaxLatin1 {
					latinOffset++
				}
			} else {
				r32 = append(r32, s)
			}
		}
		if len(r16) > 0 {
			b.WriteString("R16: []unicode.Range16{\n")
			for _, s := range r16 {
				fmt.Fprintf(&b, "{Lo: 0x%04X, Hi: 0x%04X, Stride: 1},\n", s.lo, s.hi)
			}
			b.WriteString("},\n")
		}
		if len(r32) > 0 {
			b.WriteString("R32: []unicode.Range32{\n")
			for _, s := range r32 {
				fmt.Fprintf(&b, "{Lo: 0x%05X, Hi: 0x%05X, Stride: 1},\n", s.lo, s.hi)
			}
			b.WriteString("},\n")
		}
		if latinOffset > 0 {
			fmt.Fprintf(&b, "LatinOffset: %d,\n", latinOffset)
		}
		b.WriteString("}\n")
	}
	return format.Source(b.Bytes())
}
