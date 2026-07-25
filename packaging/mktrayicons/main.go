// mktrayicons generates the menu-bar state icons as macOS *template* images.
//
// Template images carry shape only — AppKit tints them to match the actual menu
// bar, including Tahoe's glass bar whose appearance follows the wallpaper rather
// than the Light/Dark setting. That is why state must be encoded as a badge
// SHAPE, not a colour: the previous colored icons shipped light/dark variants
// picked from AppleInterfaceStyle, which answers the wrong question on Tahoe and
// left a black glyph on a dark bar.
//
// Input is the existing icon_rec.png: an opaque Z glyph plus a colored badge
// disc. The badge's bounding box is found by colour, then redrawn per state.
//
//	go run ./packaging/mktrayicons        # rewrites tray/icon_{rec,warn,busy}.png
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
)

const srcPath = "tray/icon_rec.png"

// badge shapes. Sized at ~5.5pt on screen, so they must differ in silhouette,
// not in detail: a filled disc, a ring, and a bar are told apart at a glance.
type shape int

const (
	disc shape = iota // recording — the loud state gets the solid mark
	ring              // warning (recording but hearing nothing) — hollow reads as "empty"
	bar               // transcribing — a different silhouette entirely
)

func main() {
	root, err := repoRoot()
	if err != nil {
		fail(err)
	}
	src, err := loadPNG(filepath.Join(root, srcPath))
	if err != nil {
		fail(err)
	}
	box, ok := badgeBox(src)
	if !ok {
		fail(fmt.Errorf("no colored badge found in %s", srcPath))
	}

	for _, out := range []struct {
		name string
		s    shape
	}{
		{"icon_rec.png", disc},
		{"icon_warn.png", ring},
		{"icon_busy.png", bar},
	} {
		img := render(src, box, out.s)
		p := filepath.Join(root, "tray", out.name)
		if err := savePNG(p, img); err != nil {
			fail(err)
		}
		fmt.Println("wrote", p)
	}
}

// badgeBox locates the colored disc: the badge is the only saturated colour in
// the source (the glyph is pure white or near-black).
//
// Bounds are tracked as plain ints rather than by unioning image.Rects from an
// inverted seed — image.Rect canonicalises its corners, so a deliberately
// inverted seed silently becomes the full image and the badge shape would then
// overwrite the whole glyph.
func badgeBox(img image.Image) (image.Rectangle, bool) {
	b := img.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	found := false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a>>8 < 250 {
				continue
			}
			r8, g8, b8 := int(r>>8), int(g>>8), int(bl>>8)
			if max3(r8, g8, b8)-min3(r8, g8, b8) < 40 { // greyscale => glyph, not badge
				continue
			}
			found = true
			minX, minY = mini(minX, x), mini(minY, y)
			maxX, maxY = maxi(maxX, x+1), maxi(maxY, y+1)
		}
	}
	if !found {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX, maxY), true
}

// render rewrites src as a template image: every pixel becomes black, alpha is
// preserved (that is the whole content of a template image), and the badge area
// is replaced by the requested shape.
func render(src image.Image, box image.Rectangle, s shape) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	cx := float64(box.Min.X+box.Max.X-1) / 2
	cy := float64(box.Min.Y+box.Max.Y-1) / 2
	rad := float64(box.Dx()) / 2

	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			var a uint8
			if inBox(box, x, y) {
				a = badgeAlpha(s, float64(x)-cx, float64(y)-cy, rad)
			} else {
				_, _, _, a32 := src.At(x, y).RGBA()
				a = uint8(a32 >> 8)
			}
			dst.Set(x, y, color.NRGBA{0, 0, 0, a})
		}
	}
	return dst
}

// badgeAlpha is the shape's coverage at (dx,dy) from the badge centre, with
// one-pixel smoothing at the edges so the small marks don't look ragged.
func badgeAlpha(s shape, dx, dy, rad float64) uint8 {
	d := math.Hypot(dx, dy)
	switch s {
	case disc:
		return cov(rad - d)
	case ring:
		// Outer edge minus an inner hole; ~40% of the radius keeps the hole
		// visible without the stroke thinning to nothing.
		return min8(cov(rad-d), cov(d-rad*0.42))
	default: // bar
		halfH := math.Max(1, rad*0.34)
		return min8(cov(halfH-math.Abs(dy)), cov(rad-math.Abs(dx)))
	}
}

// cov maps a signed distance (pixels inside the shape) to coverage.
func cov(inside float64) uint8 {
	switch {
	case inside >= 0.5:
		return 255
	case inside <= -0.5:
		return 0
	default:
		return uint8((inside + 0.5) * 255)
	}
}

func inBox(r image.Rectangle, x, y int) bool {
	return x >= r.Min.X && x < r.Max.X && y >= r.Min.Y && y < r.Max.Y
}

func min8(a, b uint8) uint8 {
	if a < b {
		return a
	}
	return b
}
func max3(a, b, c int) int { return maxi(a, maxi(b, c)) }
func min3(a, b, c int) int { return mini(a, mini(b, c)) }
func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func loadPNG(p string) (image.Image, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	return img, err
}

func savePNG(p string, img image.Image) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// repoRoot walks up until it finds go.mod, so the tool runs from anywhere.
func repoRoot() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("go.mod not found above %s", d)
		}
		d = parent
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "mktrayicons:", err)
	os.Exit(1)
}
