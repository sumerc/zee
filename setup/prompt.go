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
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "skip"))

	ctrlC := false
	err := huh.NewForm(huh.NewGroup(field)).
		WithKeyMap(km).
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
