// Command iconresize takes a square PNG source and writes 16x16, 48x48,
// and 128x128 copies for use as Chrome extension icons. Uses CatmullRom
// resampling — sharper than bilinear at small sizes, better at preserving
// edges on logos than nearest-neighbor.
//
//	go run ./cmd/iconresize -src logo.png -dir ../extension/icons
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"

	_ "image/jpeg"
)

func main() {
	src := flag.String("src", "", "source PNG (square, high resolution)")
	dir := flag.String("dir", "icons", "output directory")
	flag.Parse()

	if *src == "" {
		fmt.Fprintln(os.Stderr, "missing -src")
		os.Exit(2)
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}

	f, err := os.Open(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decode:", err)
		os.Exit(1)
	}
	fmt.Printf("source: %dx%d\n", img.Bounds().Dx(), img.Bounds().Dy())

	for _, sz := range []int{16, 48, 128} {
		dst := image.NewRGBA(image.Rect(0, 0, sz, sz))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

		path := filepath.Join(*dir, fmt.Sprintf("%d.png", sz))
		out, err := os.Create(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "create:", err)
			os.Exit(1)
		}
		if err := png.Encode(out, dst); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		out.Close()
		st, _ := os.Stat(path)
		fmt.Printf("wrote %s (%d bytes)\n", path, st.Size())
	}
}
