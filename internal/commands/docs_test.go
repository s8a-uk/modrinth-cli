package commands_test

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/commands"
	"github.com/s8a-uk/modrinth-cli/internal/output"
)

// docExample is one mdr command line found in a code block of a document.
type docExample struct {
	file string
	line int
	args []string // without the leading "mdr"
}

// TestSkillExamplesParse checks that every mdr command line in the code blocks of
// SKILL.md and README.md parses against the command table: known commands and
// flags, valid flag and argument values, and valid --fields. Nothing is run and no
// request is sent.
func TestSkillExamplesParse(t *testing.T) {
	var docs []string
	for _, name := range []string{"skills/modrinth-cli/SKILL.md", "README.md"} {
		abs, err := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, abs)
	}
	// Some validators look at the file system (--dir must exist): parse in a
	// directory that has what the examples refer to.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, doc := range docs {
		examples := docExamples(t, doc)
		if len(examples) < 5 {
			t.Fatalf("%s: found only %d mdr examples; did the code block layout change?", doc, len(examples))
		}
		for _, ex := range examples {
			name := filepath.Base(ex.file) + ":" + strconv.Itoa(ex.line)
			t.Run(name, func(t *testing.T) {
				p, err := cli.Parse(commands.Root(), ex.args)
				if err != nil {
					t.Fatalf("mdr %s: %v", strings.Join(ex.args, " "), err)
				}
				if p.Globals.Help || p.Globals.Version {
					return
				}
				if !p.Cmd.IsLeaf() {
					t.Fatalf("mdr %s: %q is not a runnable command", strings.Join(ex.args, " "), strings.Join(p.Path, " "))
				}
				if err := output.CheckFields(p.Cmd.Output, p.Globals.Fields, p.Globals.Full, strings.Join(p.Path, " "), !p.Cmd.Output.AnyFields); err != nil {
					t.Fatalf("mdr %s: %v", strings.Join(ex.args, " "), err)
				}
			})
		}
	}
}

// docExamples returns the mdr command lines in the fenced code blocks of name. A
// trailing backslash continues a line, "# ..." starts a comment, and every segment
// of a pipeline that starts with mdr is an example. Redirections end a command.
func docExamples(t *testing.T, name string) []docExample {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []docExample
	inCode := false
	pending, start := "", 0
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			continue
		}
		if !inCode {
			continue
		}
		if pending == "" {
			start = n
		}
		if s, ok := strings.CutSuffix(strings.TrimRight(line, " "), `\`); ok {
			pending += s + " "
			continue
		}
		line, pending = pending+line, ""
		words, err := shellWords(strings.TrimPrefix(strings.TrimSpace(line), "$ "))
		if err != nil {
			t.Fatalf("%s:%d: %v", name, start, err)
		}
		for _, cmd := range words {
			if len(cmd) > 0 && cmd[0] == "mdr" {
				out = append(out, docExample{file: name, line: start, args: cmd[1:]})
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// shellWords splits a POSIX-like shell line into the words of each command of a
// pipeline or list. It handles single and double quotes and backslashes; an
// unquoted # starts a comment, and an unquoted <, > or 2> ends the command (the
// redirection target is not an argument).
func shellWords(s string) ([][]string, error) {
	var cmds [][]string
	var cur []string
	var word strings.Builder
	inWord, redirect := false, false
	flush := func() {
		if inWord && !redirect {
			cur = append(cur, word.String())
		}
		word.Reset()
		inWord = false
	}
	endCmd := func() {
		flush()
		if len(cur) > 0 {
			cmds = append(cmds, cur)
		}
		cur, redirect = nil, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, errUnterminated
			}
			word.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				word.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errUnterminated
			}
			inWord = true
		case c == '\\' && i+1 < len(s):
			i++
			word.WriteByte(s[i])
			inWord = true
		case c == ' ' || c == '\t':
			flush()
		case c == '#' && !inWord:
			endCmd()
			return cmds, nil
		case c == '|' || c == ';' || c == '&':
			endCmd()
		case c == '<' || c == '>':
			if word.String() == "2" {
				word.Reset()
				inWord = false
			}
			flush()
			redirect = true
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	endCmd()
	return cmds, nil
}

var errUnterminated = errors.New("unterminated quote")
