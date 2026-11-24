package tests

import "github.com/xiam/bytescan/internal/leanread"

// The oracle tests and every other test of this package run the commit of the
// lean read path with the check of the extent on: the cursor computes the
// position after each token from its bytes, as Seek does, and the token fails
// when the position from the scanner's extent differs (leanread.Check).
func init() {
	leanread.Check = true
}
