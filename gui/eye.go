//go:build gui

package gui

import (
	"image/color"
	"math"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

const (
	eyeWidth    = 44
	eyeHeight   = 15
	pixelHeight = eyeHeight * 2
)

// Color palettes (ANSI 256 → RGB approximations)
var (
	colorsRec = []color.Color{
		color.RGBA{0, 0, 0, 255},       // 0: transparent/black
		color.RGBA{255, 255, 0, 255},   // 1: yellow (226)
		color.RGBA{255, 215, 0, 255},   // 2: gold (220)
		color.RGBA{255, 175, 0, 255},   // 3: orange (214)
		color.RGBA{255, 135, 0, 255},   // 4: dark orange (208)
		color.RGBA{255, 0, 0, 255},     // 5: red (196)
		color.RGBA{215, 0, 0, 255},     // 6: dark red (160)
		color.RGBA{175, 0, 0, 255},     // 7: darker red (124)
		color.RGBA{135, 0, 0, 255},     // 8: (88)
		color.RGBA{95, 0, 0, 255},      // 9: (52)
		color.RGBA{48, 48, 48, 255},    // 10: gray (236)
		color.RGBA{48, 48, 48, 255},    // 11: gray
		color.RGBA{48, 48, 48, 255},    // 12: gray
		color.RGBA{48, 48, 48, 255},    // 13: gray
		color.RGBA{255, 255, 255, 255}, // 14: white (255)
		color.RGBA{180, 180, 180, 255}, // 15: light gray (249)
	}

	colorsIdle = []color.Color{
		color.RGBA{0, 0, 0, 255},       // 0: transparent/black
		color.RGBA{255, 255, 255, 255}, // 1: white (231)
		color.RGBA{255, 215, 215, 255}, // 2: pink (224)
		color.RGBA{255, 175, 175, 255}, // 3: (217)
		color.RGBA{255, 135, 135, 255}, // 4: (210)
		color.RGBA{215, 0, 0, 255},     // 5: (160)
		color.RGBA{175, 0, 0, 255},     // 6: (124)
		color.RGBA{135, 0, 0, 255},     // 7: (88)
		color.RGBA{95, 0, 0, 255},      // 8: (52)
		color.RGBA{48, 48, 48, 255},    // 9: gray (236)
		color.RGBA{48, 48, 48, 255},    // 10:
		color.RGBA{48, 48, 48, 255},    // 11:
		color.RGBA{48, 48, 48, 255},    // 12:
		color.RGBA{48, 48, 48, 255},    // 13:
		color.RGBA{255, 255, 255, 255}, // 14: white
		color.RGBA{180, 180, 180, 255}, // 15: light gray
	}
)

type EyeWidget struct {
	widget.BaseWidget
	mu        sync.Mutex
	frame     int
	level     float64
	recording bool
	duration  float64
	noVoice   bool
	stopCh    chan struct{}
}

func NewEyeWidget() *EyeWidget {
	e := &EyeWidget{stopCh: make(chan struct{})}
	e.ExtendBaseWidget(e)
	go e.animate()
	return e
}

func (e *EyeWidget) SetRecording(r bool) {
	e.mu.Lock()
	e.recording = r
	if !r {
		e.level = 0
	}
	e.mu.Unlock()
}

func (e *EyeWidget) SetLevel(l float64) {
	e.mu.Lock()
	if e.recording {
		if l > e.level {
			e.level = e.level*0.2 + l*0.8
		} else {
			e.level = e.level*0.7 + l*0.3
		}
	}
	e.mu.Unlock()
}

func (e *EyeWidget) SetDuration(d float64) {
	e.mu.Lock()
	e.duration = d
	e.mu.Unlock()
}

func (e *EyeWidget) SetNoVoice(v bool) {
	e.mu.Lock()
	e.noVoice = v
	e.mu.Unlock()
}

func (e *EyeWidget) Stop() {
	select {
	case <-e.stopCh:
	default:
		close(e.stopCh)
	}
}

func (e *EyeWidget) animate() {
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.mu.Lock()
			e.frame++
			e.mu.Unlock()
			fyne.Do(func() {
				e.Refresh()
			})
		}
	}
}

func (e *EyeWidget) MinSize() fyne.Size {
	return fyne.NewSize(float32(eyeWidth*8), float32(eyeHeight*16))
}

