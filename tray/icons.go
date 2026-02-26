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
	dotR := 44.0 / 6.5
	iconIdle = renderIcon(22, &transparent, 22.0/8, nil, 0)
	iconIdleHi = renderIcon(44, &transparent, 44.0/8, nil, 0)
	iconRecHi = renderIcon(44, &red, dotR, nil, 0)
	iconWarnHi = renderWarnIcon(44, &red, dotR, nil, 0)
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

func renderWarnIcon(size int, dot *color.RGBA, dotR float64, inner *color.RGBA, innerR float64) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	drawCircleIcon(img, size, dot, dotR, inner, innerR)

	// Small yellow badge with "!" in bottom-right corner
	s := float64(size)
	badgeR := s * 0.34
	badgeCX, badgeCY := s-badgeR+0.5, s-badgeR+0.5
	dark := color.RGBA{R: 40, G: 40, B: 40, A: 255}
	yellow := color.RGBA{R: 255, G: 204, B: 0, A: 255}
	bangHW := badgeR * 0.24

	for y := range size {
		for x := range size {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			if math.Hypot(fx-badgeCX, fy-badgeCY) > badgeR {
				continue
			}
			localY := (fy - (badgeCY - badgeR*0.7)) / (badgeR * 1.4)
			localX := math.Abs(fx - badgeCX)
			isBar := localX <= bangHW && localY >= 0.1 && localY <= 0.62
			isDot := localX <= bangHW && localY >= 0.72 && localY <= 0.85
			if isBar || isDot {
				img.Set(x, y, dark)
			} else {
				img.Set(x, y, yellow)
			}
		}
	}
	return encodePNG(img)
}
