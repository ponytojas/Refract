package compile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/fonts"
)

func TestCompileBundledFontFamilies(t *testing.T) {
	tc := tectonic(t)
	tc.Fonts = fonts.NewResolver(t.TempDir())
	dir := t.TempDir()
	source := `\documentclass{article}
\usepackage{fontspec}
\IfFontExistsTF{Latin Modern Sans}{\setmainfont[Scale=1]{Latin Modern Sans}}{\errmessage{Named font not resolved}}
\setsansfont{TeX Gyre Heros}[Scale=0.9]
\setmonofont{Latin Modern Mono}
\newfontfamily\heading{TeX Gyre Pagella}
\newfontface\face{Latin Modern Roman}
\newfontfamily\explicit{Latin Modern Sans}[Path={},UprightFont=texgyretermes-regular.otf]
\typeout{REFRACT-JOBNAME:\jobname}
\begin{document}
Named font. \textbf{Bold} \textit{Italic} \textbf{\textit{Bold italic}}.
\par {\sffamily Sans} {\ttfamily Mono} {\heading Heading} {\face Face}.
\par {\explicit Explicit file selection} {\fontspec{Latin Modern Mono} Direct selection}.
\input{section}
\end{document}
`
	write(t, dir, "sub/main.tex", source)
	write(t, dir, "sub/section.tex", "Section from the main file's directory.\n")
	res, err := tc.Compile(context.Background(), dir, "sub/main.tex")
	if err != nil || !res.OK || res.PDF == "" {
		t.Fatalf("compile: %+v, %v", res, err)
	}
	for _, p := range res.Problems {
		if p.Kind == "absolute-path" {
			t.Fatalf("OS font lookup: %+v", p)
		}
	}
	got, _ := os.ReadFile(filepath.Join(dir, "sub/main.tex"))
	if string(got) != source {
		t.Fatal("compilation changed the document")
	}
	if _, err := os.Stat(filepath.Join(dir, BuildDir, "main.synctex.gz")); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(filepath.Join(dir, BuildDir, "main.log"))
	if !strings.Contains(string(log), "REFRACT-JOBNAME:main") {
		t.Fatal("driver changed jobname")
	}
	if !strings.Contains(string(pdfObjects(t, filepath.Join(dir, res.PDF))), "TeXGyreTermes") {
		t.Fatal("explicit file selection was overridden")
	}
	line := strings.Count(strings.Split(source, "Named font.")[0], "\n") + 1
	boxes, syncErr := (&SyncTeX{}).Forward(context.Background(), dir, "sub/main.tex", "sub/main.tex", line, 0)
	if syncErr != nil && !strings.Contains(syncErr.Error(), "executable file not found") {
		t.Fatalf("SyncTeX with prepared sources: %v", syncErr)
	}
	if syncErr == nil {
		box := boxes[0]
		file, _, err := (&SyncTeX{}).Inverse(context.Background(), dir, "sub/main.tex", box.Page, box.X+box.W/2, box.Y+box.H/2)
		if err != nil || file != "sub/main.tex" {
			t.Fatalf("inverse SyncTeX: %s %v", file, err)
		}
	}
	write(t, dir, "sub/main.tex", strings.Replace(source, "Named font.", `Named font. \undefinedcommand`, 1))
	bad, err := tc.Compile(context.Background(), dir, "sub/main.tex")
	if err != nil || bad.OK {
		t.Fatalf("expected source error: %+v %v", bad, err)
	}
	found := false
	for _, p := range bad.Problems {
		if p.File == "sub/main.tex" && p.Line == line && strings.Contains(p.Message, "Undefined control sequence") {
			found = true
		}
	}
	if !found {
		t.Fatalf("lost original source location: %+v", bad.Problems)
	}
}

