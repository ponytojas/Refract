#!/usr/bin/env python3
"""Build a large LaTeX project as a zip, for testing the sidebar (file tree, outline).

It has ~200 files across nested folders and a main file whose includes give an
outline of several hundred headings. Import it with:

    python3 scripts/make_scroll_fixture.py /tmp/big.zip
    curl -s -X POST -H 'X-Refract: 1' --data-binary @/tmp/big.zip \
      'http://127.0.0.1:8080/api/projects/import?name=Sidebar%20scroll%20test'
"""

import sys
import zipfile

CHAPTERS = 12
SECTIONS = 8
SUBSECTIONS = 4


def chapter(c: int) -> str:
    out = [f"\\chapter{{Chapter {c}: a fairly long title to test truncation}}\n"]
    for s in range(1, SECTIONS + 1):
        out.append(f"\\section{{Section {c}.{s}}}\nSome text for section {c}.{s}.\n\n")
        for u in range(1, SUBSECTIONS + 1):
            out.append(f"\\subsection{{Subsection {c}.{s}.{u}}}\nLorem ipsum dolor sit amet.\n\n")
    return "".join(out)


def main(dest: str) -> None:
    with zipfile.ZipFile(dest, "w", zipfile.ZIP_DEFLATED) as z:
        includes = "".join(f"\\include{{chapters/ch{c:02d}}}\n" for c in range(1, CHAPTERS + 1))
        z.writestr(
            "main.tex",
            "\\documentclass{report}\n\\begin{document}\n\\tableofcontents\n"
            + includes
            + "\\end{document}\n",
        )
        for c in range(1, CHAPTERS + 1):
            z.writestr(f"chapters/ch{c:02d}.tex", chapter(c))
        for folder in ("figures", "tables", "appendix", "notes", "data"):
            for i in range(1, 31):
                z.writestr(f"{folder}/{folder}-{i:02d}.tex", f"% {folder} {i}\n")
        for i in range(1, 6):
            for j in range(1, 6):
                z.writestr(f"deep/level{i}/sub{j}/file-{i}-{j}.tex", "% nested\n")
        for i in range(1, 21):
            z.writestr(f"root-file-{i:02d}.tex", "% root\n")


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "big.zip")
