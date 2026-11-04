import { test } from 'node:test'
import assert from 'node:assert/strict'
import { defaultRules, findSections, latexWords, plainWords, selectedWords, sectionAt } from './wordcount.ts'

const text = (source: string, rules = defaultRules) => latexWords(source, rules).map((w) => w.text)

test('plain text: whitespace, Unicode, punctuation, PDF ligatures and line-wrap hyphens', () => {
  assert.deepEqual(
    plainWords("  Hola\t mundo\n\n café — don't well-known 42 ﬂower inter-\nnational ").map((w) => w.text),
    ['Hola', 'mundo', 'café', "don't", 'well-known', '42', 'flower', 'international'],
  )
})

test('LaTeX counts visible arguments, accents and escaped characters', () => {
  assert.deepEqual(
    text(
      String.raw`\section[Short]{Long title} Some \textbf{important \emph{results}} with caf\'{e} and \href{https://example.test}{a link}. 50\% done.`,
    ),
    ['Long', 'title', 'Some', 'important', 'results', 'with', 'cafe', 'and', 'a', 'link', '50', 'done'],
  )
})

test('excludes metadata, comments, citations, bibliography and all common math forms', () => {
  const source = String.raw`\documentclass{article}
\usepackage[utf8]{inputenc}
\newcommand{\secret}{hidden words}
\begin{document}
Read % ignored words
this $x + y$ and $$z$$ \(x\) \[y\].
\begin{align}a &= b \\ c &= d\end{align}
\cite[see][p. 2]{key} \ref{label} \label{label}
\begin{thebibliography}{99}\bibitem{key} Hidden book\end{thebibliography}
\end{document}`
  assert.deepEqual(text(source), ['Read', 'this', 'and'])
})

test('rules consistently include or exclude headings, notes, captions, table prose and bibliography', () => {
  const source = String.raw`\section{Heading} Body\footnote{Foot note}\caption[Short]{Long caption}\begin{tabular}{ll}Cell & Contents\end{tabular}\begin{thebibliography}{9}\bibitem{key}Book title\end{thebibliography}`
  assert.deepEqual(text(source), ['Heading', 'Body', 'Foot', 'note', 'Long', 'caption', 'Cell', 'Contents'])
  assert.deepEqual(
    text(source, {
      ...defaultRules,
      titles: false,
      footnotes: false,
      captions: false,
      tables: false,
      bibliography: true,
    }),
    ['Body', 'Book', 'title'],
  )
})

test('selection uses document context and source positions', () => {
  const source = String.raw`Visible \textbf{bold words} $hidden math$ % secret text`
  const words = latexWords(source)
  const from = source.indexOf('bold')
  assert.deepEqual(
    selectedWords(words, from, from + 10).map((w) => w.text),
    ['bold', 'words'],
  )
  assert.equal(selectedWords(words, source.indexOf('hidden'), source.length).length, 0)
  assert.equal(source.slice(words[1].from, words[1].to), 'bold')
})

test('sections: abstract, hierarchical ends, comments and repeated headings', () => {
  const source = String.raw`% \section{Fake}
\begin{abstract}Short summary\end{abstract}
\section{First}One\subsection{Child}Two\section{First}Three\end{document}`
  const sections = findSections(source)
  assert.deepEqual(
    sections.map((s) => s.name),
    ['Abstract', 'First', 'Child', 'First'],
  )
  assert.equal(source.slice(sections[0].from, sections[0].to), 'Short summary')
  assert.equal(source.slice(sections[1].from, sections[1].to), String.raw`One\subsection{Child}Two`)
  assert.equal(source.slice(sections[3].from, sections[3].to), 'Three')
  assert.notEqual(sections[1].id, sections[3].id)
})

test('right-click positions recognize headings, section bodies and the innermost subsection', () => {
  const source = String.raw`\begin{abstract}Summary\end{abstract}
\section{Main}Body\subsection{Nested}Child\section{Next}Last\end{document}`
  assert.equal(sectionAt(source, source.indexOf('abstract'))?.name, 'Abstract')
  assert.equal(sectionAt(source, source.indexOf('Summary'))?.name, 'Abstract')
  assert.equal(sectionAt(source, source.indexOf('Main'))?.name, 'Main')
  assert.equal(sectionAt(source, source.indexOf('Body'))?.name, 'Main')
  assert.equal(sectionAt(source, source.indexOf('Nested'))?.name, 'Nested')
  assert.equal(sectionAt(source, source.indexOf('Child'))?.name, 'Nested')
  assert.equal(sectionAt(source, source.indexOf('Next'))?.name, 'Next')
  assert.equal(sectionAt(source, source.length), undefined)
})

test('literal examples and command definitions do not split section budgets or create abstracts', () => {
  const examples = [
    String.raw`\begin{verbatim}\section{Fake}\begin{abstract}Fake\end{abstract}\end{verbatim}`,
    String.raw`\begin{Verbatim}\section{Fake}\end{Verbatim}`,
    String.raw`\begin{verbatim*}\section{Fake}\end{verbatim*}`,
    String.raw`\begin{lstlisting}\section{Fake}\end{lstlisting}`,
    String.raw`\begin{minted}{latex}\section{Fake}\end{minted}`,
    String.raw`\begin{comment}\section{Fake}\end{comment}`,
    String.raw`\verb|\section{Fake}|`,
    String.raw`\verb*|\section{Fake}|`,
    String.raw`\newcommand{\example}[1][default]{\section{Fake} {#1}}`,
    String.raw`\renewcommand\example{\section{Fake}}`,
    String.raw`\def\example#1{\section{Fake} {#1}}`,
  ]
  for (const example of examples) {
    const source = String.raw`\section{Real}Before ${example} After\section{Next}Last`
    const sections = findSections(source)
    assert.deepEqual(
      sections.map((s) => s.name),
      ['Real', 'Next'],
      example,
    )
    assert.equal(source.slice(sections[0].from, sections[0].to), `Before ${example} After`)
    assert.equal(source.slice(sections[1].from, sections[1].to), 'Last')
  }
})
