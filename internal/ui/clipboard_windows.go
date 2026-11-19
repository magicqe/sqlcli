//go:build windows

package ui

// systemCopy does nothing on Windows: Bubble Tea's OSC 52 copy
// (tea.SetClipboard) puts the text on the clipboard in Windows Terminal.
// Writing the clipboard directly through the Win32 API crashed sqlcli.
// Tests replace it.
var systemCopy = func(text string) {}
