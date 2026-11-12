package gui

import (
	"os"
	"strings"
	"sync"

	"github.com/egoist/mygo"
)

// language is the interface language stored in gui.json: system, zh or en.
// tr translates a Chinese source string for the current language; the source
// string stays readable in the code and is also the fallback.
var (
	langMu sync.RWMutex
	lang   = "zh"
)

func setLanguage(pref string) {
	langMu.Lock()
	lang = resolveLanguage(pref)
	langMu.Unlock()
}

func currentLanguage() string {
	langMu.RLock()
	defer langMu.RUnlock()
	return lang
}

// resolveLanguage maps a setting to zh/en. "system" follows mygo's locale,
// then LANG/LC_ALL.
func resolveLanguage(pref string) string {
	switch pref {
	case "zh", "en":
		return pref
	}
	tag := strings.ToLower(mygo.App.Locale())
	if tag == "" {
		tag = strings.ToLower(os.Getenv("LANG") + " " + os.Getenv("LC_ALL") + " " + os.Getenv("LANGUAGE"))
	}
	if strings.HasPrefix(tag, "en") {
		return "en"
	}
	return "zh"
}

// tr returns s in English when that is the current language, else s itself.
func tr(s string) string {
	if currentLanguage() != "en" {
		return s
	}
	if v, ok := enCatalog[s]; ok {
		return v
	}
	return s
}

// trAll translates a slice of source strings, for menu and preset labels.
func trAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = tr(s)
	}
	return out
}
