package special

import (
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// The classes of Email.
var (
	// emailLocalClass is [A-Za-z0-9.!#$%&'*+/=?^_`{|}~\-]: the runes of the
	// part of an address before "@".
	emailLocalClass = core.ASCIILetter.Or(core.ASCIIDigit).Or(core.RuneSet(".!#$%&'*+/=?^_`{|}~-"))

	// emailLabelEndClass is [A-Za-z0-9]: the first and the last rune of a
	// domain label.
	emailLabelEndClass = core.ASCIILetter.Or(core.ASCIIDigit)

	// emailLabelInnerClass is [A-Za-z0-9\-]: the runes of a domain label
	// between its first and its last rune.
	emailLabelInnerClass = emailLabelEndClass.Or(core.RuneSet("-"))
)

// Email returns a rule for an email address in the practical form of the
// "valid email address" of the WHATWG HTML Living Standard (section 4.10.5.1.5,
// the type=email input), not the full addr-spec of RFC 5322:
//
//	[A-Za-z0-9.!#$%&'*+/=?^_`{|}~\-]+@[A-Za-z0-9](?:[A-Za-z0-9\-]{0,61}[A-Za-z0-9])?
//	(?:\.[A-Za-z0-9](?:[A-Za-z0-9\-]{0,61}[A-Za-z0-9])?)*
//
// The part before "@" is a run of the atext runes of RFC 5322 and full stops,
// and the domain is a sequence of labels of RFC 1034 section 3.5, of 1 to 63
// letters, digits and hyphens that start and end with a letter or digit. HTML
// calls this a willful violation of RFC 5322. The deviations from RFC 5322
// section 3.4.1 and RFC 5321 section 4.1.2:
//
//   - No quoted local part ("a b"@example.com), no comment and no folding
//     white space.
//   - A full stop may start or end the local part or repeat in it, as in
//     ".a..b.@example.com", which the dot-atom of RFC 5322 forbids.
//   - No address literal (user@[192.0.2.1]) and no internationalized address
//     (RFC 6531): every rune is ASCII.
//   - A domain of one label, as in "user@localhost", matches. The rule checks
//     no length limit but that of a label, so a label of more than 63 runes
//     ends the match after its 63rd rune; nor does it check that a domain or a
//     top-level label exists.
//
// The rule matches the longest address that the grammar allows. A full stop
// or a hyphen after the domain is not part of it, so in prose "write to
// me@example.com." matches "me@example.com". Every atext rune is allowed
// before "@", so in "'me@example.com'" the match starts with the apostrophe.
//
// At end of input before the first rune of the domain it matches nothing;
// after it, it ends with the longest address read so far, and it excludes a
// final hyphen or full stop with a pushback chain.
//
// Cost: linear in the address length. The parts are repetitions of core
// rules without lookahead, so their runs are merged.
func Email() bytescan.Rule {
	label := core.Sequence(
		core.Rune(emailLabelEndClass),
		core.Optional(core.Sequence(
			core.Repeat(core.Rune(emailLabelInnerClass), 0, 61),
			core.Rune(emailLabelEndClass),
		)),
	)
	return core.Sequence(
		core.RuneRun(emailLocalClass),
		core.Literal("@"),
		label,
		core.ZeroOrMore(core.Sequence(core.Literal("."), label)),
	)
}
