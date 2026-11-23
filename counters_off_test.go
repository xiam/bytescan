//go:build !bench

package bytescan

import (
	"reflect"
	"testing"
	"unsafe"
)

// TestCountersAddNothingToScanner checks the layout rule of
// docs/specs/measurement.md, "The build tag": in the normal build the counter
// fields of Scanner and of the rules processor have size zero, and neither is
// the last field of its struct, because Go pads a struct whose last field has
// size zero.
func TestCountersAddNothingToScanner(t *testing.T) {
	if size := unsafe.Sizeof(scanCounters{}); size != 0 {
		t.Fatalf("scanCounters has size %d in the normal build, want 0", size)
	}
	if size := unsafe.Sizeof(ruleCounters{}); size != 0 {
		t.Fatalf("ruleCounters has size %d in the normal build, want 0", size)
	}

	for _, typ := range []reflect.Type{reflect.TypeOf((*Scanner)(nil)).Elem(), reflect.TypeOf((*rulesProcessor)(nil)).Elem()} {
		field, ok := typ.FieldByName("ctr")
		if !ok {
			t.Fatalf("%s has no field ctr", typ)
		}
		if field.Index[0] == typ.NumField()-1 {
			t.Fatalf("ctr is the last field of %s, so the normal build pads it", typ)
		}
	}
}
