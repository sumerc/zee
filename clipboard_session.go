package main

import (
	"sync"
	"time"

	"zee/clipboard"
)

type clipboardSession struct {
	mu            sync.Mutex
	restoreMu     sync.Mutex
	restoreCancel func()

	textMu   sync.Mutex
	lastText string
}

var clip clipboardSession

func (c *clipboardSession) PasteText(text string) {
	c.mu.Lock()
	clipboard.Copy(text)
	clipboard.Paste()
	c.mu.Unlock()
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
