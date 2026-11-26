package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
)

// Shells are the shells Completion supports.
var Shells = []string{"bash", "zsh", "fish", "powershell"}

// compData is the command table reduced to what completion needs. Paths are
// canonical command paths joined with spaces ("" is the root).
type compData struct {
	children    []compList  // per non-leaf path: its visible child names
	resolve     []compChild // path + typed name (name, alias or alias node) -> canonical path
	flags       []compList  // per leaf path: the spellings of its own flags
	globalFlags []string    // spellings of the global flags
	values      []compValue // flags that take a value; path "" = global
	args        []compList  // per leaf path: the values of its first positional argument
}

type compList struct {
	path  string
	words []string
}

type compChild struct{ path, name, to string }

type compValue struct {
	path, flag string
	values     []string // nil = any value (files are offered)
}

// spellings are the accepted spellings of a flag (see Parse).
func spellings(f Flag) []string {
	s := []string{dashName(f.Name)}
	if f.Short != "" {
		s = append(s, "-"+f.Short)
	}
	return s
}

func flagValues(path string, f Flag) []compValue {
	if f.Kind == KindBool {
		return nil
	}
	var vals []string
	if f.Kind == KindEnum {
		vals = f.Enum
	}
	var out []compValue
	for _, s := range spellings(f) {
		out = append(out, compValue{path: path, flag: s, values: vals})
	}
	return out
}

func buildCompData(root *Command) compData {
	var d compData
	for _, f := range GlobalFlags() {
		if f.Hidden {
			continue
		}
		d.globalFlags = append(d.globalFlags, spellings(f)...)
		d.values = append(d.values, flagValues("", f)...)
	}
	var walk func(c *Command, path []string)
	walk = func(c *Command, path []string) {
		p := strings.Join(path, " ")
		if c.IsLeaf() {
			var fl []string
			for _, f := range c.Flags {
				if f.Hidden {
					continue
				}
				fl = append(fl, spellings(f)...)
				d.values = append(d.values, flagValues(p, f)...)
			}
			if len(fl) > 0 {
				d.flags = append(d.flags, compList{p, fl})
			}
			if len(c.Args) > 0 && len(c.Args[0].Values) > 0 {
				d.args = append(d.args, compList{p, c.Args[0].Values})
			}
			return
		}
		var names []string
		for _, s := range c.Sub {
			if s.Hidden {
				continue
			}
			names = append(names, s.Name)
			if s.AliasOf != "" {
				d.resolve = append(d.resolve, compChild{p, s.Name, s.AliasOf})
				continue
			}
			sp := append(slices.Clip(path), s.Name)
			for _, n := range append([]string{s.Name}, s.Aliases...) {
				d.resolve = append(d.resolve, compChild{p, n, strings.Join(sp, " ")})
			}
			walk(s, sp)
		}
		d.children = append(d.children, compList{p, names})
	}
	walk(root, nil)
	return d
}

// Completion returns a static completion script for shell, generated from the
// command table: command names, flag names, enum flag values and the listed values
// of a first positional argument. Anything else falls back to file names.
func Completion(root *Command, shell string) (string, error) {
	d := buildCompData(root)
	switch shell {
	case "bash":
		return posixFuncs(d, "bash") + bashMain, nil
	case "zsh":
		return "#compdef mdr\n" + posixFuncs(d, "zsh") + zshMain, nil
	case "fish":
		return fishScript(d), nil
	case "powershell":
		return powershellScript(d), nil
	}
	return "", apperr.New(apperr.Usage, "unknown shell %q", shell).WithHint("use one of: %s", strings.Join(Shells, ", "))
}

