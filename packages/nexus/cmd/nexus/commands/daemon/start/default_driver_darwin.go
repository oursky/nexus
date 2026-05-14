//go:build darwin

package start

// DefaultDriver is empty on macOS so daemon start attempts both process and macvm;
// the default backend is libkrun when dylibs are present after bootstrap, otherwise process.
const DefaultDriver = ""
