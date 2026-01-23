package main

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TUI message types
type RecordingStartMsg struct{}
type RecordingStopMsg struct{}
type RecordingTickMsg struct{ Duration float64 }
type AudioLevelMsg struct{ Level float64 }
type LogMsg struct{ Text string }
type TranscriptionMsg struct {
	Text     string
	Metrics  []string
	Copied   bool
	NoSpeech bool // true when no speech was detected
}
type ModeLineMsg struct{ Text string }    // Mode/provider info
type DeviceLineMsg struct{ Text string }  // Microphone device name
type RateLimitMsg struct{ Text string }   // Rate limit info
type tickMsg time.Time

type tuiState int

const (
	tuiStateIdle tuiState = iota
	tuiStateRecording
)

type tuiModel struct {
	state             tuiState
	frame             int
	recordingDuration float64
	audioLevel        float64
	peakLevel         float64  // peak audio level during current recording
	msgCount          int
	width, height     int
	modeLine          string   // "[fast | MP3@16kbps | deepgram]"
	deviceLine        string   // microphone device name
	rateLimit         string   // "45/50 remaining"
	lastText          string   // last transcribed text
	lastMetrics       []string // metrics for last transcription
	copiedToClipboard bool     // show clipboard indicator
	noSpeech          bool     // last transcription had no speech
}

var (
	tuiProgram *tea.Program
	tuiMu      sync.Mutex
)

// Pre-computed pixel styles to avoid allocations in render loop
var (
	pixelColorsRec  = []string{"", "226", "220", "214", "208", "196", "160", "124", "88", "52", "236", "236", "236", "236", "255", "249"}
	pixelColorsIdle = []string{"", "231", "224", "217", "210", "160", "124", "88", "52", "236", "236", "236", "236", "236", "255", "249"}
	pixelStylesRec  [16]lipgloss.Style
	pixelStylesIdle [16]lipgloss.Style
	pixelBgRec      [16][16]lipgloss.Style
	pixelBgIdle     [16][16]lipgloss.Style
)

func init() {
	for i, c := range pixelColorsRec {
		if c != "" {
			pixelStylesRec[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(c))
		}
	}
	for i, c := range pixelColorsIdle {
		if c != "" {
			pixelStylesIdle[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(c))
		}
	}
	for i, fg := range pixelColorsRec {
		for j, bg := range pixelColorsRec {
			if fg != "" && bg != "" {
				pixelBgRec[i][j] = lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg))
			}
		}
	}
	for i, fg := range pixelColorsIdle {
		for j, bg := range pixelColorsIdle {
			if fg != "" && bg != "" {
				pixelBgIdle[i][j] = lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg))
			}
		}
	}
}

func NewTUIProgram() *tea.Program {
	m := tuiModel{}
	return tea.NewProgram(m, tea.WithAltScreen())
}

func tuiTick() tea.Cmd {
	return tea.Tick(60*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m tuiModel) Init() tea.Cmd {
	return tuiTick()
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case tickMsg:
		m.frame++
		return m, tuiTick()

	case RecordingStartMsg:
		m.state = tuiStateRecording
		m.recordingDuration = 0
		m.audioLevel = 0
		m.peakLevel = 0

	case RecordingStopMsg:
		m.state = tuiStateIdle
		m.audioLevel = 0

	case RecordingTickMsg:
		m.recordingDuration = msg.Duration

	case AudioLevelMsg:
		if m.state == tuiStateRecording {
			m.audioLevel = m.audioLevel*0.6 + msg.Level*0.4
			if msg.Level > m.peakLevel {
				m.peakLevel = msg.Level
			}
		}

	case LogMsg:
		// No longer using log buffer - only show last transcription

	case TranscriptionMsg:
		m.msgCount++
		m.lastText = msg.Text
		m.lastMetrics = msg.Metrics
		m.copiedToClipboard = msg.Copied
		m.noSpeech = msg.NoSpeech

	case ModeLineMsg:
		m.modeLine = msg.Text

	case DeviceLineMsg:
		m.deviceLine = msg.Text

	case RateLimitMsg:
		m.rateLimit = msg.Text
	}
	return m, nil
}

