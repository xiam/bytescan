//go:build !bench

package cursor

import (
	"reflect"
	"testing"
	"unsafe"
)

// TestCountersAddNothingToReader checks the layout rule of
// docs/specs/measurement.md, "The build tag": in the normal build the counter
// field of Reader has size zero, and it is not the last field, because Go pads
// a struct whose last field has size zero.
func TestCountersAddNothingToReader(t *testing.T) {
	if size := unsafe.Sizeof(readerCounters{}); size != 0 {
		t.Fatalf("readerCounters has size %d in the normal build, want 0", size)
	}

	typ := reflect.TypeOf((*Reader)(nil)).Elem()
	field, ok := typ.FieldByName("ctr")
	if !ok {
		t.Fatal("Reader has no field ctr")
	}
	if field.Index[0] == typ.NumField()-1 {
		t.Fatal("ctr is the last field of Reader, so the normal build pads Reader")
	}
}
