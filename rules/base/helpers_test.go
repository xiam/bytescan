package base_test

import "github.com/xiam/bytescan/internal/lextest"

// The stream helpers of this package are the shared ones in internal/lextest,
// under short local names. The code tests use their own names, prefixed with
// code, for the same helpers.
var (
	lexStream      = lextest.LexStream
	token          = lextest.Token
	runStreamCases = lextest.RunStreamCases[streamCase]

	codeStream   = lextest.LexStream
	codeToken    = lextest.Token
	codeUnknowns = lextest.Unknowns
	runCodeCases = lextest.RunStreamCases[codeCase]
)

// streamCase is one input of a stream test and its complete expected token
// stream.
type streamCase struct {
	Input    string
	Expected []string
}

// codeCase is one input of a stream test and its complete expected stream.
type codeCase struct {
	Input    string
	Expected []string
}
