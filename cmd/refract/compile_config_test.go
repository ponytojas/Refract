package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseCompileConfig(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

	c, err := parseCompileConfig(env(nil))
	if err != nil || c.sandbox != "none" || c.workers < 1 || c.maxWait != 20*time.Second || c.timeout != 90*time.Second {
		t.Errorf("defaults = %+v, %v", c, err)
	}
	c, err = parseCompileConfig(env(map[string]string{"REFRACT_SANDBOX": "bwrap", "REFRACT_COMPILE_WORKERS": "3", "REFRACT_COMPILE_MAX_WAIT": "5", "REFRACT_COMPILE_TIMEOUT": "60"}))
	if err != nil || c.sandbox != "bwrap" || c.workers != 3 || c.maxWait != 5*time.Second || c.timeout != 60*time.Second {
		t.Errorf("set = %+v, %v", c, err)
	}
	c, err = parseCompileConfig(env(map[string]string{"REFRACT_COMPILE_MAX_WAIT": "0"}))
	if err != nil || c.maxWait != 0 {
		t.Errorf("max wait 0 = %+v, %v; want it accepted", c, err)
	}
	for name, value := range map[string]string{
		"REFRACT_SANDBOX":          "docker",
		"REFRACT_COMPILE_WORKERS":  "0",
		"REFRACT_COMPILE_MAX_WAIT": "soon",
		"REFRACT_COMPILE_TIMEOUT":  "-5",
	} {
		if _, err := parseCompileConfig(env(map[string]string{name: value})); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=%s: err = %v; want it named", name, value, err)
		}
	}
}
