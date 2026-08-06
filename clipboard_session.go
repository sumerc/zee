package main

import (
	"sync"
	"time"

	"zee/clipboard"
	"zee/log"
)

type clipboardSession struct {
	mu            sync.Mutex
	restoreMu     sync.Mutex
	restoreCancel func()

	textMu   sync.Mutex
	lastText string
}

var clip clipboardSession

func (c *clipboardSession) PasteText(text string) (copyMs, keyMs float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Log failures — a broken paste (revoked Accessibility, pbcopy error) is
	// otherwise indistinguishable from "no text" for the user.
	t := time.Now()
	if err := clipboard.Copy(text); err != nil {
		log.Warnf("paste: clipboard copy failed: %v", err)
		return
	}
	copyMs = float64(time.Since(t).Microseconds()) / 1000
	t = time.Now()
	if err := clipboard.Paste(); err != nil {
		log.Warnf("paste: keystroke failed (Accessibility?): %v", err)
	}
	keyMs = float64(time.Since(t).Microseconds()) / 1000
	return copyMs, keyMs
}

func (c *clipboardSession) SaveCurrent() string {
	prev, _ := clipboard.Read()
	return prev
}

func (c *clipboardSession) CancelRestore() {
	c.restoreMu.Lock()
	if c.restoreCancel != nil {
		c.restoreCancel()
		c.restoreCancel = nil
	}
	c.restoreMu.Unlock()
}

func (c *clipboardSession) ScheduleRestore(prev string) {
	if prev == "" {
		return
	}
	cancelled := make(chan struct{})
	c.restoreMu.Lock()
	c.restoreCancel = func() { close(cancelled) }
	c.restoreMu.Unlock()

	go func() {
		select {
		case <-time.After(600 * time.Millisecond):
			c.mu.Lock()
			clipboard.Copy(prev)
			c.mu.Unlock()
		case <-cancelled:
		}
	}()
}

func (c *clipboardSession) CopyLast() {
	c.textMu.Lock()
	text := c.lastText
	c.textMu.Unlock()
	if text != "" {
		clipboard.Copy(text)
	}
}

func (c *clipboardSession) SetLastText(text string) {
	c.textMu.Lock()
	c.lastText = text
	c.textMu.Unlock()
}
