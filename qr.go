package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mdp/qrterminal/v3"
	"rsc.io/qr"
)

const qrImageCells = 26

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

func qrImage(text string) (string, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return "", err
	}
	code.Scale = 8
	var buf bytes.Buffer
	if err := png.Encode(&buf, code.Image()); err != nil {
		return "", err
	}
	return fmt.Sprintf("\033]1337;File=inline=1;width=%d;preserveAspectRatio=1:%s\a", qrImageCells,
		base64.StdEncoding.EncodeToString(buf.Bytes())), nil
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
	info := strings.Join(instructions, "\n")
	if inlineImages() {
		if img, err := qrImage(text); err == nil {
			fmt.Println(info)
			fmt.Println("  " + img)
			return
		}
	}
	block := qrBlocks(text)
	fmt.Println(lipgloss.JoinHorizontal(lipgloss.Top, indent(block, "  "), "   ", "\n"+info))
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
