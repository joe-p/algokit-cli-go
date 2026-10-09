package cli

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

// MaxContentWidth matches the Python CLI's max_content_width context setting.
const MaxContentWidth = 120

// TerminalWidth returns the terminal width like Python's shutil.get_terminal_size; replaceable for testing.
var TerminalWidth = func() int {
	if columns, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && columns > 0 {
		return columns
	}
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width > 0 {
		return width
	}
	return 80
}

func helpWidth() int {
	return max(min(TerminalWidth(), MaxContentWidth)-2, 50)
}

// chunks splits text into words and whitespace runs like Python's textwrap (with break_on_hyphens), so that
// wrapping matches click's output.
func chunks(text string) []string {
	var result []string
	runes := []rune(text)
	isLetter := func(i int) bool {
		return i >= 0 && i < len(runes) && unicode.IsLetter(runes[i])
	}
	isWordChar := func(i int, extra string) bool {
		if i < 0 || i >= len(runes) {
			return false
		}
		r := runes[i]
		return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(extra, r)
	}
	start := 0
	for i := 0; i < len(runes); {
		if unicode.IsSpace(runes[i]) {
			j := i
			for j < len(runes) && unicode.IsSpace(runes[j]) {
				j++
			}
			result = append(result, string(runes[i:j]))
			i, start = j, j
			continue
		}
		// an em-dash ("--") between words is its own chunk
		if runes[i] == '-' && i > start && i+1 < len(runes) && runes[i+1] == '-' && isWordChar(i-1, `!"'&.,?`) {
			j := i
			for j < len(runes) && runes[j] == '-' {
				j++
			}
			if isWordChar(j, "") {
				result = append(result, string(runes[start:i]), string(runes[i:j]))
				i, start = j, j
				continue
			}
		}
		// split hyphenated words after the hyphen, e.g. "algokit-quick-start" -> "algokit-", "quick-", "start"
		if runes[i] == '-' && i > start &&
			((isLetter(i-1) && isLetter(i-2)) || (isLetter(i-1) && i-2 >= 0 && runes[i-2] == '-' && isLetter(i-3))) &&
			isLetter(i+1) && (isLetter(i+2) || (i+2 < len(runes) && runes[i+2] == '-' && isLetter(i+3))) {
			result = append(result, string(runes[start:i+1]))
			start = i + 1
			i++
			continue
		}
		i++
		if i == len(runes) || unicode.IsSpace(runes[i]) {
			result = append(result, string(runes[start:i]))
			start = i
		}
	}
	return result
}

func textLen(s string) int { return utf8.RuneCountInString(s) }

// wrap fills a paragraph like Python's textwrap.TextWrapper.wrap (with replace_whitespace=False).
func wrap(text string, width int, initialIndent, subsequentIndent string) []string {
	var lines []string
	pending := chunks(text)
	for len(pending) > 0 {
		indent := subsequentIndent
		if len(lines) == 0 {
			indent = initialIndent
		}
		available := width - textLen(indent)
		// drop leading whitespace on all but the first line
		if strings.TrimSpace(pending[0]) == "" && len(lines) > 0 {
			pending = pending[1:]
		}
		var line []string
		length := 0
		for len(pending) > 0 && length+textLen(pending[0]) <= available {
			length += textLen(pending[0])
			line = append(line, pending[0])
			pending = pending[1:]
		}
		if len(line) == 0 && len(pending) > 0 {
			// a single chunk that's too long for the line
			line = append(line, pending[0])
			pending = pending[1:]
		}
		// drop trailing whitespace
		if len(line) > 0 && strings.TrimSpace(line[len(line)-1]) == "" {
			line = line[:len(line)-1]
		}
		if len(line) > 0 {
			lines = append(lines, indent+strings.Join(line, ""))
		}
	}
	return lines
}

