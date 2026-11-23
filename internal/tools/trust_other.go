//go:build !unix

package tools

// unsafe is always "" here: on Windows the bundled folder is the executable's
// own folder and Refract relies on that folder's permissions (see the README).
func unsafe(string) string { return "" }

func gitTreeUnsafe(string) string { return "" }

func treeUnsafe(string) string { return "" }
