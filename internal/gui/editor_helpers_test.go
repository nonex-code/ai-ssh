package gui

import "github.com/nonex-code/ai-ssh/internal/config"

// loadDraftForTest fills the draft from a parsed catalogue without opening a
// window, so the form can be rendered headless.
func (a *App) loadDraftForTest(cfg *config.Config, alias string) {
	a.editAlias = alias
	if h, ok := cfg.Hosts[alias]; ok && h != nil {
		a.draft = draftOf(alias, h)
	}
}
