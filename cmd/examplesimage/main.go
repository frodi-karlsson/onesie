// Command examplesimage writes the example image the docs and skills use. It never ships.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

func main() {
	if err := write(filepath.Join("examples", "images", "square.png")); err != nil {
		fmt.Fprintln(os.Stderr, "examplesimage:", err)
		os.Exit(1)
	}
}

func write(path string) error {
	data, err := square()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

func square() ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	red := image.Rect(160, 120, 320, 240)

	for y := range 480 {
		for x := range 640 {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})

			if image.Pt(x, y).In(red) {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
