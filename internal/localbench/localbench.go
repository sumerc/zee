// Package localbench exists to benchmark the on-device engines side by side.
// It has no runtime code: the benchmark needs to import both internal/parakeet
// and internal/whisper and dispatch per model, which neither engine package can
// do without depending on the other. Everything lives in bench_test.go.
//
//	make bench-local     # every downloaded model x every clip
//	make bench-save      # same, appended to benchmark.txt as a labelled block
package localbench
