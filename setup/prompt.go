package setup

// huh-based prompts, sharing the wizard's key semantics: Esc backs out of the
// prompt (the caller treats it as "take the default / skip"), Ctrl+C
// interrupts the whole wizard with the "progress is saved" note. stepProviders
// runs on these; the remaining steps still use menu.go's hand-rolled readers
// until this prototype proves itself in the installer flow.

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"golang.org/x/term"
)

// runPrompt runs one field as a single-field form. Reports escaped=true when
// the user backed out with Esc (the bound value is left untouched). Ctrl+C
// never returns — it exits via exitInterrupted, after huh restored the
// terminal. Both keys surface as huh.ErrUserAborted, so a message filter
// records which one fired.
func runPrompt(field huh.Field) (escaped bool) {
	fmt.Println() // separate the prompt from preceding output (menu() did this)
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "skip"))

	ctrlC := false
	err := huh.NewForm(huh.NewGroup(field)).
		WithKeyMap(km).
		// ThemeBase is huh's leanest theme: monochrome, using only ANSI 0/7/8
		// (black/white/gray, which follow the terminal palette) with no accent
		// colors — the selected button is a plain inverse, not a colored fill.
		WithTheme(huh.ThemeFunc(huh.ThemeBase)).
		WithAccessible(!term.IsTerminal(int(os.Stdin.Fd()))).
		WithProgramOptions(tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
			if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "ctrl+c" {
				ctrlC = true
			}
			return msg
		})).
		Run()
	if err == nil {
		return false
	}
	if errors.Is(err, huh.ErrUserAborted) && !ctrlC {
		return true
	}
	if errors.Is(err, huh.ErrUserAborted) {
		exitInterrupted()
	}
	// Anything else (EOF on a pipe, tty gone): take the default rather than
	// looping on a dead input.
	fmt.Fprintf(os.Stderr, "prompt failed, keeping default: %v\n", err)
	return true
}

// selectIndex shows a Select over labels and returns the chosen index. def is
// pre-highlighted; Enter on it, Esc anywhere, or a dead tty all yield def.
func selectIndex(title string, labels []string, def int) int {
	opts := make([]huh.Option[int], len(labels))
	for i, l := range labels {
		opts[i] = huh.NewOption(l, i)
	}
	choice := def
	runPrompt(huh.NewSelect[int]().Title(title).Options(opts...).Value(&choice))
	return choice
}

// askYesNo asks a yes/no question, returning def on Enter or Esc. It uses huh's
// Confirm rather than a hand-rolled stdin read: after a huh Select/Input runs
// (bubbletea owns stdin during it), a raw os.Stdin read in the same process can
// race a lingering bubbletea reader that steals the keystroke — which sent the
// wizard back a step instead of advancing. Keeping every prompt on huh means
// one stdin owner throughout.
func askYesNo(prompt string, def bool) bool {
	v := def
	runPrompt(huh.NewConfirm().Title(prompt).Value(&v))
	return v
}

// secretInput reads a masked line. Enter submits (possibly empty), Esc backs
// out with ok=false. On a non-tty huh's accessible password prompt can't read
// (it requires a terminal for no-echo), so fall back to a visible line read —
// same behavior the hand-rolled readSecret had over pipes.
func secretInput(title, description string) (secret string, ok bool) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Printf("%s: ", title)
		return strings.TrimSpace(readLine()), true
	}
	var s string
	in := huh.NewInput().Title(title).EchoMode(huh.EchoModePassword).Value(&s)
	if description != "" {
		in = in.Description(description)
	}
	if runPrompt(in) {
		return "", false
	}
	return strings.TrimSpace(s), true
}
