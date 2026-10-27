package compile

import (
	"bytes"
	"compress/zlib"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The testdata files are real Tectonic 0.17.0 output from small synthetic documents.
func TestParseStderr(t *testing.T) {
	cases := []struct {
		file string
		want []Problem
	}{
		{"undefcs.stderr", []Problem{{Severity: "error", Kind: "error", File: "sec/a", Line: 3, Message: "Undefined control sequence"}}},
		{"missingfile.stderr", []Problem{{Severity: "error", Kind: "error", File: "main.tex", Line: 3, Message: "LaTeX Error: File `nothere.tex' not found."}}},
		{"fontspec.stderr", []Problem{{Severity: "error", Kind: "error", File: "main.tex", Line: 4, Message: `Package fontspec Error: The font "Some Font That Does Not Exist" cannot be found.`}}},
		// The overfull box is printed once per TeX pass: reported once.
		{"refs.stderr", []Problem{{Severity: "warning", Kind: "overfull", File: "main.tex", Line: 4, Message: `Overfull \hbox (139.34pt too wide) detected at line 4`}}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			got := parseStderr(testdata(t, c.file))
			if len(got) != len(c.want) {
				t.Fatalf("got %d problems: %+v", len(got), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("got  %+v\nwant %+v", got[i], c.want[i])
				}
			}
		})
	}
}

func TestParseStderrIgnoresContinueOnErrorsSummary(t *testing.T) {
	got := parseStderr("error: main.tex:268: You can't use `macro parameter character #' in horizontal mode\nwarning: errors were issued by the TeX engine, but were ignored; use --print and/or --keep-logs for details.\n")
	if len(got) != 1 || got[0].Line != 268 {
		t.Fatalf("got %+v", got)
	}
}