func (m tuiModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}

	const eyeWidth = 45
	recording := m.state == tuiStateRecording
	level := m.audioLevel
	if !recording {
		level = 0
	}

	eye := renderHALEye(m.frame, level, recording)

	// Build info section below eye
	var infoLines []string

	// Status line
	if recording {
		status := lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true).
			Render(fmt.Sprintf("● REC %.1fs", m.recordingDuration))
		infoLines = append(infoLines, status)
		// Voice detection warning (after 1s of recording with no voice)
		if m.recordingDuration > 1.0 && m.peakLevel < 0.02 {
			warn := lipgloss.NewStyle().
				Foreground(lipgloss.Color("208")).
				Render("  ⚠ no voice detected")
			infoLines = append(infoLines, warn)
		}
	} else {
		status := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Render("○ STANDBY")
		infoLines = append(infoLines, status)
	}

	// Mode line
	if m.modeLine != "" {
		modeLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Render(m.modeLine)
		infoLines = append(infoLines, modeLine)
	}

	// Device line
	if m.deviceLine != "" {
		deviceLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Render(m.deviceLine)
		infoLines = append(infoLines, deviceLine)
	}

	// Rate limit (gray)
	if m.rateLimit != "" {
		rateLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Render(m.rateLimit)
		infoLines = append(infoLines, rateLine)
	}

	// Percentile table
	if table := renderPercentileTable(); table != "" {
		infoLines = append(infoLines, "")
		tableStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
		for _, line := range strings.Split(table, "\n") {
			infoLines = append(infoLines, tableStyle.Render(line))
		}
	}

	// Empty line for spacing
	infoLines = append(infoLines, "")

	// Help line with version
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("239"))
	boldStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("239")).Bold(true)
	helpLine := boldStyle.Render("Ctrl+Shift+Space") + helpStyle.Render(" to record")
	infoLines = append(infoLines, helpLine)
	infoLines = append(infoLines, helpStyle.Render("ses9000 "+version))

	// Append info to eye
	for _, line := range infoLines {
		eye += line + "\n"
	}

	eyeLines := strings.Split(eye, "\n")

	// Calculate log panel width
	logWidth := m.width - eyeWidth - 1
	if logWidth < 20 {
		logWidth = 20
	}

	// Build right panel content - only last transcription
	var logContent strings.Builder
	wrapWidth := logWidth - 2
	if wrapWidth < 10 {
		wrapWidth = 10
	}

	if m.lastText != "" {
		// Title with number
		title := lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")).
			Render(fmt.Sprintf("Last transcription (#%d)", m.msgCount))
		logContent.WriteString(title + "\n\n")

		// Transcribed text
		var textStyle lipgloss.Style
		if m.noSpeech {
			textStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
		} else {
			textStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
		}
		lines := wrapText(m.lastText, wrapWidth)
		for i, line := range lines {
			logContent.WriteString(textStyle.Render(line))
			if i == len(lines)-1 && m.copiedToClipboard && !m.noSpeech {
				clipboardStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
				logContent.WriteString(" " + clipboardStyle.Render("[✓ copied]"))
			}
			logContent.WriteString("\n")
		}

		// Metrics
		if len(m.lastMetrics) > 0 {
			logContent.WriteString("\n")
			metricsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
			for _, metric := range m.lastMetrics {
				logContent.WriteString(metricsStyle.Render(metric) + "\n")
			}
		}
	} else {
		// No transcription yet
		placeholder := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Render("No transcriptions yet")
		logContent.WriteString(placeholder)
	}

	logPanel := lipgloss.NewStyle().
		Width(logWidth).
		Height(m.height).
		PaddingLeft(1).
		Render(logContent.String())

	// Pad eye panel to full height (eye at top)
	eyePadded := make([]string, m.height)
	for i := range eyePadded {
		if i < len(eyeLines) {
			eyePadded[i] = eyeLines[i]
		} else {
			eyePadded[i] = strings.Repeat(" ", eyeWidth-1)
		}
	}

	eyePanel := lipgloss.NewStyle().
		Width(eyeWidth - 1).
		Height(m.height).
		Render(strings.Join(eyePadded, "\n"))

	return lipgloss.JoinHorizontal(lipgloss.Top, eyePanel, logPanel)
}

