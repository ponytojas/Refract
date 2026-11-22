//go:build !darwin

package main

// excludeFromBackup is a macOS (Time Machine) matter.
func excludeFromBackup(string) {}