// posixFuncs writes the lookup functions shared by bash and zsh. Each prints the
// candidates separated by spaces; _mdr_values fails for a flag without a value.
func posixFuncs(d compData, shell string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# mdr completion for %s, generated from the command table by \"mdr completion %s\".\n", shell, shell)
	b.WriteString("\n_mdr_children() {\n  case \"$1\" in\n")
	for _, c := range d.children {
		fmt.Fprintf(&b, "    %q) echo %q ;;\n", c.path, strings.Join(c.words, " "))
	}
	b.WriteString("  esac\n}\n\n_mdr_child() {\n  case \"$1/$2\" in\n")
	for _, r := range d.resolve {
		fmt.Fprintf(&b, "    %q) echo %q ;;\n", r.path+"/"+r.name, r.to)
	}
	b.WriteString("  esac\n}\n\n_mdr_flags() {\n  case \"$1\" in\n")
	for _, f := range d.flags {
		fmt.Fprintf(&b, "    %q) echo %q ;;\n", f.path, strings.Join(f.words, " "))
	}
	fmt.Fprintf(&b, "  esac\n  echo %q\n}\n\n_mdr_values() {\n  case \"$1/$2\" in\n", strings.Join(d.globalFlags, " "))
	for _, v := range d.values {
		pat := fmt.Sprintf("%q", v.path+"/"+v.flag)
		if v.path == "" {
			pat = fmt.Sprintf("*/%q", v.flag)
		}
		fmt.Fprintf(&b, "    %s) echo %q ;;\n", pat, strings.Join(v.values, " "))
	}
	b.WriteString("    *) return 1 ;;\n  esac\n}\n\n_mdr_args() {\n  case \"$1\" in\n")
	for _, a := range d.args {
		fmt.Fprintf(&b, "    %q) echo %q ;;\n", a.path, strings.Join(a.words, " "))
	}
	b.WriteString("  esac\n}\n")
	return b.String()
}

// posixPath is the shell code shared by bash and zsh that uses the lookup functions.
const posixPath = `
# _mdr_path <words before the cursor, without mdr>: the command path. Flags (and
# the values of flags that take one) are skipped, command names descend.
_mdr_path() {
  local cmdpath="" w next skip=0
  for w in "$@"; do
    if [ "$skip" = 1 ]; then skip=0; continue; fi
    case "$w" in
      --) break ;;
      -*=*) ;;
      -*) if _mdr_values "$cmdpath" "$w" >/dev/null; then skip=1; fi ;;
      *) next="$(_mdr_child "$cmdpath" "$w")"; if [ -n "$next" ]; then cmdpath="$next"; fi ;;
    esac
  done
  echo "$cmdpath"
}

# _mdr_candidates <path> <previous word> <current word>: the words to offer, or
# nothing (exit 1) for file names.
_mdr_candidates() {
  local vals
  case "$2" in
    -*=*) ;;
    -*) if vals="$(_mdr_values "$1" "$2")"; then
          [ -n "$vals" ] && echo "$vals" && return 0
          return 1
        fi ;;
  esac
  case "$3" in
    -*) _mdr_flags "$1"; return 0 ;;
  esac
  vals="$(_mdr_children "$1")"
  [ -z "$vals" ] && vals="$(_mdr_args "$1")"
  [ -n "$vals" ] && echo "$vals" && return 0
  return 1
}
`

const bashMain = posixPath + `
_mdr() {
  local cur="${COMP_WORDS[COMP_CWORD]}" prev="" cmdpath vals
  [ "$COMP_CWORD" -gt 1 ] && prev="${COMP_WORDS[COMP_CWORD-1]}"
  cmdpath="$(_mdr_path "${COMP_WORDS[@]:1:COMP_CWORD-1}")"
  if vals="$(_mdr_candidates "$cmdpath" "$prev" "$cur")"; then
    COMPREPLY=($(compgen -W "$vals" -- "$cur"))
  else
    COMPREPLY=($(compgen -f -- "$cur"))
  fi
}
complete -o default -F _mdr mdr
`

