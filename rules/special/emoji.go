package special

import (
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

//go:generate go run ./internal/gen -data internal/gen/data/emoji-data.txt -o emoji_table.go

// The classes of Emoji. The property classes come from the generated tables of
// emoji_table.go; the others are fixed by the definitions of UTS #51.
var (
	// emojiCharacterClass is \p{Emoji}: an emoji character (ED-3).
	emojiCharacterClass = core.UnicodeTables(emojiTable)

	// emojiModifierClass is \p{EMod}: a skin tone modifier (ED-11).
	emojiModifierClass = core.UnicodeTables(emojiModifierTable)

	// emojiModifierBaseClass is \p{EBase}: a character that a skin tone
	// modifier can follow (ED-12).
	emojiModifierBaseClass = core.UnicodeTables(emojiModifierBaseTable)

	// emojiRegionalIndicatorClass is \p{Regional_Indicator}:
	// [\x{1F1E6}-\x{1F1FF}], the 26 regional indicator symbols of an emoji
	// flag sequence (ED-14). The set is fixed by the Unicode stability policy
	// for the property.
	emojiRegionalIndicatorClass = core.RuneRange(0x1F1E6, 0x1F1FF)

	// emojiQualifiedCharacterClass is [\p{EPres}--\p{Regional_Indicator}]: an
	// emoji character with default emoji presentation (ED-6), which is a
	// qualified emoji character on its own (ED-17a (a)), less the regional
	// indicators, which are emoji components and not emoji on their own.
	emojiQualifiedCharacterClass = core.UnicodeTables(emojiPresentationTable).And(emojiRegionalIndicatorClass.Not())

	// emojiKeycapBaseClass is [0-9#*]: the first character of an emoji keycap
	// sequence (ED-14c).
	emojiKeycapBaseClass = core.RuneSet("0123456789#*")

	// emojiTagSpecClass is [\x{E0020}-\x{E007E}]: a tag character of the
	// tag_spec of an emoji tag sequence (ED-14a).
	emojiTagSpecClass = core.RuneRange(0xE0020, 0xE007E)
)

// The fixed characters of an emoji sequence.
const (
	emojiPresentationSelector = "️"          // VARIATION SELECTOR-16 (ED-9)
	emojiTextSelector         = "︎"          // VARIATION SELECTOR-15 (ED-8)
	emojiKeycap               = "⃣"          // COMBINING ENCLOSING KEYCAP (ED-14c)
	emojiJoiner               = "‍"          // ZERO WIDTH JOINER (ED-16)
	emojiTagEnd               = "\U000E007F" // CANCEL TAG (ED-14a)
)

// Emoji returns a rule for one emoji sequence of Unicode Technical Standard
// #51, "Unicode Emoji" (UTS #51), version UnicodeEmojiVersion: an
// emoji_sequence of definition ED-17 whose first character is a qualified
// emoji character (ED-17a). In the notation of UTS #51, with \p{EPres} for
// Emoji_Presentation and \p{RI} for Regional_Indicator:
//
//	emoji               := first (ZWJ element)*
//	first               := qualified_character (?!\x{FE0E})
//	                     | qualified_start | keycap | flag
//	                     | (qualified_character | qualified_start) tag
//	qualified_character := [\p{EPres}--\p{RI}]                  ED-6, ED-17a
//	qualified_start     := presentation | modifier              ED-17a
//	element             := base | keycap | flag | base tag      ED-15a
//	base                := \p{Emoji} | presentation | modifier  ED-14a tag_base
//	presentation        := \p{Emoji} \x{FE0F}                   ED-9a
//	modifier            := \p{EBase} \p{EMod}                   ED-13
//	keycap              := [0-9#*] \x{FE0F} \x{20E3}            ED-14c
//	flag                := \p{RI} \p{RI}                        ED-14
//	tag                 := [\x{E0020}-\x{E007E}]+ \x{E007F}     ED-14a
//	ZWJ                 := \x{200D}                             ED-16
//
// The property classes come from tables generated from emoji-data.txt of
// UnicodeEmojiVersion (see the package documentation). Each RGI emoji
// sequence of that version, as listed in emoji-sequences.txt and
// emoji-zwj-sequences.txt, is one match, and so is each fully-qualified and
// minimally-qualified emoji (ED-18, ED-18a).
//
// The rule checks the form of a sequence, not its validity. The deviations
// from UTS #51:
//
//   - It matches a well-formed sequence that is not RGI or not valid: any
//     pair of regional indicators is a flag, any tag_spec is accepted, with
//     no limit of 32 code points (Annex C), any emoji character takes
//     \x{FE0F} (only the sequences of emoji-variation-sequences.txt are
//     valid, ED-9a), and any ZWJ sequence of elements is one match, as for
//     the possible_emoji of section 1.4.9.
//   - It does not match an unqualified emoji (ED-19): an emoji character
//     without default emoji presentation needs \x{FE0F} or a skin tone
//     modifier after it to start a sequence. So "1", "#", "©" and "☺" are
//     not emoji, while "©\uFE0F", "☺\uFE0F" and "1\uFE0F\u20E3" are. An
//     element after a ZWJ need not be qualified.
//   - A lone regional indicator, which has default emoji presentation, does
//     not start a sequence: it is an emoji component and not in the basic
//     emoji set. After a ZWJ it is an emoji character like any other.
//   - An emoji character with default emoji presentation that \x{FE0E}
//     follows is a text presentation sequence (ED-8a), not an emoji: on
//     "⌚\uFE0E" the rule matches nothing.
//   - A keycap needs \x{FE0F}: the unqualified "1\u20E3" is not an emoji.
//
// The rule matches the longest sequence: on "👩‍❤️‍👨" it matches all of it,
// and on "🇺🇸🇫🇷" it matches "🇺🇸". A ZWJ that no element follows is not
// part of the match: on "😀\u200D" the rule matches "😀". At end of input it
// ends with the longest sequence read so far, excluding a final ZWJ or an
// unfinished tag_spec with a pushback chain; on "⌚" at end of input it
// matches "⌚", because the text presentation selector can no longer follow.
//
// Cost: linear in the sequence length. Each rune is tested against at most a
// few property tables, each by a binary search over its ranges, and the only
// lookahead is the one rune after a qualified character that starts a
// sequence.
func Emoji() bytescan.Rule {
	presentation := core.Sequence(core.Rune(emojiCharacterClass), core.Literal(emojiPresentationSelector))
	modifier := core.Sequence(core.Rune(emojiModifierBaseClass), core.Rune(emojiModifierClass))
	keycap := core.Sequence(core.Rune(emojiKeycapBaseClass), core.Literal(emojiPresentationSelector+emojiKeycap))
	flag := core.Sequence(core.Rune(emojiRegionalIndicatorClass), core.Rune(emojiRegionalIndicatorClass))
	tag := core.Sequence(core.RuneRun(emojiTagSpecClass), core.Literal(emojiTagEnd))

	qualifiedCharacter := core.NotFollowedBy(core.Rune(emojiQualifiedCharacterClass), core.Literal(emojiTextSelector))
	qualifiedBase := core.Choice(presentation, modifier)
	firstElement := core.Choice(
		qualifiedCharacter,
		qualifiedBase,
		keycap,
		flag,
		core.Sequence(core.Choice(core.Rune(emojiQualifiedCharacterClass), qualifiedBase), tag),
	)

	tagBase := core.Choice(core.Rune(emojiCharacterClass), presentation, modifier)
	zwjElement := core.Choice(
		tagBase,
		keycap,
		flag,
		core.Sequence(tagBase, tag),
	)
	return core.Sequence(
		firstElement,
		core.ZeroOrMore(core.Sequence(core.Literal(emojiJoiner), zwjElement)),
	)
}
