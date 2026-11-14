package main

// The band scope under the flash picture, as in the MD-UV390 flasher: a spectrum over a waterfall,
// the image's blocks as its band. It is a picture of the progress, not a measurement: up to where
// the write has reached, carriers breathe over a lively floor; beyond it only a low noise floor.

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	specRows  = 4 // the spectrum: rows above the waterfall
	fallRows  = 4 // the waterfall: rows, two lines of history each
	fallEvery = 3 // draws (50 ms) between waterfall lines
	scopeRows = specRows + fallRows
)

// The scope's colours by level, as on a transceiver's waterfall: cool for the floor and the weak,
// hot for the strong. Night, navy, blue, cyan, then yellow, orange, red.
var coolStops = [7][3]float64{{0x06, 0x0b, 0x1f}, {0x12, 0x2a, 0x6b}, {0x25, 0x63, 0xeb}, {0x22, 0xd3, 0xee},
	{0xfd, 0xe0, 0x47}, {0xf9, 0x73, 0x16}, {0xef, 0x44, 0x44}}

// cool is the colour for a level from 0 to 1: up to a half it is cool, above that hot.
func cool(v float64) color.Color {
	t := min(max(v, 0), 1) * float64(len(coolStops)-1)
	i := min(int(t), len(coolStops)-2)
	var c [3]int
	for k := range c {
		c[k] = int(coolStops[i][k] + (coolStops[i+1][k]-coolStops[i][k])*(t-float64(i)))
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2]))
}

// scope is the spectrum's clock (draws of 50 ms) and the waterfall's history, newest line first.
type scope struct {
	ticks int
	fall  [2 * fallRows][]float64
}

func (s *scope) t() float64 { return float64(s.ticks) * 0.05 }

// spectrum is the level in each of w columns at time t with f of the image written, 0 to 1.
func spectrum(w int, t float64, f float64) []float64 {
	v := make([]float64, w)
	head := int(f * float64(w))
	for c := range v {
		x := float64(c)
		noise := 0.5 + 0.5*math.Sin(x*12.9898+t*(2+math.Mod(x*0.618, 1)*5))
		if c >= head {
			v[c] = 0.04 + 0.07*noise
			continue
		}
		v[c] = 0.16 + 0.12*noise
		for k := 0.0; k < 9; k++ { // carriers, each at its own place, width and pace
			at := math.Mod(k*0.3819+0.07, 1) * float64(w)
			wide := 0.7 + math.Mod(k*0.53, 1)*1.6
			v[c] += (0.35 + 0.3*math.Sin(t*(0.7+k*0.23)+k)) * math.Exp(-(x-at)*(x-at)/(2*wide*wide))
		}
		v[c] = min(v[c], 1)
	}
	return v
}

// tick moves the scope on one draw; every fallEvery a new line drops into the waterfall.
func (s *scope) tick(w int, f float64) {
	if len(s.fall[0]) != w { // a new width: start the history again
		for i := range s.fall {
			s.fall[i] = make([]float64, w)
		}
	}
	if s.ticks++; s.ticks%fallEvery == 0 {
		copy(s.fall[1:], s.fall[:len(s.fall)-1])
		s.fall[0] = spectrum(w, s.t(), f)
	}
}

// rows draws the spectrum (bars in eighths of a cell, a white │ where the write is) over the
// waterfall (▀ with one line of history in its foreground and the next in its background).
func (s *scope) rows(w int, f float64, writing bool) []string {
	glyph := []rune(" ▁▂▃▄▅▆▇█")
	now, head := spectrum(w, s.t(), f), int(f*float64(w))
	var lines []string
	for r := range specRows {
		var b strings.Builder
		for c, v := range now {
			k := min(max(int(v*specRows*8)-(specRows-1-r)*8, 0), 8)
			switch {
			case k == 0 && c == head && writing:
				b.WriteString(valS.Render("│"))
			case k == 0:
				b.WriteByte(' ')
			default:
				b.WriteString(lipgloss.NewStyle().Foreground(cool(0.25 + 0.75*v)).Render(string(glyph[k])))
			}
		}
		lines = append(lines, b.String())
	}
	for r := range fallRows {
		var b strings.Builder
		for c := range w {
			b.WriteString(lipgloss.NewStyle().Foreground(cool(1.3 * s.fall[2*r][c])).Background(cool(1.3 * s.fall[2*r+1][c])).Render("▀"))
		}
		lines = append(lines, b.String())
	}
	return lines
}