const zshMain = posixPath + `
_mdr() {
  local cur="${words[CURRENT]}" prev="" cmdpath vals
  (( CURRENT > 2 )) && prev="${words[CURRENT-1]}"
  cmdpath="$(_mdr_path "${(@)words[2,CURRENT-1]}")"
  if vals="$(_mdr_candidates "$cmdpath" "$prev" "$cur")"; then
    compadd -- ${=vals}
  else
    _files
  fi
}

if [ "$funcstack[1]" = "_mdr" ]; then
  _mdr "$@"
else
  compdef _mdr mdr
fi
`

func fishScript(d compData) string {
	var b strings.Builder
	b.WriteString("# mdr completion for fish, generated from the command table by \"mdr completion fish\".\n")
	b.WriteString("\nfunction __mdr_children\n    switch \"$argv[1]\"\n")
	for _, c := range d.children {
		fmt.Fprintf(&b, "        case %s\n            echo %s\n", fishQuote(c.path), fishQuote(strings.Join(c.words, " ")))
	}
	b.WriteString("    end\nend\n\nfunction __mdr_child\n    switch \"$argv[1]/$argv[2]\"\n")
	for _, r := range d.resolve {
		fmt.Fprintf(&b, "        case %s\n            echo %s\n", fishQuote(r.path+"/"+r.name), fishQuote(r.to))
	}
	b.WriteString("    end\nend\n\nfunction __mdr_flags\n    switch \"$argv[1]\"\n")
	for _, f := range d.flags {
		fmt.Fprintf(&b, "        case %s\n            echo %s\n", fishQuote(f.path), fishQuote(strings.Join(f.words, " ")))
	}
	fmt.Fprintf(&b, "    end\n    echo %s\nend\n\nfunction __mdr_values\n    switch \"$argv[1]/$argv[2]\"\n", fishQuote(strings.Join(d.globalFlags, " ")))
	for _, v := range d.values {
		pat := fishQuote(v.path + "/" + v.flag)
		if v.path == "" {
			pat = "'*/" + v.flag + "'"
		}
		fmt.Fprintf(&b, "        case %s\n            echo %s\n", pat, fishQuote(strings.Join(v.values, " ")))
	}
	b.WriteString("        case '*'\n            return 1\n    end\nend\n\nfunction __mdr_args\n    switch \"$argv[1]\"\n")
	for _, a := range d.args {
		fmt.Fprintf(&b, "        case %s\n            echo %s\n", fishQuote(a.path), fishQuote(strings.Join(a.words, " ")))
	}
	b.WriteString("    end\nend\n" + fishMain)
	return b.String()
}

// fishQuote single-quotes s for fish (the table has no quotes or backslashes).
func fishQuote(s string) string { return "'" + s + "'" }

const fishMain = `
function __mdr_complete
    set -l tokens (commandline -opc)
    set -l cur (commandline -ct)
    set -l cmdpath ''
    set -l skip 0
    for w in $tokens[2..-1]
        if test $skip = 1
            set skip 0
            continue
        end
        switch $w
            case '--'
                break
            case '-*=*'
            case '-*'
                if __mdr_values "$cmdpath" $w >/dev/null
                    set skip 1
                end
            case '*'
                set -l next (__mdr_child "$cmdpath" $w)
                if test -n "$next"
                    set cmdpath $next
                end
        end
    end
    set -l prev ''
    if test (count $tokens) -gt 1
        set prev $tokens[-1]
    end
    if string match -q -- '-*' $prev; and not string match -q -- '*=*' $prev
        set -l vals (__mdr_values "$cmdpath" $prev)
        if test $status -eq 0
            if test -n "$vals"
                string split ' ' -- $vals
            else
                __fish_complete_path $cur
            end
            return
        end
    end
    if string match -q -- '-*' $cur
        string split ' ' -- (__mdr_flags "$cmdpath")
        return
    end
    set -l vals (__mdr_children "$cmdpath")
    if test -z "$vals"
        set vals (__mdr_args "$cmdpath")
    end
    if test -n "$vals"
        string split ' ' -- $vals
    else
        __fish_complete_path $cur
    end
end

complete -c mdr -f -a '(__mdr_complete)'
`

