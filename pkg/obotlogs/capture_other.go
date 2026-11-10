//go:build !unix

package obotlogs

// Capture is not supported on this platform; Obot's log buffer stays empty.
func Capture() error { return nil }

// Stop does nothing on this platform.
func Stop() {}
