//go:build !bench

package bytescan

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// TestCountersAddNothingToScanner checks the layout rule of
// docs/specs/measurement.md, "The build tag": in the normal build the counter
// fields of Scanner and of the rules processor have size zero, and neither is
// the last field of its struct, because Go pads a struct whose last field has
// size zero.
func TestCountersAddNothingToScanner(t *testing.T) {
	{
		size := unsafe.Sizeof(scanCounters{})
		require.Truef(t, size == 0, "scanCounters has size %d in the normal build, want 0", size)
	}
	{
		size := unsafe.Sizeof(ruleCounters{})
		require.Truef(t, size == 0, "ruleCounters has size %d in the normal build, want 0", size)
	}

	for _, typ := range []reflect.Type{reflect.TypeOf((*Scanner)(nil)).Elem(), reflect.TypeOf((*rulesProcessor)(nil)).Elem()} {
		field, ok := typ.FieldByName("ctr")
		require.Truef(t, ok, "%s has no field ctr", typ)
		require.Falsef(t, field.Index[0] == typ.NumField()-1, "ctr is the last field of %s, so the normal build pads it", typ)
	}
}
