import type { CompletionHelp } from './completion'
import { t } from './i18n'

// The arguments of a command, as shown next to its name.
const arg = (name: string) => `{${name}}`
const opt = (name: string) => `[${name}]`

const address = arg(t('address'))
const file = arg(t('file'))
const keys = arg(t('keys'))
const label = arg(t('label'))
const options = opt(t('options'))
const text = arg(t('text'))
const title = arg(t('title'))
const citation = opt(t('note')) + keys
const heading = opt(t('short title')) + title

export const completionHelp: CompletionHelp = {
  // Structure
  '\\documentclass': { detail: options + arg(t('class')), info: t('Sets the type of document.') },
  '\\usepackage': { detail: options + arg(t('package')), info: t('Loads a package.') },
  '\\title': { detail: title, info: t('Sets the title that \\maketitle prints.') },
  '\\author': { detail: arg(t('names')), info: t('Sets the author that \\maketitle prints.') },
  '\\date': { detail: arg(t('date')), info: t('Sets the date that \\maketitle prints. Leave it empty for none.') },
  '\\maketitle': { info: t('Prints the title, author and date.') },
  '\\tableofcontents': { info: t('Prints the table of contents.') },
  '\\listoffigures': { info: t('Prints the list of figures.') },
  '\\listoftables': { info: t('Prints the list of tables.') },
  '\\appendix': { info: t('Makes the sections that follow appendices.') },
  '\\part': { detail: heading, info: t('Starts a part.') },
  '\\chapter': { detail: heading, info: t('Starts a chapter (book and report classes).') },
  '\\section': { detail: heading, info: t('Starts a section.') },
  '\\subsection': { detail: heading, info: t('Starts a subsection.') },
  '\\subsubsection': { detail: heading, info: t('Starts a subsubsection.') },
  '\\paragraph': { detail: heading, info: t('Starts a titled paragraph.') },
  '\\begin': { detail: arg(t('environment')), info: t('Opens an environment.') },
  '\\end': { detail: arg(t('environment')), info: t('Closes an environment.') },
  '\\input': { detail: file, info: t('Inserts the content of a file here.') },
  '\\include': { detail: file, info: t('Inserts the content of a file on a new page.') },

  // References and citations
  '\\label': { detail: arg(t('name')), info: t('Names this place, so a reference can point to it.') },
  '\\ref': { detail: label, info: t('Prints the number of a label.') },
  '\\eqref': { detail: label, info: t('Prints the number of an equation in parentheses (amsmath).') },
  '\\autoref': { detail: label, info: t('Prints the number of a label after its type, such as "Figure" (hyperref).') },
  '\\pageref': { detail: label, info: t('Prints the page of a label.') },
  '\\cref': { detail: label, info: t('Prints the number of a label after its type (cleveref).') },
  '\\Cref': { detail: label, info: t('Like \\cref, capitalised to start a sentence (cleveref).') },
  '\\nameref': { detail: label, info: t('Prints the title of a labelled section (hyperref).') },
  '\\cite': { detail: citation, info: t('Cites works from the bibliography.') },
  '\\citep': { detail: citation, info: t('Cites in parentheses (natbib).') },
  '\\citet': { detail: citation, info: t('Cites within the text, with the author as part of the sentence (natbib).') },
  '\\parencite': { detail: citation, info: t('Cites in parentheses (biblatex).') },
  '\\textcite': {
    detail: citation,
    info: t('Cites within the text, with the author as part of the sentence (biblatex).'),
  },
  '\\autocite': { detail: citation, info: t('Cites in the style set for the document (biblatex).') },
  '\\footcite': { detail: citation, info: t('Cites in a footnote (biblatex).') },
  '\\nocite': { detail: keys, info: t('Lists works in the bibliography without citing them.') },
  '\\bibliography': { detail: file, info: t('Prints the bibliography from a .bib file (BibTeX).') },
  '\\bibliographystyle': { detail: arg(t('style')), info: t('Sets the style of the bibliography (BibTeX).') },
  '\\addbibresource': { detail: file, info: t('Adds a .bib file, with its extension (biblatex).') },

  // Text
  '\\textbf': { detail: text, info: t('Bold text.') },
  '\\textit': { detail: text, info: t('Italic text.') },
  '\\texttt': { detail: text, info: t('Monospaced text.') },
  '\\textsc': { detail: text, info: t('Small capitals.') },
  '\\emph': { detail: text, info: t('Emphasised text, usually in italics.') },
  '\\underline': { detail: text, info: t('Underlined text.') },
  '\\footnote': { detail: text, info: t('Adds a footnote.') },
  '\\url': { detail: address, info: t('Prints a web address (url or hyperref).') },
  '\\href': { detail: address + text, info: t('Links text to a web address (hyperref).') },
  '\\item': { detail: opt(t('label')), info: t('An entry of a list.') },
  '\\centering': { info: t('Centres what follows, up to the end of the current group.') },
  '\\noindent': { info: t('Starts this paragraph without indentation.') },
  '\\newpage': { info: t('Starts a new page.') },
  '\\clearpage': { info: t('Starts a new page after printing the pending figures and tables.') },
  '\\bigskip': { info: t('Large vertical space.') },
  '\\medskip': { info: t('Medium vertical space.') },
  '\\smallskip': { info: t('Small vertical space.') },
  '\\hfill': { info: t('Horizontal space that fills the line.') },
  '\\vfill': { info: t('Vertical space that fills the page.') },

  // Figures and tables
  '\\includegraphics': { detail: options + file, info: t('Inserts an image (graphicx).') },
  '\\caption': { detail: text, info: t('Adds a caption to a figure or table.') },
  '\\hline': { info: t('Horizontal line in a table.') },
  '\\toprule': { info: t('Top line of a table (booktabs).') },
  '\\midrule': { info: t('Line between rows of a table (booktabs).') },
  '\\bottomrule': { info: t('Bottom line of a table (booktabs).') },

  // Math
  '\\frac': { detail: arg(t('numerator')) + arg(t('denominator')), info: t('A fraction.') },
  '\\sqrt': { detail: opt(t('degree')) + arg(t('expression')), info: t('A root, square by default.') },
  '\\mathbf': { detail: arg(t('symbols')), info: t('Bold letters in math.') },
  '\\mathrm': { detail: arg(t('symbols')), info: t('Upright letters in math.') },
  '\\mathcal': { detail: arg(t('symbols')), info: t('Calligraphic capitals in math.') },
  '\\mathbb': { detail: arg(t('symbols')), info: t('Blackboard bold capitals, such as ℝ (amssymb).') },
  '\\hat': { detail: arg(t('symbol')), info: t('A hat over a symbol.') },
  '\\bar': { detail: arg(t('symbol')), info: t('A bar over a symbol.') },
  '\\vec': { detail: arg(t('symbol')), info: t('An arrow over a symbol.') },

  // Symbols: the glyph says it all, in any language.
  '\\ldots': { detail: '…' },
  '\\cdots': { detail: '⋯' },
  '\\dots': { detail: '…' },
  '\\alpha': { detail: 'α' },
  '\\beta': { detail: 'β' },
  '\\gamma': { detail: 'γ' },
  '\\delta': { detail: 'δ' },
  '\\epsilon': { detail: 'ϵ' },
  '\\theta': { detail: 'θ' },
  '\\lambda': { detail: 'λ' },
  '\\mu': { detail: 'μ' },
  '\\pi': { detail: 'π' },
  '\\sigma': { detail: 'σ' },
  '\\phi': { detail: 'ϕ' },
  '\\omega': { detail: 'ω' },
  '\\infty': { detail: '∞' },
  '\\sum': { detail: '∑' },
  '\\prod': { detail: '∏' },
  '\\int': { detail: '∫' },
  '\\partial': { detail: '∂' },
  '\\nabla': { detail: '∇' },
  '\\times': { detail: '×' },
  '\\cdot': { detail: '⋅' },
  '\\leq': { detail: '≤' },
  '\\geq': { detail: '≥' },
  '\\neq': { detail: '≠' },
  '\\approx': { detail: '≈' },
  '\\rightarrow': { detail: '→' },
  '\\leftarrow': { detail: '←' },
  '\\Rightarrow': { detail: '⇒' },

  // Environments
  abstract: { info: t('The summary of the document.') },
  align: { info: t('Numbered equations aligned at & (amsmath).') },
  'align*': { info: t('Equations aligned at &, without numbers (amsmath).') },
  array: { detail: arg(t('columns')), info: t('A table inside math.') },
  center: { info: t('Centred content.') },
  description: { info: t('A list of terms: \\item[term] and its description.') },
  document: { info: t('The body of the document.') },
  enumerate: { info: t('A numbered list.') },
  equation: { info: t('A numbered equation.') },
  'equation*': { info: t('An equation without a number (amsmath).') },
  figure: { detail: opt(t('placement')), info: t('A floating figure, with its image and caption.') },
  'figure*': { detail: opt(t('placement')), info: t('A floating figure across both columns.') },
  flushleft: { info: t('Content aligned to the left.') },
  flushright: { info: t('Content aligned to the right.') },
  frame: { info: t('A slide (beamer).') },
  gather: { info: t('Numbered equations, each centred (amsmath).') },
  'gather*': { info: t('Centred equations without numbers (amsmath).') },
  itemize: { info: t('A bulleted list.') },
  lstlisting: { info: t('Source code (listings).') },
  minipage: { detail: arg(t('width')), info: t('A block of text with its own width.') },
  proof: { info: t('A proof (amsthm).') },
  quote: { info: t('A quotation set apart from the text.') },
  table: { detail: opt(t('placement')), info: t('A floating table, with its content and caption.') },
  'table*': { detail: opt(t('placement')), info: t('A floating table across both columns.') },
  tabular: { detail: arg(t('columns')), info: t('Rows and columns; use l, c or r for each column.') },
  theorem: { info: t('A theorem, once \\newtheorem has defined it.') },
  verbatim: { info: t('Text printed exactly as written.') },
}
