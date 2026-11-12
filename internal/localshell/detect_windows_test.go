//go:build windows

package localshell

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16LE(s string) []byte {
	raw := []byte{0xff, 0xfe}
	for _, u := range utf16.Encode([]rune(s)) {
		raw = append(raw, byte(u), byte(u>>8))
	}
	return raw
}

func TestParseWSLListUTF8(t *testing.T) {
	got := parseWSLList([]byte("\ufeffUbuntu\r\n\r\nDebian\r\n"))
	if len(got) != 2 || got[0] != "Ubuntu" || got[1] != "Debian" {
		t.Fatalf("got %q", got)
	}
}

func TestParseWSLListUTF16(t *testing.T) {
	raw := append(utf16LE("Ubuntu\r\nDebian-12\r\n"), 0, 0, 0, 0)
	got := parseWSLList(raw)
	if len(got) != 2 || got[0] != "Ubuntu" || got[1] != "Debian-12" {
		t.Fatalf("got %q", got)
	}
}

func TestParseWSLListWithoutBOM(t *testing.T) {
	got := parseWSLList(utf16LE("Ubuntu\r\n")[2:])
	if len(got) != 1 || got[0] != "Ubuntu" {
		t.Fatalf("got %q", got)
	}
}

func TestDecodeWSLKeepsUTF8(t *testing.T) {
	in := "Ubuntu 中文\r\n"
	if got := decodeWSL([]byte(in)); got != in {
		t.Fatalf("decodeWSL = %q, want %q", got, in)
	}
}

func TestDetectAlwaysOffersDefault(t *testing.T) {
	got := Detect()
	if len(got) == 0 || got[0].Key != "default" || got[0].Command != nil {
		t.Fatalf("first entry = %+v", got)
	}
	for _, sh := range got {
		if sh.Label == "" || sh.Key == "" {
			t.Fatalf("incomplete entry %+v", sh)
		}
		if sh.Command != nil && len(sh.Command) == 0 {
			t.Fatalf("%s has an empty command", sh.Key)
		}
	}
	// A duplicate key would draw two menu items that act as one.
	seen := map[string]bool{}
	for _, sh := range got {
		if seen[strings.ToLower(sh.Key)] {
			t.Fatalf("duplicate key %q in %+v", sh.Key, got)
		}
		seen[strings.ToLower(sh.Key)] = true
	}
}
