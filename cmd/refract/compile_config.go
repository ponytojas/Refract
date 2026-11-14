package main

import (
	"fmt"
	"runtime"
	"strconv"
	"time"
)

// compileConfig is how compiles are run: see REFRACT_SANDBOX,
// REFRACT_COMPILE_WORKERS, REFRACT_COMPILE_MAX_WAIT and REFRACT_COMPILE_TIMEOUT.
type compileConfig struct {
	sandbox string // "none" or "bwrap"
	workers int
	maxWait time.Duration
	timeout time.Duration
}

func parseCompileConfig(getenv func(string) string) (compileConfig, error) {
	c := compileConfig{
		sandbox: "none",
		workers: max(runtime.NumCPU()-1, 1),
		maxWait: 20 * time.Second,
		timeout: 90 * time.Second,
	}
	if v := getenv("REFRACT_SANDBOX"); v != "" {
		if v != "none" && v != "bwrap" {
			return c, fmt.Errorf("REFRACT_SANDBOX=%q: want none or bwrap", v)
		}
		c.sandbox = v
	}
	for _, f := range []struct {
		name string
		set  func(n int)
	}{
		{"REFRACT_COMPILE_WORKERS", func(n int) { c.workers = n }},
		{"REFRACT_COMPILE_MAX_WAIT", func(n int) { c.maxWait = time.Duration(n) * time.Second }},
		{"REFRACT_COMPILE_TIMEOUT", func(n int) { c.timeout = time.Duration(n) * time.Second }},
	} {
		v := getenv(f.name)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			unit := " of seconds"
			if f.name == "REFRACT_COMPILE_WORKERS" {
				unit = ""
			}
			return c, fmt.Errorf("%s=%q: want a positive number%s", f.name, v, unit)
		}
		f.set(n)
	}
	return c, nil
}