// wrapParagraphs wraps text made of paragraphs separated by blank lines (like click.formatting.wrap_text with
// preserve_paragraphs), returning the lines.
func wrapParagraphs(text string, width int, indent string) []string {
	var lines []string
	for i, paragraph := range strings.Split(text, "\n\n") {
		if i > 0 {
			lines = append(lines, "")
		}
		// a paragraph's lines are joined with spaces, keeping any other whitespace
		joined := strings.Join(strings.Split(strings.TrimLeft(paragraph, " "), "\n"), " ")
		lines = append(lines, wrap(joined, width, indent, indent)...)
	}
	return lines
}

type helpWriter struct {
	b     strings.Builder
	width int
}

func (w *helpWriter) write(parts ...string) {
	for _, part := range parts {
		w.b.WriteString(part)
	}
}

func (w *helpWriter) paragraph() {
	if w.b.Len() > 0 {
		w.b.WriteString("\n")
	}
}

// definitionList writes two column rows like click's HelpFormatter.write_dl.
func (w *helpWriter) definitionList(rows [][2]string) {
	const indent, colMax, colSpacing = 2, 30, 2
	firstWidth := 0
	for _, row := range rows {
		firstWidth = max(firstWidth, textLen(row[0]))
	}
	firstCol := min(firstWidth, colMax) + colSpacing
	for _, row := range rows {
		w.write(strings.Repeat(" ", indent), row[0])
		if row[1] == "" {
			w.b.WriteString("\n")
			continue
		}
		if textLen(row[0]) <= firstCol-colSpacing {
			w.b.WriteString(strings.Repeat(" ", firstCol-textLen(row[0])))
		} else {
			w.write("\n", strings.Repeat(" ", firstCol+indent))
		}
		textWidth := max(w.width-firstCol-2, 10)
		for i, line := range wrapParagraphs(row[1], textWidth, "") {
			if i > 0 {
				w.b.WriteString(strings.Repeat(" ", firstCol+indent))
			}
			w.write(line, "\n")
		}
	}
}

// joinOpts formats option names like click, short options first, e.g. "-n, --name".
func joinOpts(opts []string) string {
	sorted := slices.Clone(opts)
	slices.SortStableFunc(sorted, func(a, b string) int {
		return len(a) - len(strings.TrimLeft(a, "-")) - (len(b) - len(strings.TrimLeft(b, "-")))
	})
	return strings.Join(sorted, ", ")
}

func (o *Option) helpRecord() [2]string {
	names := joinOpts(o.Opts)
	if len(o.SecondaryOpts) > 0 {
		names += " / " + joinOpts(o.SecondaryOpts)
	}
	if o.takesValue() {
		metavar := o.Metavar
		if metavar == "" {
			metavar = "TEXT"
		}
		names += " " + metavar
	}
	help := o.Help
	if o.ShowDefault {
		if value, ok := o.Default.(string); ok {
			help += "  [default: " + value + "]"
		}
	}
	return [2]string{names, help}
}

// Help returns the command's help text, formatted like click.
func (ctx *Context) Help() string {
	cmd := ctx.Command
	w := &helpWriter{width: helpWidth()}
	w.write(ctx.Usage(), "\n")
	if cmd.Help != "" {
		w.paragraph()
		for _, line := range wrapParagraphs(cmd.Help, w.width, "  ") {
			w.write(line, "\n")
		}
	}

	var options [][2]string
	for _, option := range cmd.allOptions() {
		if !option.Hidden {
			options = append(options, option.helpRecord())
		}
	}
	w.paragraph()
	w.b.WriteString("Options:\n")
	w.definitionList(options)

	if cmd.isGroup() {
		commands := slices.Clone(cmd.Commands)
		slices.SortFunc(commands, func(a, b *Command) int { return strings.Compare(a.Name, b.Name) })
		var rows [][2]string
		for _, sub := range commands {
			rows = append(rows, [2]string{sub.Name, sub.ShortHelp})
		}
		w.paragraph()
		w.b.WriteString("Commands:\n")
		w.definitionList(rows)
	}
	return strings.TrimRight(w.b.String(), "\n")
}
