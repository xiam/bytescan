//go:build !bench

package cursor

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// TestCountersAddNothingToReader checks the layout rule of
// docs/specs/measurement.md, "The build tag": in the normal build the counter
// field of Reader has size zero, and it is not the last field, because Go pads
// a struct whose last field has size zero.
func TestCountersAddNothingToReader(t *testing.T) {
	{
		size := unsafe.Sizeof(readerCounters{})
		require.Truef(t, size == 0, "readerCounters has size %d in the normal build, want 0", size)
	}

	typ := reflect.TypeOf((*Reader)(nil)).Elem()
	field, ok := typ.FieldByName("ctr")
	require.True(t, ok, "Reader has no field ctr")
	require.False(t, field.Index[0] == typ.NumField()-1, "ctr is the last field of Reader, so the normal build pads Reader")
}
