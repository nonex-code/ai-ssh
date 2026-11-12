//go:build !windows

package main

// attachConsole is a Windows concern: on Unix a process inherits its terminal
// normally, whether or not it also has a GUI.
func attachConsole() {}