func TestCompileFontsAndBibTeX(t *testing.T) {
	tc := tectonic(t)
	tc.Fonts = fonts.NewResolver(t.TempDir())
	dir := t.TempDir()
	write(t, dir, "sub/main.tex", `\documentclass{article}
\usepackage{fontspec}
\setmainfont{Latin Modern Sans}
\begin{document}
A reference: \cite{example2024}.
\bibliographystyle{plain}
\bibliography{references}
\end{document}
`)
	write(t, dir, "sub/references.bib", `@article{example2024,author={Example, Ada},title={Synthetic bibliography test},journal={Example Journal},year={2024}}`)
	res, err := tc.Compile(context.Background(), dir, "sub/main.tex")
	if err != nil || !res.OK {
		t.Fatalf("compile: %+v %v", res, err)
	}
	log, err := os.ReadFile(filepath.Join(dir, BuildDir, "main.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "main.bbl") || strings.Contains(string(log), "No file main.bbl") || strings.Contains(string(log), "undefined") {
		t.Fatalf("BibTeX did not resolve the reference: %s", log)
	}
}

func TestCompileBOMMainWithFontsAndBibliography(t *testing.T) {
	tc := tectonic(t)
	tc.Fonts = fonts.NewResolver(t.TempDir())
	dir := t.TempDir()
	source := "\ufeff" + `\documentclass{article}
\usepackage{fontspec}
\setmainfont{Latin Modern Sans}
\begin{document}
A reference: \cite{example2024}.
\bibliographystyle{plain}
\bibliography{references}
\end{document}
`
	write(t, dir, "main.tex", source)
	write(t, dir, "references.bib", `@article{example2024,author={Example, Ada},title={Synthetic bibliography test},journal={Example Journal},year={2024}}`)
	res, err := tc.Compile(context.Background(), dir, "main.tex")
	if err != nil || !res.OK || res.PDF == "" {
		t.Fatalf("compile: %+v %v", res, err)
	}
	log, err := os.ReadFile(filepath.Join(dir, BuildDir, "main.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "main.bbl") || strings.Contains(string(log), "No file main.bbl") || strings.Contains(string(log), "undefined") {
		t.Fatalf("BOM document's bibliography was not resolved: %s", log)
	}
	original, err := os.ReadFile(filepath.Join(dir, "main.tex"))
	if err != nil || string(original) != source {
		t.Fatal("original source, including its BOM, changed")
	}
}

func TestCompileFontsAndAPABibliography(t *testing.T) {
	if os.Getenv("REFRACT_TEST_BIBER") != "1" {
		t.Skip("set REFRACT_TEST_BIBER=1 with a Biber matching the TeX bundle")
	}
	tc := tectonic(t)
	tc.Fonts = fonts.NewResolver(t.TempDir())
	dir := t.TempDir()
	write(t, dir, "paper.tex", `\documentclass{article}
\usepackage{fontspec}
\setmainfont{Latin Modern Sans}
\usepackage{csquotes}
\usepackage[backend=biber,style=apa]{biblatex}
\addbibresource{references.bib}
\begin{document}
A reference: \parencite{example2024}.
\printbibliography
\end{document}
`)
	write(t, dir, "references.bib", `@article{example2024,author={Example, Ada},title={Synthetic bibliography test},journaltitle={Example Journal},date={2024}}`)
	res, err := tc.Compile(context.Background(), dir, "paper.tex")
	if err != nil || !res.OK {
		t.Fatalf("compile: %+v %v", res, err)
	}
	log, err := os.ReadFile(filepath.Join(dir, BuildDir, "paper.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "paper.bbl' found") || strings.Contains(string(log), "Empty bibliography") || strings.Contains(string(log), "undefined") {
		t.Fatalf("Biber did not resolve the APA reference: %s", log)
	}
}

// Opt in to the real provider check. Unit tests use an HTTP transport fixture.
func TestCompileGoogleFontFamilies(t *testing.T) {
	if os.Getenv("REFRACT_TEST_GOOGLE_FONTS") != "1" {
		t.Skip("set REFRACT_TEST_GOOGLE_FONTS=1 for the live provider test")
	}
	tc := tectonic(t)
	tc.Fonts = fonts.NewResolver(t.TempDir())
	dir := t.TempDir()
	write(t, dir, "main.tex", `\documentclass{article}
\usepackage{fontspec}
\IfFontExistsTF{Open Sans}{\setmainfont{Open Sans}}{\errmessage{Open Sans not resolved}}
\setsansfont{Inter}
\begin{document}
Open Sans: áéíóú ñ. \textbf{Bold} \textit{Italic} \textbf{\textit{Bold italic}}.
\par {\sffamily Inter: \textbf{Bold} \textit{Italic} \textbf{\textit{Bold italic}}.}
\end{document}
`)
	res, err := tc.Compile(context.Background(), dir, "main.tex")
	if err != nil || !res.OK {
		t.Fatalf("compile: %+v, %v", res, err)
	}
	pdf := pdfObjects(t, filepath.Join(dir, res.PDF))
	for _, name := range []string{"OpenSans", "Inter"} {
		if !strings.Contains(string(pdf), name) {
			t.Errorf("%s not embedded in the PDF", name)
		}
	}
	// Public downloads survive a new resolver without network access.
	tc.Fonts = fonts.NewResolver(tc.Fonts.CacheDir)
	tc.Fonts.Offline = true
	if again, err := tc.Compile(context.Background(), dir, "main.tex"); err != nil || !again.OK {
		t.Fatalf("offline cache: %+v %v", again, err)
	}
	// Uploaded files are discovered by metadata, even from a nested main file
	// on a server with an empty public cache and downloads disabled.
	files, err := filepath.Glob(filepath.Join(dir, ".refract/fonts/*/*.ttf"))
	if err != nil || len(files) != 8 {
		t.Fatalf("font files: %v %v", files, err)
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(filepath.Join(dir, ".refract/fonts"), file)
		write(t, dir, filepath.Join("assets", rel), string(b))
	}
	source, _ := os.ReadFile(filepath.Join(dir, "main.tex"))
	write(t, dir, "nested/main.tex", string(source))
	tc.Fonts = fonts.NewResolver(t.TempDir())
	tc.Fonts.Offline = true
	if local, err := tc.Compile(context.Background(), dir, "nested/main.tex"); err != nil || !local.OK {
		t.Fatalf("uploaded fonts: %+v %v", local, err)
	}
}
