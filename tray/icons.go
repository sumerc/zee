//go:build darwin

package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

var (
	iconIdle   []byte
	iconIdleHi []byte
	iconRecHi  []byte
	iconWarnHi []byte
)

func init() {
	transparent := color.RGBA{A: 0}
	red := color.RGBA{R: 255, G: 59, B: 48, A: 255}
	amber := color.RGBA{R: 255, G: 149, B: 0, A: 255}
	dotR := 44.0 / 6.5
	iconIdle = renderIcon(22, &transparent, 22.0/8, nil, 0)
	iconIdleHi = renderIcon(44, &transparent, 44.0/8, nil, 0)
	iconRecHi = renderIcon(44, &red, dotR, nil, 0)
	iconWarnHi = renderIcon(44, &amber, dotR, nil, 0)
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic("encodePNG: " + err.Error())
	}
	return buf.Bytes()
}

func drawCircleIcon(img *image.RGBA, size int, dot *color.RGBA, dotR float64, inner *color.RGBA, innerR float64) {
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size)/2 - 1
	for y := range size {
		for x := range size {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if inner != nil && d <= innerR {
				img.Set(x, y, inner)
			} else if dot != nil && d <= dotR {
				img.Set(x, y, dot)
			} else if d <= r {
				img.Set(x, y, color.Black)
			}
		}
	}
}

func renderIcon(size int, dot *color.RGBA, dotR float64, inner *color.RGBA, innerR float64) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	drawCircleIcon(img, size, dot, dotR, inner, innerR)
	return encodePNG(img)
}