func TestParseStderrAbsolutePath(t *testing.T) {
	// Seen with \IfFontExistsTF on macOS: fontconfig reads system fonts.
	got := parseStderr("warning: accessing absolute path `/Library/Fonts/OpenSans-Regular.ttf`; build may not be reproducible in other environments\n")
	if len(got) != 1 || got[0].Kind != "absolute-path" || got[0].File != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseLog(t *testing.T) {
	got := parseLog(testdata(t, "refs.log"))
	want := []Problem{
		{Severity: "warning", Kind: "undefined-reference", Line: 3, Message: "Reference `missing' undefined"},
		{Severity: "warning", Kind: "undefined-citation", Line: 3, Message: "Citation `nobody' undefined"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v", got)
	}
}

func TestLocate(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "paper/main.tex", "\\documentclass{article}\n\\begin{document}\n\\input{sec/b}\n\\end{document}\n")
	write(t, dir, "paper/sec/b.tex", "x\n\\cite[p.~3]{other, ghost}\n")
	ps := []Problem{
		{Kind: "error", File: "sec/b", Line: 2},                                      // TeX name without .tex, relative to main
		{Kind: "undefined-citation", Line: 2, Message: "Citation `ghost' undefined"}, // found by key
		{Kind: "undefined-reference", Line: 9, Message: "Reference `nowhere' undefined"},
	}
	locate(ps, dir, "paper/main.tex")
	if ps[0].File != "paper/sec/b.tex" {
		t.Errorf("file = %q", ps[0].File)
	}
	if ps[1].File != "paper/sec/b.tex" || ps[1].Line != 2 {
		t.Errorf("citation at %s:%d", ps[1].File, ps[1].Line)
	}
	if ps[2].File != "paper/main.tex" || ps[2].Line != 9 {
		t.Errorf("unfound key at %s:%d, want main file", ps[2].File, ps[2].Line)
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- integration: needs tectonic on PATH (and network on the first run) ---

func tectonic(t *testing.T) *Tectonic {
	t.Helper()
	if _, err := exec.LookPath("tectonic"); err != nil {
		t.Skip("tectonic not installed")
	}
	return &Tectonic{Bundle: os.Getenv("REFRACT_TECTONIC_BUNDLE"), CacheDir: os.Getenv("TECTONIC_CACHE_DIR"), Timeout: 90 * time.Second}
}

func TestCompileErrorsPointToSource(t *testing.T) {
	tc := tectonic(t)
	dir := t.TempDir()
	write(t, dir, "main.tex", "\\documentclass{article}\n\\begin{document}\nHello\n\\input{chapters/one}\n\\end{document}\n")
	write(t, dir, "chapters/one.tex", "Line one\nLine two\n\\undefinedcommand here\n")
	res, err := tc.Compile(context.Background(), dir, "main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || len(res.Problems) == 0 {
		t.Fatalf("want a failed build with problems: %+v", res)
	}
	p := res.Problems[0]
	if p.Severity != "error" || p.File != "chapters/one.tex" || p.Line != 3 {
		t.Fatalf("problem = %+v, want error at chapters/one.tex:3", p)
	}
	if res.PDF == "" {
		t.Fatal("no PDF despite continue-on-errors")
	}
}

func TestCompileOKWithSynctexAndWarnings(t *testing.T) {
	tc := tectonic(t)
	dir := t.TempDir()
	write(t, dir, "main.tex", "\\documentclass{article}\n\\begin{document}\nSee \\ref{nope}.\n\\newpage\nTwo.\n\\end{document}\n")
	res, err := tc.Compile(context.Background(), dir, "main.tex")
	if err != nil || !res.OK {
		t.Fatalf("compile: %+v %v", res, err)
	}
	for _, f := range []string{"main.pdf", "main.synctex.gz", "main.log"} {
		if _, err := os.Stat(filepath.Join(dir, BuildDir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if n := pdfPages(t, filepath.Join(dir, res.PDF)); n != 2 {
		t.Errorf("pages = %d, want 2", n)
	}
	if len(res.Problems) != 1 || res.Problems[0].Kind != "undefined-reference" || res.Problems[0].File != "main.tex" || res.Problems[0].Line != 3 {
		t.Errorf("problems = %+v", res.Problems)
	}
}

// make dev uses REFRACT_DATA_DIR=./data: a relative project dir must work.
func TestCompileRelativeProjectDir(t *testing.T) {
	tc := tectonic(t)
	base := t.TempDir()
	write(t, base, "data/p/sub/main.tex", "\\documentclass{article}\n\\begin{document}\nHi\n\\end{document}\n")
	t.Chdir(base)
	res, err := tc.Compile(context.Background(), "data/p", "sub/main.tex")
	if err != nil || !res.OK || res.PDF == "" {
		t.Fatalf("compile: %+v %v", res, err)
	}
	if _, err := (&SyncTeX{}).Forward(context.Background(), "data/p", "sub/main.tex", "sub/main.tex", 3, 0); err != nil && !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("forward sync with relative dir: %v", err)
	}
}

const infiniteLoop = "\\documentclass{article}\n\\begin{document}\n\\def\\x{\\x}\\x\n\\end{document}\n"

func TestCompileTimeoutKillsTheBuild(t *testing.T) {
	tc := tectonic(t)
	tc.Timeout = 3 * time.Second
	dir := t.TempDir()
	write(t, dir, "main.tex", infiniteLoop)
	start := time.Now()
	res, err := tc.Compile(context.Background(), dir, "main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.OK || time.Since(start) > 8*time.Second {
		t.Fatalf("res = %+v after %s", res, time.Since(start))
	}
}

func TestRunnerNewCompileCancelsRunningOne(t *testing.T) {
	tc := tectonic(t)
	dir := t.TempDir()
	write(t, dir, "main.tex", infiniteLoop)
	r := &Runner{Compiler: tc}
	first := make(chan *Result)
	go func() {
		res, _ := r.Compile(context.Background(), "p", dir, "main.tex")
		first <- res
	}()
	time.Sleep(1500 * time.Millisecond) // let the first build start
	write(t, dir, "main.tex", "\\documentclass{article}\n\\begin{document}\nFixed.\n\\end{document}\n")
	second, err := r.Compile(context.Background(), "p", dir, "main.tex")
	if err != nil || !second.OK {
		t.Fatalf("second compile: %+v %v", second, err)
	}
	select {
	case res := <-first:
		if !res.Superseded {
			t.Fatalf("first = %+v, want superseded", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first compile still running")
	}
}

// pdfObjects includes compressed object streams (stdlib only; for tests).
func pdfObjects(t *testing.T, file string) []byte {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text := raw
	streams := regexp.MustCompile(`(?s)stream\r?\n(.*?)endstream`)
	for _, m := range streams.FindAllSubmatch(raw, -1) {
		if zr, err := zlib.NewReader(bytes.NewReader(m[1])); err == nil {
			if b, err := io.ReadAll(zr); err == nil || len(b) > 0 {
				text = append(text, b...)
			}
			zr.Close()
		}
	}
	return text
}

// pdfPages counts pages from the /Count of the page tree root.
func pdfPages(t *testing.T, file string) int {
	t.Helper()
	text := pdfObjects(t, file)
	pages := regexp.MustCompile(`(?s)/Type\s*/Pages\b(?:[^>]|>[^>])*?/Count\s+(\d+)|/Count\s+(\d+)(?:[^>]|>[^>])*?/Type\s*/Pages\b`)
	max := 0
	for _, m := range pages.FindAllSubmatch(text, -1) {
		n, _ := strconv.Atoi(strings.TrimSpace(string(m[1]) + string(m[2])))
		max = maxInt(max, n)
	}
	if max == 0 {
		t.Fatalf("%s: no page tree found", file)
	}
	return max
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
