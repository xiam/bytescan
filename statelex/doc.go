// Package statelex is a lexer with state: it holds a text and its tokens, and
// it answers each edit of the text with what changed in the tokens.
//
// The caller says "the bytes [Start, End) became Text" with Apply, and
// receives one Change: these old tokens are removed, these new tokens are
// inserted, and every later token moves by Shift() bytes. The lexer lexes
// again only the tokens that the edit can change, and it stops at the first
// token boundary after the edit where the old scan and the new scan agree. The
// held tokens are always the tokens of a scan of the whole held text from its
// start, with the end of the held text as the end of input.
//
// # Pure rules
//
// The lexer supports pure rules only. A pure rule keeps its progress only in
// the Rule values it returns: it writes no captured variable and changes
// nothing outside itself. The rules of package rules/core and rules/base are
// pure. The lexer cannot check that a rule is pure. An impure rule can give a
// wrong change without an error, because the lexer decides which tokens an
// edit can change from the bytes that each token read, and a captured variable
// is not one of them.
//
// # Modes
//
// New takes the rules as a modes.ModeSet. A rule set without modes is a set
// with one mode. For each held token the lexer keeps the mode state at its
// start, a modes.Stack. A re-lex restarts in the state saved at the restart
// point, and a token boundary after the edit is a sync point only when the
// saved state there equals the state of the re-lex (modes.Stack.Equal). A
// Token and a Change carry no mode state.
//
// # Queries
//
// Besides the change, the lexer answers queries: Len, the bytes of a range
// (Text), the tokens that overlap a range (Tokens), the mode state at the end
// of the text (ModeAtEnd), and the first token whose rules read the end of the
// text (Unfinished). A range is clipped to [0, Len()], and these queries never
// fail. Position and Offset convert between a byte offset and a line, counted
// from 1, and a rune column, counted from 0. No query returns a part of the
// trees that hold the text and the tokens.
//
// # Cost
//
// The text is held in a balanced tree of chunks, and the tokens in a balanced
// tree that keeps, in each node, the summed length of its tokens and the
// furthest point that they read. No token stores an absolute offset. So an
// edit finds the bytes and the tokens that it touches by going down the trees,
// and the tokens after the edit move with no work. The work of an edit grows
// with the size of the edit and with the length of the run of tokens that are
// lexed again, and not with the untouched text after the edit. The rope also
// keeps the newlines and the runes of each subtree, so a query goes down one
// path of a tree and then reads only what it returns.
package statelex
