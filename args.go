package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// flags is a deliberately tiny arg parser: it accepts the same flag anywhere on
// the line (before or after positionals), which the stdlib flag package does
// not, and it is forgiving enough for LLM-generated command lines.
type flags struct {
	spec   map[string]bool // name -> takes a value
	values map[string]string
	seen   map[string]bool
	pos    []string
}

func parseFlags(args []string, spec map[string]bool) (*flags, error) {
	f := &flags{spec: spec, values: map[string]string{}, seen: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			f.pos = append(f.pos, args[i+1:]...)
			return f, nil
		}
		if !isFlag(a) {
			f.pos = append(f.pos, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			if _, ok := spec[name[:eq]]; !ok {
				return nil, fmt.Errorf("unknown flag %q", a)
			}
			f.values[name[:eq]] = name[eq+1:]
			f.seen[name[:eq]] = true
			continue
		}
		takesValue, ok := spec[name]
		if !ok {
			return nil, fmt.Errorf("unknown flag %q", a)
		}
		f.seen[name] = true
		if takesValue {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag %q needs a value", a)
			}
			i++
			f.values[name] = args[i]
		}
	}
	return f, nil
}

func isFlag(a string) bool {
	if len(a) < 2 || a[0] != '-' {
		return false
	}
	if a[1] == '-' { // long form: --name, --name=value
		return len(a) > 2
	}
	return isLetter(a[1])
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func (f *flags) str(name, def string) string {
	if v, ok := f.values[name]; ok {
		return v
	}
	return def
}

func (f *flags) bool(name string) bool {
	if !f.seen[name] {
		return false
	}
	if v, ok := f.values[name]; ok {
		b, err := strconv.ParseBool(v)
		return err == nil && b
	}
	return true
}

func (f *flags) int(name string, def int) int {
	n, err := strconv.Atoi(f.str(name, ""))
	if err != nil {
		return def
	}
	return n
}

func (f *flags) int64(name string, def int64) int64 {
	n, err := strconv.ParseInt(f.str(name, ""), 10, 64)
	if err != nil {
		return def
	}
	return n
}

// fatalf is for operator-facing commands (daemon, init) where a human is
// watching; agent-facing commands always emit JSON instead.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ai-ssh: "+format+"\n", args...)
	os.Exit(1)
}
