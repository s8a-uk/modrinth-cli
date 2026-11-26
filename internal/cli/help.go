package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/s8a-uk/modrinth-cli/internal/output"
)

// HelpWidth is the column at which help text wraps.
const HelpWidth = 100

// HelpText returns the human-readable help for cmd, found at path under root.
func HelpText(root, cmd *Command, path []string) string {
	var b strings.Builder
	line := commandLine(path)
	b.WriteString(cmd.Summary + "\n")
	if cmd == root {
		b.WriteString("Agents: run \"mdr schema\" for a machine-readable command list.\n")
	}
	if cmd.Long != "" {
		b.WriteString("\n")
		writeWrapped(&b, cmd.Long, "")
	}

	if !cmd.IsLeaf() {
		fmt.Fprintf(&b, "\nUsage:\n  %s <command> [flags]\n", line)
		var rows [][2]string
		for _, c := range cmd.Sub {
			if !c.Hidden && c.AliasOf == "" {
				rows = append(rows, [2]string{c.Name, c.Summary})
			}
		}
		b.WriteString("\nCommands:\n")
		writeRows(&b, rows)
		if cmd == root {
			b.WriteString("\nGlobal flags:\n")
			writeRows(&b, flagRows(GlobalFlags()))
		}
		fmt.Fprintf(&b, "\nRun \"%s <command> --help\" for details.\n", line)
		return b.String()
	}

	fmt.Fprintf(&b, "\nUsage:\n  %s%s [flags]\n", line, usageArgs(cmd.Args))
	if cmd.Mutation != ReadOnly {
		if s := firstScope(cmd); s != "" {
			fmt.Fprintf(&b, "Requires scope: %s. ", s)
		}
		b.WriteString("Supports --dry-run.\n")
		if cmd.Mutation == Destructive {
			b.WriteString("Requires --yes when stdin is not a terminal.\n")
		}
	}
	if len(cmd.Args) > 0 {
		var rows [][2]string
		for _, a := range cmd.Args {
			rows = append(rows, [2]string{"<" + a.Name + ">", a.Help})
		}
		b.WriteString("\nArguments:\n")
		writeRows(&b, rows)
	}
	if rows := flagRows(cmd.Flags); len(rows) > 0 {
		b.WriteString("\nFlags:\n")
		writeRows(&b, rows)
	}
	// The global flags are listed only by the root help.
	b.WriteString("\nGlobal flags (-o, --fields, --full, --dry-run, ...): see mdr --help\n")
	o := cmd.Output
	selectable := o != nil && o.Shape != output.Raw && o.Shape != output.Ack
	if selectable && len(o.Fields) > 0 {
		if o.DefaultFields != nil {
			fmt.Fprintf(&b, "\nOutput fields (default; all: mdr schema %s):\n", strings.Join(path, " "))
			writeWrapped(&b, strings.Join(o.DefaultFields, ", "), "  ")
		} else {
			b.WriteString("\nOutput fields:\n")
			writeWrapped(&b, strings.Join(o.Fields, ", "), "  ")
		}
	}
	if len(cmd.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, e := range cmd.Examples {
			b.WriteString("  " + e + "\n")
		}
	}
	if len(cmd.Endpoints) > 0 {
		b.WriteString("\n")
		for _, e := range cmd.Endpoints {
			fmt.Fprintf(&b, "API: %s %s\n", e.Method, e.Path)
		}
	}
	return b.String()
}

func usageArgs(args []Arg) string {
	var b strings.Builder
	for _, a := range args {
		s := "<" + a.Name + ">"
		if a.Variadic {
			s += "..."
		}
		if !a.Required {
			s = "[" + s + "]"
		}
		b.WriteString(" " + s)
	}
	return b.String()
}

func firstScope(c *Command) string {
	for _, e := range c.Endpoints {
		if e.Scope != "" {
			return e.Scope
		}
	}
	return ""
}

// flagRows formats flags for help.
func flagRows(flags []Flag) [][2]string {
	var rows [][2]string
	for _, f := range flags {
		if f.Hidden {
			continue
		}
		left := "    "
		switch {
		case f.Short != "":
			left = "-" + f.Short + ", "
		case len(f.Name) <= 2:
			left = "" // "-vv" lines up with the short flags
		}
		left += dashName(f.Name)
		if p := placeholder(f); p != "" {
			left += " " + p
		}
		help := f.Help
		if f.Default != "" && f.Kind != KindBool {
			help += " (default " + f.Default + ")"
		}
		rows = append(rows, [2]string{left, help})
	}
	return rows
}

func placeholder(f Flag) string {
	if f.Value != "" {
		return f.Value
	}
	switch f.Kind {
	case KindEnum:
		return "<" + strings.Join(f.Enum, "|") + ">"
	case KindInt:
		return "<n>"
	case KindString, KindStrings:
		return "<value>"
	}
	return ""
}

// writeRows writes two aligned columns, indented by 2, wrapping the second column.
func writeRows(b *strings.Builder, rows [][2]string) {
	width := 0
	for _, r := range rows {
		width = max(width, utf8.RuneCountInString(r[0]))
	}
	col := 2 + width + 3
	pad := strings.Repeat(" ", col)
	for _, r := range rows {
		lines := wrap(r[1], HelpWidth-col)
		if len(lines) == 0 {
			lines = []string{""}
		}
		left := "  " + r[0] + strings.Repeat(" ", width-utf8.RuneCountInString(r[0])+3)
		b.WriteString(strings.TrimRight(left+lines[0], " ") + "\n")
		for _, l := range lines[1:] {
			b.WriteString(pad + l + "\n")
		}
	}
}

func writeWrapped(b *strings.Builder, text, indent string) {
	for _, para := range strings.Split(text, "\n") {
		lines := wrap(para, HelpWidth-len(indent))
		if len(lines) == 0 {
			b.WriteString("\n")
		}
		for _, l := range lines {
			b.WriteString(indent + l + "\n")
		}
	}
}

// wrap splits text into lines of at most width characters, breaking at spaces.
// A single word longer than width gets a line of its own.
func wrap(text string, width int) []string {
	width = max(width, 20)
	var lines []string
	cur := ""
	for _, w := range strings.Fields(text) {
		switch {
		case cur == "":
			cur = w
		case utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
