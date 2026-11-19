// Package modedef is the bridge between package modes and package statelex.
// It is internal, so it adds no name to the API of either package.
//
// Package modes fills the hooks in its init function. Package statelex imports
// modes, so the hooks are set before statelex can call them. The hooks take
// and return the values of modes as any, because this package cannot import
// modes: modes imports it.
package modedef

import "github.com/xiam/bytescan"

// Action is what a rule does to the mode stack after its token.
type Action int

const (
	// Stay leaves the stack unchanged.
	Stay Action = iota
	// Push pushes the mode Rule.Next.
	Push
	// Pop pops the top mode. A pop of the last mode does nothing.
	Pop
)

// Rule is one rule of a mode, with its action.
type Rule struct {
	Type   bytescan.TokenType
	Rule   bytescan.Rule
	Action Action
	Next   string // the mode that Push pushes
}

// Mode is one named mode: its rules in registration order.
type Mode struct {
	Name  string
	Rules []Rule
}

// Set is a copy of the definitions of a modes.ModeSet: its start mode and its
// modes in the order of their first ModeSet.Mode call.
type Set struct {
	Start string
	Modes []Mode
}

// The hooks that package modes sets.
var (
	// ReadSet returns a copy of the definitions of a *modes.ModeSet.
	ReadSet func(ms any) Set
	// PushMode returns the modes.Stack s with the mode name pushed on top.
	PushMode func(s any, name string) any
	// PopMode returns the modes.Stack s without its top mode. It leaves a
	// stack of one mode unchanged.
	PopMode func(s any) any
)
