package special

import (
	"strings"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// The classes of URL, named after the rules of RFC 3986 that use them.
var (
	// urlUnreservedClass is [A-Za-z0-9\-._~]: unreserved (RFC 3986 section 2.3).
	urlUnreservedClass = core.ASCIILetter.Or(core.ASCIIDigit).Or(core.RuneSet("-._~"))

	// urlSubDelimsClass is [!$&'()*+,;=]: sub-delims (RFC 3986 section 2.2).
	urlSubDelimsClass = core.RuneSet("!$&'()*+,;=")

	// urlRegNameClass is [A-Za-z0-9\-._~!$&'()*+,;=]: the runes of a reg-name
	// other than a percent-encoding (RFC 3986 section 3.2.2).
	urlRegNameClass = urlUnreservedClass.Or(urlSubDelimsClass)

	// urlUserinfoClass is [A-Za-z0-9\-._~!$&'()*+,;=:]: the runes of a
	// userinfo other than a percent-encoding (RFC 3986 section 3.2.1).
	urlUserinfoClass = urlRegNameClass.Or(core.RuneSet(":"))

	// urlPathClass is [A-Za-z0-9\-._~!$&'()*+,;=:@]: pchar other than a
	// percent-encoding (RFC 3986 section 3.3).
	urlPathClass = urlUserinfoClass.Or(core.RuneSet("@"))

	// urlQueryClass is [A-Za-z0-9\-._~!$&'()*+,;=:@/?]: the runes of a query
	// or a fragment other than a percent-encoding (RFC 3986 sections 3.4
	// and 3.5).
	urlQueryClass = urlPathClass.Or(core.RuneSet("/?"))

	// urlIPv6Class is [0-9A-Fa-f:.]: the runes of an IPv6address (RFC 3986
	// section 3.2.2), of which the rule checks only the runes.
	urlIPv6Class = core.ASCIIHexDigit.Or(core.RuneSet(":."))
)

// URL returns a rule for an absolute http, https or ftp URL: a subset of the
// URI syntax of RFC 3986, "Uniform Resource Identifier (URI): Generic Syntax".
// In the ABNF of RFC 3986, the rule matches
//
//	URL         = scheme "://" authority path-abempty
//	              [ "?" query ] [ "#" fragment ]
//	scheme      = "http" / "https" / "ftp"         ; ASCII, any case
//	authority   = [ userinfo "@" ] host [ ":" port ]
//	host        = IP-literal / reg-name
//	IP-literal  = "[" ( 1*( HEXDIG / ":" / "." ) / IPvFuture ) "]"
//	IPvFuture   = "v" 1*HEXDIG "." 1*( unreserved / sub-delims / ":" )
//	reg-name    = 1*( unreserved / pct-encoded / sub-delims )
//
// with userinfo, port, path-abempty, query, fragment, pct-encoded, unreserved
// and sub-delims as in RFC 3986. As a regular expression:
//
//	(?:[hH][tT][tT][pP][sS]?|[fF][tT][pP])://(?:(?:[A-Za-z0-9\-._~!$&'()*+,;=:]|%[0-9A-Fa-f]{2})*@)?
//	(?:\[(?:[0-9A-Fa-f:.]+|[vV][0-9A-Fa-f]+\.[A-Za-z0-9\-._~!$&'()*+,;=:]+)\]
//	|(?:[A-Za-z0-9\-._~!$&'()*+,;=]|%[0-9A-Fa-f]{2})+)(?::[0-9]*)?
//	(?:/(?:[A-Za-z0-9\-._~!$&'()*+,;=:@]|%[0-9A-Fa-f]{2})*)*
//	(?:\?(?:[A-Za-z0-9\-._~!$&'()*+,;=:@/?]|%[0-9A-Fa-f]{2})*)?
//	(?:#(?:[A-Za-z0-9\-._~!$&'()*+,;=:@/?]|%[0-9A-Fa-f]{2})*)?
//
// The deviations from RFC 3986:
//
//   - Only the schemes http, https and ftp, and only an absolute URI with an
//     authority ("//"): no relative reference, no "mailto:" and no other
//     scheme. The scheme is matched in ASCII without case, as section 3.1
//     allows; no Unicode case folding applies.
//   - The host is not empty, as RFC 9110 requires for http and https.
//   - An IPv6 address is checked only for its runes: "[::1]" and
//     "[2001:db8::7]" match, and so does "[1:::.]". An IPv4 address is a
//     reg-name, as the grammar allows.
//   - Only ASCII: an IRI (RFC 3987) is not matched beyond its first rune
//     outside ASCII, and "%" must start a percent-encoding.
//
// The rule matches the longest URL that the grammar allows. RFC 3986 allows
// a full stop, a comma, a closing parenthesis and other sub-delims at the end
// of a URL, so in prose "see http://example.com/a." matches with the full
// stop. Appendix C of RFC 3986 recommends delimiting a URI in text with angle
// brackets, and the rule stops at '>', at a space and at '"'. A "%" that no
// two hexadecimal digits follow ends the URL before it: on
// "http://a/b%2" the rule matches "http://a/b".
//
// At end of input before the host it matches nothing; after the first rune of
// the host it ends with the longest URL read so far, and inside a
// percent-encoding it excludes the partial encoding with a pushback chain.
//
// Cost: linear in the URL length. The parts are repetitions of core rules
// without lookahead, so their runs are merged.
func URL() bytescan.Rule {
	userinfo := core.Sequence(core.ZeroOrMore(urlPart(urlUserinfoClass)), core.Literal("@"))
	ipLiteral := core.Sequence(
		core.Literal("["),
		core.Choice(
			core.RuneRun(urlIPv6Class),
			core.Sequence(
				core.Rune(core.RuneSet("vV")),
				core.RuneRun(core.ASCIIHexDigit),
				core.Literal("."),
				core.RuneRun(urlUserinfoClass),
			),
		),
		core.Literal("]"),
	)
	host := core.Choice(ipLiteral, core.OneOrMore(urlPart(urlRegNameClass)))
	port := core.Sequence(core.Literal(":"), core.ZeroOrMore(core.Rune(core.ASCIIDigit)))
	path := core.ZeroOrMore(core.Sequence(core.Literal("/"), core.ZeroOrMore(urlPart(urlPathClass))))
	query := core.Sequence(core.Literal("?"), core.ZeroOrMore(urlPart(urlQueryClass)))
	fragment := core.Sequence(core.Literal("#"), core.ZeroOrMore(urlPart(urlQueryClass)))
	return core.Sequence(
		core.Choice(asciiIgnoreCase("http"), asciiIgnoreCase("https"), asciiIgnoreCase("ftp")),
		core.Literal("://"),
		core.Optional(userinfo),
		host,
		core.Optional(port),
		path,
		core.Optional(query),
		core.Optional(fragment),
	)
}

// urlPart returns a rule for one rune of c or one percent-encoding:
// [c]|%[0-9A-Fa-f]{2}, the pct-encoded of RFC 3986 section 2.1.
func urlPart(c core.Class) bytescan.Rule {
	return core.Choice(
		core.Rune(c),
		core.Sequence(core.Literal("%"), core.Rune(core.ASCIIHexDigit), core.Rune(core.ASCIIHexDigit)),
	)
}

// asciiIgnoreCase returns a rule for the ASCII text s in any ASCII case, as
// (?i)s with folding limited to ASCII letters: 'K' (U+212A) does not match
// "k". It panics if s is empty or not ASCII.
func asciiIgnoreCase(s string) bytescan.Rule {
	if s == "" {
		panic("special: asciiIgnoreCase: empty text")
	}
	runes := make([]bytescan.Rule, 0, len(s))
	for _, r := range s {
		if r > 0x7F {
			panic("special: asciiIgnoreCase: text is not ASCII")
		}
		runes = append(runes, core.Rune(core.RuneSet(strings.ToLower(string(r))+strings.ToUpper(string(r)))))
	}
	return core.Sequence(runes...)
}
