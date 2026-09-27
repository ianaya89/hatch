package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mdp/qrterminal/v3"
	"rsc.io/qr"
)

// The inline QR lives in an 18x9 cell box: cells are about twice as tall as
// wide, so that's roughly square and small enough to sit beside the text.
const (
	qrImageCols = 18
	qrImageRows = 9
	qrQuiet     = 2
	qrPixel     = 10
)

// inlineImages reports whether the terminal speaks the iTerm2 image protocol
// (iTerm2, WezTerm). tmux swallows the escape without passthrough, so skip it.
func inlineImages() bool {
	switch os.Getenv("HATCH_QR") {
	case "image":
		return true
	case "blocks":
		return false
	}
	if os.Getenv("TMUX") != "" {
		return false
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm":
		return true
	}
	return os.Getenv("LC_TERMINAL") == "iTerm2"
}

// qrPNG rasterizes the code itself: rsc.io/qr's Image() sizes its bounds by
// Scale but draws modules unscaled, leaving a tiny code in a white canvas.
func qrPNG(text string) ([]byte, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return nil, err
	}
	side := (code.Size + 2*qrQuiet) * qrPixel
	img := image.NewGray(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			mx, my := x/qrPixel-qrQuiet, y/qrPixel-qrQuiet
			v := uint8(0xFF)
			if code.Black(mx, my) {
				v = 0
			}
			img.Pix[y*img.Stride+x] = v
		}
	}
	var buf bytes.Buffer
	err = png.Encode(&buf, img)
	return buf.Bytes(), err
}

func qrImage(text string) (string, error) {
	b, err := qrPNG(text)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("\033]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1:%s\a",
		qrImageCols, qrImageRows, base64.StdEncoding.EncodeToString(b)), nil
}

func qrBlocks(text string) string {
	var buf bytes.Buffer
	qrterminal.GenerateWithConfig(text, qrterminal.Config{
		Level:          qrterminal.L,
		Writer:         &buf,
		HalfBlocks:     true,
		BlackChar:      qrterminal.BLACK_BLACK,
		WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar:      qrterminal.WHITE_WHITE,
		BlackWhiteChar: qrterminal.BLACK_WHITE,
		QuietZone:      2,
	})
	return strings.TrimRight(buf.String(), "\n")
}

// printQR puts the instructions next to the code instead of under it, so the
// banner grows sideways rather than pushing the pairing code off screen.
func printQR(text string, instructions []string) {
	if inlineImages() {
		if img, err := qrImage(text); err == nil {
			printBeside(img, instructions)
			return
		}
	}
	block := qrBlocks(text)
	fmt.Println(lipgloss.JoinHorizontal(lipgloss.Top, indent(block, "  "), "   ", "\n"+strings.Join(instructions, "\n")))
}

// printBeside reserves the rows first so any scrolling happens before the
// cursor is saved; then it draws the image, jumps back and writes the text
// to its right, and finally lands below the block.
func printBeside(img string, lines []string) {
	rows := max(qrImageRows, len(lines))
	fmt.Print(strings.Repeat("\n", rows))
	fmt.Printf("\033[%dA\0337  %s\0338", rows, img)
	top := (qrImageRows - len(lines)) / 2
	for i := range rows {
		if j := i - top; j >= 0 && j < len(lines) {
			fmt.Printf("\033[%dC%s", qrImageCols+5, lines[j])
		}
		fmt.Print("\n")
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
