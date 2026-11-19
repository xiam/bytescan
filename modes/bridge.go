package modes

import "github.com/xiam/bytescan/internal/modedef"

// The bridge to package statelex (internal/modedef). It adds no exported name
// to this package.
func init() {
	modedef.ReadSet = func(v any) modedef.Set {
		return readSet(v.(*ModeSet))
	}
	modedef.PushMode = func(s any, name string) any {
		return s.(Stack).push(name)
	}
	modedef.PopMode = func(s any) any {
		return s.(Stack).pop()
	}
}

// readSet returns a copy of the definitions of ms. A change to ms after the
// call does not change the copy.
func readSet(ms *ModeSet) modedef.Set {
	set := modedef.Set{Start: ms.start, Modes: make([]modedef.Mode, 0, len(ms.order))}
	for _, name := range ms.order {
		m := ms.modes[name]
		dm := modedef.Mode{Name: name, Rules: make([]modedef.Rule, len(m.rules))}
		for i, r := range m.rules {
			dm.Rules[i] = modedef.Rule{Type: r.typ, Rule: r.rule, Action: bridgeAction(r.act), Next: r.next}
		}
		set.Modes = append(set.Modes, dm)
	}
	return set
}

func bridgeAction(a action) modedef.Action {
	switch a {
	case push:
		return modedef.Push
	case pop:
		return modedef.Pop
	}
	return modedef.Stay
}
