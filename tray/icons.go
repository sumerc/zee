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
)

func init() {
	transparent := color.RGBA{A: 0}
	red := color.RGBA{R: 255, G: 59, B: 48, A: 255}
	iconIdle = renderIcon(22, &transparent, 22.0/8)
	iconIdleHi = renderIcon(44, &transparent, 44.0/8)
	iconRecHi = renderIcon(44, &red, 44.0/6.5)
}

func renderIcon(size int, dot *color.RGBA, dotR float64) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size)/2 - 1
	for y := range size {
		for x := range size {
			d := math.Hypot(float64(x)-cx+0.5, float64(y)-cy+0.5)
			if dot != nil && d <= dotR {
				img.Set(x, y, dot)
			} else if d <= r {
				img.Set(x, y, color.Black)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic("renderIcon: " + err.Error())
	}
	return buf.Bytes()
}
