package pty

// MouseModeEnableSeqs are DEC private mode sequences that enable mouse
// reporting in a terminal. When a program inside the PTY sends one of these,
// it means it wants to receive mouse events.
var MouseModeEnableSeqs = []string{
	"\x1b[?1000h", // VT200 X10 — button press only
	"\x1b[?1002h", // button-event tracking
	"\x1b[?1003h", // any-event tracking
	"\x1b[?1006h", // SGR extended mouse
}

// MouseModeDisableSeqs are the corresponding disable sequences.
var MouseModeDisableSeqs = []string{
	"\x1b[?1000l",
	"\x1b[?1002l",
	"\x1b[?1003l",
	"\x1b[?1006l",
}
