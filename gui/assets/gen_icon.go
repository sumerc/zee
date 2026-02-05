//go:build ignore

package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	const size = 22
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	center := float64(size) / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - center + 0.5
			dy := float64(y) - center + 0.5
			dist := math.Sqrt(dx*dx + dy*dy)

			if dist < 4 {
				// Inner red core
				img.Set(x, y, color.RGBA{255, 50, 50, 255})
			} else if dist < 7 {
				// Orange ring
				t := (dist - 4) / 3
				r := uint8(255 - t*100)
				g := uint8(50 + t*50)
				img.Set(x, y, color.RGBA{r, g, 0, 255})
			} else if dist < 9 {
				// Dark outer ring
				img.Set(x, y, color.RGBA{80, 20, 20, 255})
			} else if dist < 10 {
				// Border
				img.Set(x, y, color.RGBA{40, 10, 10, 255})
			}
			// else transparent
		}
	}

	f, _ := os.Create("tray.png")
	defer f.Close()
	png.Encode(f, img)
}