func renderHALEye(frame int, level float64, recording bool) string {
	const charsW = 44
	const charsH = 15
	const pixW = charsW
	const pixH = charsH * 2

	centerX := float64(pixW) / 2
	centerY := float64(pixH) / 2

	// Voice-reactive breathing
	var breathe float64
	if recording {
		breathe = math.Sin(float64(frame)*0.10)*0.03 + level*10.0 - 0.05
	} else {
		breathe = math.Sin(float64(frame)*0.08)*0.02 - 0.05
	}

	pixels := make([][]int, pixH)
	for i := range pixels {
		pixels[i] = make([]int, pixW)
	}

	type ring struct {
		radius     float64
		breatheAmt float64
		colorIdx   int
	}

	rings := []ring{
		{0.6, 0.10, 1},
		{1.3, 0.12, 2},
		{2.0, 0.15, 3},
		{2.8, 0.35, 4},  // red rings: high reactivity
		{3.5, 0.40, 5},
		{4.2, 0.38, 6},
		{5.0, 0.30, 7},
		{5.8, 0.15, 8},
		{6.5, 0.03, 9},
		{7.2, 0.0, 10},
		{8.0, 0.0, 11},
		{10.0, 0.0, 12},
		{12.0, 0.0, 13},
	}

	for y := 0; y < pixH; y++ {
		for x := 0; x < pixW; x++ {
			dx := float64(x) - centerX
			dy := float64(y) - centerY
			dist := math.Sqrt(dx*dx + dy*dy)
			for _, r := range rings {
				radius := r.radius + breathe*r.breatheAmt*20
				if radius > 10.0 {
					radius = 10.0
				}
				if dist < radius {
					pixels[y][x] = r.colorIdx
					break
				}
			}
		}
	}

	// Glass reflections
	type spot struct {
		ox, oy float64
		radius float64
		color  int
	}
	dSide := 9.0
	dSide2 := 7.2
	dTop := 10.0
	dTop2 := 8.2
	spots := []spot{
		{-dSide * 0.707, -dSide * 0.707, 0.7, 14},
		{-dSide2 * 0.707, -dSide2 * 0.707, 0.4, 15},
		{0, -dTop, 0.8, 14},
		{0, -dTop2, 0.6, 15},
		{dSide * 0.707, -dSide * 0.707, 0.7, 14},
		{dSide2 * 0.707, -dSide2 * 0.707, 0.4, 15},
		{0, -2.0, 0.6, 14},
	}
	for y := 0; y < pixH; y++ {
		for x := 0; x < pixW; x++ {
			px := float64(x) - centerX
			py := float64(y) - centerY
			for _, s := range spots {
				dx := px - s.ox
				dy := py - s.oy
				rLen := math.Sqrt(s.ox*s.ox + s.oy*s.oy)
				if rLen < 0.001 {
					rLen = 1
				}
				tx, ty := -s.oy/rLen, s.ox/rLen
				dt := dx*tx + dy*ty
				dn := dx*(-ty) + dy*tx
				if (dt*dt)/9.0+dn*dn < s.radius*s.radius {
					pixels[y][x] = s.color
				}
			}
		}
	}

	// Use pre-computed styles based on recording state
	var styles *[16]lipgloss.Style
	var bgStyles *[16][16]lipgloss.Style
	if recording {
		styles = &pixelStylesRec
		bgStyles = &pixelBgRec
	} else {
		styles = &pixelStylesIdle
		bgStyles = &pixelBgIdle
	}

	var result strings.Builder
	for cy := 0; cy < charsH; cy++ {
		for cx := 0; cx < charsW; cx++ {
			topY := cy * 2
			botY := cy*2 + 1
			top := 0
			bot := 0
			if topY < pixH {
				top = pixels[topY][cx]
			}
			if botY < pixH {
				bot = pixels[botY][cx]
			}
			if top == 0 && bot == 0 {
				result.WriteString(" ")
			} else if top == bot {
				result.WriteString(styles[top].Render("█"))
			} else if top != 0 && bot == 0 {
				result.WriteString(styles[top].Render("▀"))
			} else if top == 0 && bot != 0 {
				result.WriteString(styles[bot].Render("▄"))
			} else {
				result.WriteString(bgStyles[top][bot].Render("▀"))
			}
		}
		result.WriteString("\n")
	}
	return result.String()
}

func logToTUI(format string, args ...interface{}) {
	tuiMu.Lock()
	p := tuiProgram
	tuiMu.Unlock()

	if p != nil {
		msg := fmt.Sprintf(format, args...)
		p.Send(LogMsg{Text: msg})
	}
}

func wrapText(text string, width int) []string {
	if len(text) == 0 {
		return []string{""}
	}
	if width <= 0 {
		width = 1
	}

	var lines []string
	for len(text) > width {
		// Find last space within width
		splitAt := width
		for i := width; i > 0; i-- {
			if text[i] == ' ' {
				splitAt = i
				break
			}
		}
		lines = append(lines, text[:splitAt])
		text = strings.TrimLeft(text[splitAt:], " ")
	}
	if len(text) > 0 {
		lines = append(lines, text)
	}
	return lines
}

func renderPercentileTable() string {
	if len(transcriptions) == 0 {
		return ""
	}

	ts := percentileStats.TotalMs
	es := percentileStats.EncodeMs
	tls := percentileStats.TLSMs
	cs := percentileStats.CompPct

	return fmt.Sprintf(
		"        %5s %5s %5s %5s %5s\n"+
			"total   %5.0f %5.0f %5.0f %5.0f %5.0f\n"+
			"encode  %5.0f %5.0f %5.0f %5.0f %5.0f\n"+
			"tls     %5.0f %5.0f %5.0f %5.0f %5.0f\n"+
			"comp    %4.0f%% %4.0f%% %4.0f%% %4.0f%% %4.0f%%",
		"min", "p50", "p90", "p95", "max",
		ts[0], ts[1], ts[2], ts[3], ts[4],
		es[0], es[1], es[2], es[3], es[4],
		tls[0], tls[1], tls[2], tls[3], tls[4],
		cs[0], cs[1], cs[2], cs[3], cs[4],
	)
}
