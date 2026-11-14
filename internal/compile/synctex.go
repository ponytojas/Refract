package compile

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrNoSync means SyncTeX had no answer (no build yet, or nothing at that spot).
var ErrNoSync = errors.New("no synctex match")

// Box is a region of a PDF page in PDF points, origin at the top-left corner.
type Box struct {
	Page int     `json:"page"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

// SyncTeX wraps the synctex CLI over the last build of a project.
type SyncTeX struct {
	Bin string // "synctex" if empty
}

// Forward maps a source position (project-relative file, 1-based line) to PDF boxes.
func (s *SyncTeX) Forward(ctx context.Context, projectDir, mainFile, file string, line, col int) ([]Box, error) {
	pdf, root, err := buildPaths(projectDir, mainFile)
	if err != nil {
		return nil, err
	}
	// SyncTeX records absolute, symlink-resolved input paths.
	input := filepath.Join(root, filepath.FromSlash(path.Clean("/"+file)))
	out, err := s.run(ctx, "view", "-i", fmt.Sprintf("%d:%d:%s", line, max(col, 0), input), "-o", pdf)
	if err != nil {
		return nil, err
	}
	boxes := parseView(out)
	if len(boxes) == 0 {
		return nil, ErrNoSync
	}
	return boxes, nil
}

// Inverse maps a point on a PDF page to a project-relative source file and line.
func (s *SyncTeX) Inverse(ctx context.Context, projectDir, mainFile string, page int, x, y float64) (string, int, error) {
	pdf, root, err := buildPaths(projectDir, mainFile)
	if err != nil {
		return "", 0, err
	}
	out, err := s.run(ctx, "edit", "-o", fmt.Sprintf("%d:%.2f:%.2f:%s", page, x, y, pdf))
	if err != nil {
		return "", 0, err
	}
	input, line := parseEdit(out)
	if input == "" {
		return "", 0, ErrNoSync
	}
	rel, err := filepath.Rel(root, filepath.Clean(input))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", 0, fmt.Errorf("%w: that spot comes from a file outside the project", ErrNoSync)
	}
	return filepath.ToSlash(rel), line, nil
}

func buildPaths(projectDir, mainFile string) (pdf, root string, err error) {
	if projectDir, err = filepath.Abs(projectDir); err != nil { // synctex records absolute paths
		return "", "", err
	}
	root, err = filepath.EvalSymlinks(projectDir)
	if err != nil {
		return "", "", err
	}
	base := path.Base(mainFile)
	pdf = filepath.Join(root, filepath.FromSlash(BuildDir), strings.TrimSuffix(base, path.Ext(base))+".pdf")
	if _, err := os.Stat(pdf); err != nil {
		return "", "", fmt.Errorf("%w: compile first", ErrNoSync)
	}
	return pdf, root, nil
}

func (s *SyncTeX) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cmp.Or(s.Bin, "synctex"), args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", err
	}
	return string(out), nil // synctex exits non-zero on "no match" but still prints
}

// parseView reads `synctex view` records ("Page:1", "h:…", "v:…", "W:…", "H:…").
// h,v is the left end of the baseline; the box spans H above it.
func parseView(out string) []Box {
	var boxes []Box
	var cur *Box
	var v float64
	for sc := bufio.NewScanner(strings.NewReader(out)); sc.Scan(); {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f, _ := strconv.ParseFloat(val, 64)
		switch key {
		case "Page":
			boxes = append(boxes, Box{Page: int(f)})
			cur = &boxes[len(boxes)-1]
		case "h":
			if cur != nil {
				cur.X = f
			}
		case "v":
			v = f
		case "W":
			if cur != nil {
				cur.W = f
			}
		case "H":
			if cur != nil {
				cur.H = f
				cur.Y = v - f
			}
		}
	}
	return boxes
}

// parseEdit reads the first `synctex edit` record ("Input:/abs/file.tex", "Line:12").
func parseEdit(out string) (input string, line int) {
	for sc := bufio.NewScanner(strings.NewReader(out)); sc.Scan(); {
		key, val, _ := strings.Cut(sc.Text(), ":")
		switch key {
		case "Input":
			if input != "" {
				return input, line
			}
			input = val
		case "Line":
			line, _ = strconv.Atoi(val)
		}
	}
	return input, line
}
