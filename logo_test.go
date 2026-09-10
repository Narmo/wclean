package main

import (
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestPixelLogo(t *testing.T) {
	profile, dark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile); lipgloss.SetHasDarkBackground(dark) })
	lipgloss.SetColorProfile(termenv.TrueColor)
	for _, pixel := range []struct {
		x, y  int
		color color.NRGBA
	}{
		{0, 0, color.NRGBA{}},
		{3, 3, color.NRGBA{R: 0xfb, G: 0x5e, B: 0x56, A: 255}},
		{6, 3, color.NRGBA{R: 0xfc, G: 0xd6, B: 0x49, A: 255}},
		{9, 3, color.NRGBA{R: 0x5a, G: 0xf6, B: 0xb6, A: 255}},
		{16, 8, color.NRGBA{R: 0x5c, G: 0xf5, B: 0xb5, A: 255}},
		{3, 9, color.NRGBA{R: 0xf8, G: 0xf8, B: 0xf9, A: 255}},
	} {
		if got := aboutLogo.pixel(pixel.x, pixel.y); got != pixel.color {
			t.Fatalf("pixel (%d,%d): got %v, want %v", pixel.x, pixel.y, got, pixel.color)
		}
	}
	var previous string
	for _, dark := range []bool{false, true} {
		lipgloss.SetHasDarkBackground(dark)
		rows := aboutLogo.render()
		if len(rows) != 12 {
			t.Fatal("pixel grid must occupy 12 terminal rows")
		}
		for _, row := range rows {
			if ansi.StringWidth(row) != 24 {
				t.Fatal("pixel row width changed")
			}
		}
		rendered := strings.Join(rows, "\n")
		for _, r := range ansi.Strip(rendered) {
			if !strings.ContainsRune(" ▀▄\n", r) {
				t.Fatalf("not a rectangular pixel: %q", r)
			}
		}
		red := lipgloss.NewStyle().Foreground(lipgloss.Color("#fb5e56")).Background(lipgloss.Color("#141821")).Render("▀")
		if !strings.Contains(rendered, red) || !strings.Contains(rows[0], "▄") {
			t.Fatal("original colors or transparent corners were lost")
		}
		if previous != "" && previous != rendered {
			t.Fatal("terminal theme changed the artwork's original palette")
		}
		previous = rendered
	}
}
