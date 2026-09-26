package main

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
)

var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#6D28D9", Dark: "#A78BFA"}
	colorDim    = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#7C7F8A"}
	colorOK     = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"}
	colorWarn   = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	colorErr    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}

	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#7C3AED"))
	styleCode   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#111111")).Background(lipgloss.Color("#FBBF24"))
	styleAccent = lipgloss.NewStyle().Foreground(colorAccent)
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Foreground(colorDim)
	styleOK     = lipgloss.NewStyle().Foreground(colorOK)
	styleWarn   = lipgloss.NewStyle().Foreground(colorWarn)
	styleErr    = lipgloss.NewStyle().Foreground(colorErr)
	styleCursor = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
)

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func statusf(format string, args ...any) {
	if isTTY(os.Stderr) {
		fmt.Fprintf(os.Stderr, "\r"+format+"\033[K", args...)
	}
}

func clearStatus() {
	if isTTY(os.Stderr) {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
}

func printSummary(w io.Writer, items []Item) {
	for _, ks := range summarize(items) {
		detail := plural(ks.items, "item")
		if ks.clones > 0 {
			detail += fmt.Sprintf(" · %d clone", ks.clones)
		}
		fmt.Fprintf(w, "  %-16s %-28s %10s\n", kindLabel[ks.kind], styleDim.Render(fmt.Sprintf("%-28s", detail)), humanBytes(ks.size))
	}
}