func (e *EyeWidget) CreateRenderer() fyne.WidgetRenderer {
	r := &eyeRenderer{eye: e}
	r.rects = make([][]*canvas.Rectangle, eyeHeight)
	for y := 0; y < eyeHeight; y++ {
		r.rects[y] = make([]*canvas.Rectangle, eyeWidth)
		for x := 0; x < eyeWidth; x++ {
			r.rects[y][x] = canvas.NewRectangle(color.Black)
		}
	}
	return r
}

type eyeRenderer struct {
	eye   *EyeWidget
	rects [][]*canvas.Rectangle
}

func (r *eyeRenderer) Layout(size fyne.Size) {
	cellW := size.Width / float32(eyeWidth)
	cellH := size.Height / float32(eyeHeight)
	for y := 0; y < eyeHeight; y++ {
		for x := 0; x < eyeWidth; x++ {
			r.rects[y][x].Move(fyne.NewPos(float32(x)*cellW, float32(y)*cellH))
			r.rects[y][x].Resize(fyne.NewSize(cellW, cellH))
		}
	}
}

func (r *eyeRenderer) MinSize() fyne.Size {
	return r.eye.MinSize()
}

func (r *eyeRenderer) Refresh() {
	r.eye.mu.Lock()
	frame := r.eye.frame
	level := r.eye.level
	recording := r.eye.recording
	r.eye.mu.Unlock()

	pixels := computePixels(frame, level, recording)
	colors := colorsIdle
	if recording {
		colors = colorsRec
	}

	// Half-block rendering: each rect represents 2 vertical pixels
	for cy := 0; cy < eyeHeight; cy++ {
		topY := cy * 2
		botY := cy*2 + 1
		for cx := 0; cx < eyeWidth; cx++ {
			top := 0
			bot := 0
			if topY < pixelHeight {
				top = pixels[topY][cx]
			}
			if botY < pixelHeight {
				bot = pixels[botY][cx]
			}
			// Blend the two pixel colors for the rect
			c := blendColors(colors[top], colors[bot])
			r.rects[cy][cx].FillColor = c
			r.rects[cy][cx].Refresh()
		}
	}
}

func blendColors(top, bot color.Color) color.Color {
	tr, tg, tb, _ := top.RGBA()
	br, bg, bb, _ := bot.RGBA()
	return color.RGBA{
		R: uint8((tr + br) / 512),
		G: uint8((tg + bg) / 512),
		B: uint8((tb + bb) / 512),
		A: 255,
	}
}

func (r *eyeRenderer) Objects() []fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, 0, eyeWidth*eyeHeight)
	for y := 0; y < eyeHeight; y++ {
		for x := 0; x < eyeWidth; x++ {
			objs = append(objs, r.rects[y][x])
		}
	}
	return objs
}

func (r *eyeRenderer) Destroy() {
	r.eye.Stop()
}

// computePixels generates the HAL eye pixel grid (same logic as tui.go)
func computePixels(frame int, level float64, recording bool) [][]int {
	centerX := float64(eyeWidth) / 2
	centerY := float64(pixelHeight) / 2

	var breathe float64
	if recording {
		breathe = math.Sin(float64(frame)*0.15)*0.08 + level*15.0
	} else {
		breathe = math.Sin(float64(frame)*0.10) * 0.05
	}

	pixels := make([][]int, pixelHeight)
	for i := range pixels {
		pixels[i] = make([]int, eyeWidth)
	}

	type ring struct {
		radius     float64
		breatheAmt float64
		colorIdx   int
	}

	rings := []ring{
		{0.6, 0.30, 1},
		{1.3, 0.35, 2},
		{2.0, 0.30, 3},
		{2.8, 0.20, 4},
		{3.5, 0.18, 5},
		{4.2, 0.15, 6},
		{5.0, 0.12, 7},
		{5.8, 0.08, 8},
		{6.5, 0.03, 9},
		{7.2, 0.0, 10},
		{8.0, 0.0, 11},
		{10.0, 0.0, 12},
		{12.0, 0.0, 13},
	}

	for y := 0; y < pixelHeight; y++ {
		for x := 0; x < eyeWidth; x++ {
			dx := float64(x) - centerX
			dy := float64(y) - centerY
			dist := math.Sqrt(dx*dx + dy*dy)
			for _, r := range rings {
				radius := r.radius + breathe*r.breatheAmt*20
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

	for y := 0; y < pixelHeight; y++ {
		for x := 0; x < eyeWidth; x++ {
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

	return pixels
}
