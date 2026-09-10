package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

//go:embed wclean.png
var logoPNG []byte

type pixelArt struct {
	source        image.Image
	width, height int // Each terminal cell holds two vertical pixels.
}

var aboutLogo = pixelArt{decodeLogo(logoPNG), 24, 23}

func decodeLogo(data []byte) image.Image {
	im, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic("invalid embedded logo: " + err.Error())
	}
	return im
}

func (art pixelArt) pixel(x, y int) color.NRGBA {
	if y >= art.height {
		return color.NRGBA{}
	}
	b := art.source.Bounds()
	// Sample the supplied image directly at pixel centers, without interpolation.
	return color.NRGBAModel.Convert(art.source.At(b.Min.X+(2*x+1)*b.Dx()/(2*art.width), b.Min.Y+(2*y+1)*b.Dy()/(2*art.height))).(color.NRGBA)
}

func (art pixelArt) render() []string {
	hex := func(c color.NRGBA) lipgloss.Color { return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)) }
	rows := make([]string, 0, (art.height+1)/2)
	for y := 0; y < art.height; y += 2 {
		var row strings.Builder
		for x := 0; x < art.width; x++ {
			top, bottom := art.pixel(x, y), art.pixel(x, y+1)
			if top.A == 0 && bottom.A == 0 {
				row.WriteByte(' ')
				continue
			}
			style := lipgloss.NewStyle()
			symbol := "▀"
			if top.A == 0 {
				symbol = "▄"
				style = style.Foreground(hex(bottom))
			} else {
				style = style.Foreground(hex(top))
				if bottom.A != 0 {
					style = style.Background(hex(bottom))
				}
			}
			row.WriteString(style.Render(symbol))
		}
		rows = append(rows, row.String())
	}
	return rows
}

func brandHeader(text []string, width int, withLogo bool, art pixelArt) string {
	if !withLogo {
		return strings.Join(text, "\n")
	}
	rows := art.render()
	textWidth := max(1, width-art.width-2)
	lines := strings.Split(ansi.Wrap(strings.Join(text, "\n"), textWidth, ""), "\n")
	for i := range rows {
		if i < len(lines) {
			rows[i] += "  " + ansi.Truncate(lines[i], textWidth, "…")
		}
	}
	return strings.Join(rows, "\n")
}