func powershellScript(d compData) string {
	var b strings.Builder
	b.WriteString("# mdr completion for PowerShell, generated from the command table by \"mdr completion powershell\".\n")
	b.WriteString("Register-ArgumentCompleter -Native -CommandName mdr -ScriptBlock {\n")
	b.WriteString("    param($wordToComplete, $commandAst, $cursorPosition)\n")
	b.WriteString("    # Ordinal (case-sensitive) keys: -H and -h are different flags.\n")
	dict := func(name string) {
		fmt.Fprintf(&b, "    $%s = New-Object 'System.Collections.Generic.Dictionary[string,string]'\n", name)
	}
	dict("children")
	for _, c := range d.children {
		fmt.Fprintf(&b, "    $children[%s] = %s\n", psQuote(c.path), psQuote(strings.Join(c.words, " ")))
	}
	dict("child")
	for _, r := range d.resolve {
		fmt.Fprintf(&b, "    $child[%s] = %s\n", psQuote(r.path+"/"+r.name), psQuote(r.to))
	}
	dict("flags")
	for _, f := range d.flags {
		fmt.Fprintf(&b, "    $flags[%s] = %s\n", psQuote(f.path), psQuote(strings.Join(f.words, " ")))
	}
	fmt.Fprintf(&b, "    $globalFlags = %s\n", psQuote(strings.Join(d.globalFlags, " ")))
	dict("values")
	for _, v := range d.values {
		fmt.Fprintf(&b, "    $values[%s] = %s\n", psQuote(v.path+"/"+v.flag), psQuote(strings.Join(v.values, " ")))
	}
	dict("argValues")
	for _, a := range d.args {
		fmt.Fprintf(&b, "    $argValues[%s] = %s\n", psQuote(a.path), psQuote(strings.Join(a.words, " ")))
	}
	b.WriteString(powershellMain)
	return b.String()
}

// psQuote single-quotes s for PowerShell (the table has no quotes).
func psQuote(s string) string { return "'" + s + "'" }

const powershellMain = `
    # The values of a flag: $null if it takes none, '' if it takes any value.
    $flagValues = {
        param($path, $flag)
        if ($values.ContainsKey("$path/$flag")) { return $values["$path/$flag"] }
        if ($values.ContainsKey("/$flag")) { return $values["/$flag"] }
        return $null
    }
    $words = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.Extent.Text })
    if ($wordToComplete -ne '' -and $words.Count -gt 0) {
        $words = @($words | Select-Object -SkipLast 1)
    }
    $path = ''
    $skip = $false
    foreach ($w in $words) {
        if ($skip) { $skip = $false; continue }
        if ($w -eq '--') { break }
        if ($w -like '-*=*') { continue }
        if ($w -like '-*') {
            if ($null -ne (& $flagValues $path $w)) { $skip = $true }
            continue
        }
        if ($child.ContainsKey("$path/$w")) { $path = $child["$path/$w"] }
    }
    $candidates = $null
    $prev = if ($words.Count -gt 0) { $words[-1] } else { '' }
    if ($prev -like '-*' -and $prev -notlike '-*=*') {
        $v = & $flagValues $path $prev
        if ($null -ne $v) {
            if ($v -eq '') { return }
            $candidates = $v
        }
    }
    if ($null -eq $candidates) {
        if ($wordToComplete -like '-*') {
            $candidates = $globalFlags
            if ($flags.ContainsKey($path)) { $candidates = $flags[$path] + ' ' + $globalFlags }
        } elseif ($children.ContainsKey($path)) {
            $candidates = $children[$path]
        } elseif ($argValues.ContainsKey($path)) {
            $candidates = $argValues[$path]
        } else {
            return
        }
    }
    $candidates -split ' ' | Where-Object { $_ -clike "$wordToComplete*" } | ForEach-Object {
        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
    }
}
`
