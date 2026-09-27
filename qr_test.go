package main

import (
	"bytes"
	"image/png"
	"strings"
	"testing"

	"rsc.io/qr"
)

func TestQRPNGIsScaledToFill(t *testing.T) {
	text := strings.Repeat("x", 120)
	b, err := qrPNG(text)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	code, _ := qr.Encode(text, qr.L)
	side := (code.Size + 2*qrQuiet) * qrPixel
	if img.Bounds().Dx() != side || img.Bounds().Dy() != side {
		t.Fatalf("image is %v, want %dx%d", img.Bounds(), side, side)
	}
	lum := func(x, y int) uint32 { r, _, _, _ := img.At(x, y).RGBA(); return r }
	quiet := qrQuiet*qrPixel - 1
	corner := qrQuiet*qrPixel + qrPixel/2
	finderGap := (qrQuiet+1)*qrPixel + qrPixel/2
	if lum(quiet, quiet) == 0 || lum(corner, corner) != 0 || lum(finderGap, finderGap) == 0 {
		t.Fatal("finder pattern not where a scaled code puts it: modules drawn unscaled?")
	}
	far := side - qrQuiet*qrPixel - qrPixel/2
	if lum(far, corner) != 0 || lum(corner, far) != 0 {
		t.Fatal("other finder patterns missing: code doesn't fill the image")
	}
}
