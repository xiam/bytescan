package bench

import (
	"strconv"
	"strings"
)

// longJSONCase uses the reference JSON rules with an input dominated by
// string contents. Run it explicitly as -cases longjson.
func longJSONCase() Case {
	return Case{
		name:      "longjson",
		ruleSet:   "the same JSON rules as the json reference case",
		source:    "a generated array of 512 objects with 2 KiB strings and integer IDs",
		rules:     jsonRules,
		newCursor: textCursor,
		input:     longJSONInput,
	}
}

func longJSONInput() ([]byte, error) {
	const objects = 512
	const chunk = "0123456789abcdef"
	payload := strings.Repeat(chunk, (2<<10)/len(chunk))

	var in strings.Builder
	in.WriteByte('[')
	for i := 0; i < objects; i++ {
		if i > 0 {
			in.WriteByte(',')
		}
		in.WriteString(`{"text":"`)
		in.WriteString(payload)
		in.WriteString(`","id":`)
		in.WriteString(strconv.Itoa(i))
		in.WriteByte('}')
	}
	in.WriteByte(']')
	return []byte(in.String()), nil
}
