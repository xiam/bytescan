// Command benchreport runs the reference cases of docs/specs/measurement.md
// in their three lanes, interleaved, and writes a report and its raw samples.
//
// Run it from the module root, inside a memory-capped scope:
//
//	systemd-run --user --scope -p MemoryMax=3G -p MemorySwapMax=0 --quiet \
//		go run ./internal/bench/cmd/benchreport
//
// It refuses to run outside a cgroup with a memory limit. It builds two test
// binaries of internal/bench: the normal build, which runs the control and the
// wrapped lanes, and the build with the tag bench, which runs the
// instrumented lane. Each run is one process that lexes one case once in one
// lane (BenchmarkCase with -test.benchtime 1x). A round runs the three lanes
// of a case and any outside references (BenchmarkReference). The first
// run rotates from round to round.
//
// For each case it compares the lanes with benchstat. When the spread of a
// lane is above the limit, it runs the case again with twice the runs; when
// the spread is still above it, the case is inconclusive.
//
// It writes <out>/<kind>-YYYY-MM-DD.md and the raw output of every lane in
// <out>/raw/YYYY-MM-DD-<sha12>/<case>-<lane>.txt, and of every reference in
// <case>-ref-<tool>.txt. The samples of a first set
// that a rerun replaced are kept in the subdirectory superseded/. benchstat
// -col lane over the files of the raw directory gives the medians of the
// report.
//
// With -kind compare -base <ref> -head <ref>, it compares two commits
// instead: it builds the test binary of each ref once, the normal build, in a
// clean worktree of that ref, runs the cases of -cases in the control lane on
// both sides in the same rounds, alternating the side that runs first, and
// compares the head against the base with benchstat. The cases are reference
// cases, such as json,code, or statelex cases, such as A-key-start-64KiB.
// Two refs with the same tree are a same-tree control. It writes
// <out>/compare/YYYY-MM-DD-<base12>-<head12>.md and the raw output of each
// side in <out>/compare/YYYY-MM-DD-<base12>-<head12>/<case>-<side>.txt.
//
// It exits with status 1, and says why, when benchstat is missing, when the
// process has no memory limit, when a ref of a comparison does not resolve or
// does not build, or when a case fails.
//
// The runner is bench.Main. internal-docs/benchmarks/README.md describes
// the flags and every field of the report.
package main

import (
	"os"

	"github.com/xiam/bytescan/internal/bench"
)

func main() {
	os.Exit(bench.Main())
}
