//go:build !darwin && !linux

package localmodel

// checkDiskSpace is a no-op where we don't have statfs. Local models are an
// Apple Silicon feature; other platforms never reach the downloader in practice.
func checkDiskSpace(string, int64) error { return nil }
