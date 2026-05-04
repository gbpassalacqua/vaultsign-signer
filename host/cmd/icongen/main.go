// Command icongen produces placeholder 16/48/128 PNG icons for the
// Chrome extension. Solid indigo with a white "VS" monogram — good enough
// for dev mode; we'll do real branding before Web Store submission.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

func main() {
	dir := flag.String("dir", "icons", "output directory")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}

	bg := color.RGBA{0x4a, 0x3a, 0xc0, 0xff} // indigo
	fg := color.RGBA{0xff, 0xff, 0xff, 0xff} // white

	for _, sz := range []int{16, 48, 128} {
		img := image.NewRGBA(image.Rect(0, 0, sz, sz))
		// Background
		for y := 0; y < sz; y++ {
			for x := 0; x < sz; x++ {
				img.Set(x, y, bg)
			}
		}
		drawV(img, sz, fg)
		drawS(img, sz, fg)

		path := filepath.Join(*dir, fmt.Sprintf("%d.png", sz))
		f, err := os.Create(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "create:", err)
			os.Exit(1)
		}
		if err := png.Encode(f, img); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		f.Close()
		fmt.Println("wrote", path)
	}
}

// drawV draws a chevron "V" in the left half of the canvas.
func drawV(img *image.RGBA, sz int, c color.Color) {
	margin := sz / 5
	leftX := margin
	rightX := sz/2 - margin/2
	topY := margin
	botY := sz - margin
	thickness := max(1, sz/16)

	// Two diagonals that meet at the bottom center of the V.
	midX := (leftX + rightX) / 2
	drawLine(img, leftX, topY, midX, botY, thickness, c)
	drawLine(img, rightX, topY, midX, botY, thickness, c)
}

// drawS draws a stylized "S" (three horizontal bars + connecting verticals)
// in the right half of the canvas.
func drawS(img *image.RGBA, sz int, c color.Color) {
	margin := sz / 5
	leftX := sz/2 + margin/2
	rightX := sz - margin
	topY := margin
	botY := sz - margin
	midY := (topY + botY) / 2
	thickness := max(1, sz/16)

	// Three horizontal bars
	drawRect(img, leftX, topY, rightX, topY+thickness, c)
	drawRect(img, leftX, midY-thickness/2, rightX, midY+thickness/2+1, c)
	drawRect(img, leftX, botY-thickness, rightX, botY, c)
	// Two connecting verticals (top-left, mid-right)
	drawRect(img, leftX, topY, leftX+thickness, midY, c)
	drawRect(img, rightX-thickness, midY, rightX, botY, c)
}

func drawRect(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.Set(x, y, c)
		}
	}
}

// drawLine draws a thick line between two points using a simple
// parametric sweep — fine for icons, not a Bresenham implementation.
func drawLine(img *image.RGBA, x0, y0, x1, y1, thickness int, c color.Color) {
	steps := abs(x1-x0) + abs(y1-y0)
	if steps == 0 {
		steps = 1
	}
	for i := 0; i <= steps*4; i++ {
		t := float64(i) / float64(steps*4)
		x := int(float64(x0) + t*float64(x1-x0))
		y := int(float64(y0) + t*float64(y1-y0))
		for dy := 0; dy < thickness; dy++ {
			for dx := 0; dx < thickness; dx++ {
				img.Set(x+dx, y+dy, c)
			}
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
