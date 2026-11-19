//go:build !windows

package ui

import "github.com/atotto/clipboard"

// systemCopy puts text on the system clipboard with
// github.com/atotto/clipboard (xclip, xsel or wl-copy on Linux and BSD, the
// native clipboard on macOS), next to Bubble Tea's OSC 52 copy, which some
// terminals (e.g. VTE-based ones) ignore. Without a tool it does nothing.
// Tests replace it.
var systemCopy = func(text string) {
	_ = clipboard.WriteAll(text)
}
