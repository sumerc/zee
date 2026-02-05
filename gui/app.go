//go:build gui

package gui

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"github.com/go-gl/glfw/v3.3/glfw"
)

//go:embed assets/tray.png
var trayIcon []byte

type App struct {
	fyneApp  fyne.App
	window   fyne.Window
	eye      *EyeWidget
	onReady  func()
	posX     int
	posY     int
	glfwInit bool
}

func NewApp(onReady func()) *App {
	return &App{onReady: onReady}
}

func Run(a *App) error {
	a.fyneApp = app.NewWithID("io.zee.gui")
	a.fyneApp.Settings().SetTheme(&darkTheme{})

	// Set up system tray using Fyne's built-in support
	if desk, ok := a.fyneApp.(desktop.App); ok {
		icon := fyne.NewStaticResource("tray.png", trayIcon)
		menu := fyne.NewMenu("zee",
			fyne.NewMenuItem("Quit", func() {
				a.fyneApp.Quit()
			}),
		)
		desk.SetSystemTrayMenu(menu)
		desk.SetSystemTrayIcon(icon)
	}

	// Get primary monitor work area for positioning
	var screenW, screenH int
	monitor := glfw.GetPrimaryMonitor()
	if monitor != nil {
		_, _, screenW, screenH = monitor.GetWorkarea()
	} else {
		screenW, screenH = 1920, 1080 // fallback
	}

	// Create frameless splash window on desktop
	if drv, ok := a.fyneApp.Driver().(desktop.Driver); ok {
		a.window = drv.CreateSplashWindow()
	} else {
		a.window = a.fyneApp.NewWindow("zee")
	}

	a.eye = NewEyeWidget()

	// Set eye as content directly - no padding
	a.window.SetContent(a.eye)
	a.window.SetFixedSize(true)
	a.window.SetPadded(false)

	// Size to exactly fit the eye
	eyeSize := a.eye.MinSize()
	a.window.Resize(eyeSize)

	// Calculate bottom-center position (with margin for dock)
	posX := (screenW - int(eyeSize.Width)) / 2
	posY := screenH - int(eyeSize.Height) - 20

	// Store position for use in Show()
	a.posX = posX
	a.posY = posY

	go a.onReady()

	// Run event loop without showing window (stays hidden until RecordingStart)
	a.fyneApp.Run()
	return nil
}

func (a *App) Quit() {
	if a.fyneApp != nil {
		a.fyneApp.Quit()
	}
}

func (a *App) Show() {
	fyne.Do(func() {
		if a.window == nil {
			return
		}

		// Configure GLFW attributes BEFORE showing
		if glfwWin := glfw.GetCurrentContext(); glfwWin != nil {
			// Position window before showing
			glfwWin.SetPos(a.posX, a.posY)

			// Set non-focus and floating attributes
			glfwWin.SetAttrib(glfw.FocusOnShow, glfw.False)
			glfwWin.SetAttrib(glfw.Floating, glfw.True)
		}

		// Show without taking focus - use GLFW directly
		if glfwWin := glfw.GetCurrentContext(); glfwWin != nil {
			glfwWin.Show()
		} else {
			a.window.Show()
		}
	})
}

func (a *App) Hide() {
	fyne.Do(func() {
		if a.window != nil {
			a.window.Hide()
		}
	})
}

// EventSink implementation - Set* methods use mutex, no fyne.Do needed
func (a *App) RecordingStart() {
	a.eye.SetRecording(true)
	a.Show()
}

func (a *App) RecordingStop() {
	a.eye.SetRecording(false)
	a.Hide()
}

func (a *App) RecordingTick(duration float64) {
	a.eye.SetDuration(duration)
}

func (a *App) AudioLevel(level float64) {
	a.eye.SetLevel(level)
}

func (a *App) NoVoiceWarning() {
	a.eye.SetNoVoice(true)
}

func (a *App) Transcription(text string, metrics []string, copied bool, noSpeech bool) {
	a.eye.SetNoVoice(false)
	a.Hide()
}

func (a *App) ModeLine(text string)   {}
func (a *App) DeviceLine(text string) {}
func (a *App) RateLimit(text string)  {}
