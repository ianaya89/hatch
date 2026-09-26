package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	visCols   = 36
	visRows   = 14
	visSubW   = visCols * 2
	visSubH   = visRows * 4
	visPeriod = 9000
	visFrame  = 70 * time.Millisecond
)

type visPalette struct {
	name  string
	stops []string
}

var visualPalettes = []visPalette{
	{"violet", []string{"#2E1065", "#5B21B6", "#7C3AED", "#A78BFA", "#EDE9FE"}},
	{"ocean", []string{"#0C1E3E", "#1E40AF", "#3B82F6", "#93C5FD", "#EFF6FF"}},
	{"aurora", []string{"#042F2E", "#0F766E", "#14B8A6", "#5EEAD4", "#F0FDFA"}},
	{"ember", []string{"#431407", "#9A3412", "#EA580C", "#FDBA74", "#FFF7ED"}},
	{"sakura", []string{"#500724", "#9D174D", "#EC4899", "#F9A8D4", "#FDF2F8"}},
	{"gold", []string{"#422006", "#A16207", "#EAB308", "#FDE047", "#FEFCE8"}},
	{"lime", []string{"#1A2E05", "#4D7C0F", "#84CC16", "#BEF264", "#F7FEE7"}},
	{"ice", []string{"#083344", "#0E7490", "#06B6D4", "#67E8F9", "#ECFEFF"}},
}

type visShape int

const (
	shapeOrb visShape = iota
	shapeRing
	shapeGalaxy
	shapeHelix
)

var shapeNames = []string{"orb", "ring", "galaxy", "helix"}

type point3 struct{ x, y, z, phase float64 }

// visual is a 3D particle cloud whose look (shape, palette, arms, tilt, spin)
// is derived from the session key, so paired peers draw the same thing. The
// rotation angle comes from the wall clock, so two NTP-synced machines spin
// in lockstep and can be compared side by side while moving.
type visual struct {
	shape   visShape
	arms    int
	spin    float64
	tilt    float64
	palette visPalette
	styles  []lipgloss.Style
	points  []point3
}

func newVisual(seed []byte) visual {
	var s [32]byte
	copy(s[:], seed)
	rng := rand.New(rand.NewChaCha8(s))
	v := visual{
		palette: visualPalettes[rng.IntN(len(visualPalettes))],
		shape:   visShape(rng.IntN(len(shapeNames))),
		arms:    2 + rng.IntN(3),
		spin:    1,
	}
	// Each shape reads best from its own angle: galaxies near face-on,
	// rings at three-quarters, helices almost upright.
	tiltRange := map[visShape][2]float64{
		shapeOrb: {0.2, 0.7}, shapeRing: {0.45, 0.8}, shapeGalaxy: {0.85, 1.15}, shapeHelix: {0.1, 0.3},
	}[v.shape]
	v.tilt = tiltRange[0] + rng.Float64()*(tiltRange[1]-tiltRange[0])
	if v.shape == shapeHelix {
		v.arms = 2
	}
	if rng.IntN(2) == 0 {
		v.spin = -1
	}
	for _, c := range v.palette.stops {
		v.styles = append(v.styles, lipgloss.NewStyle().Foreground(lipgloss.Color(c)))
	}
	jitter := func(a float64) float64 { return (rng.Float64()*2 - 1) * a }
	unit := func() [3]float64 {
		x, y, z := rng.NormFloat64(), rng.NormFloat64(), rng.NormFloat64()
		n := math.Sqrt(x*x+y*y+z*z) + 1e-9
		return [3]float64{x / n, y / n, z / n}
	}
	// Orb bands are great circles around random axes, like an armillary sphere.
	var bands [][2][3]float64
	if v.shape == shapeOrb {
		for range v.arms {
			n := unit()
			u := normalize(cross(n, [3]float64{0, 1, 0}))
			if math.Abs(n[1]) > 0.9 {
				u = normalize(cross(n, [3]float64{1, 0, 0}))
			}
			bands = append(bands, [2][3]float64{u, cross(n, u)})
		}
	}
	for i := range 260 {
		var p point3
		switch v.shape {
		case shapeOrb:
			if i%5 == 0 {
				d := unit()
				r := 0.9 + jitter(0.08)
				p = point3{d[0] * r, d[1] * r, d[2] * r, 0}
				break
			}
			bd := bands[i%len(bands)]
			th := rng.Float64() * 2 * math.Pi
			r := 0.88 + jitter(0.04)
			c, s := math.Cos(th)*r, math.Sin(th)*r
			p = point3{bd[0][0]*c + bd[1][0]*s, bd[0][1]*c + bd[1][1]*s, bd[0][2]*c + bd[1][2]*s, 0}
		case shapeRing:
			th, ph := rng.Float64()*2*math.Pi, rng.Float64()*2*math.Pi
			p = point3{(0.72 + 0.2*math.Cos(ph)) * math.Cos(th), 0.2 * math.Sin(ph), (0.72 + 0.2*math.Cos(ph)) * math.Sin(th), 0}
		case shapeGalaxy:
			if i%4 == 0 {
				p = point3{rng.NormFloat64() * 0.14, rng.NormFloat64() * 0.06, rng.NormFloat64() * 0.14, 0}
				break
			}
			t := 0.15 + 0.85*math.Sqrt(rng.Float64())
			a := float64(i%v.arms)*2*math.Pi/float64(v.arms) + t*3.6 + jitter(0.22)
			p = point3{t * math.Cos(a), jitter(0.07 * (1.1 - t)), t * math.Sin(a), 0}
		case shapeHelix:
			if i%4 == 0 {
				t := -0.9 + float64(rng.IntN(10))*0.2
				a, u := t*2.2*math.Pi, rng.Float64()
				p = point3{0.5 * math.Cos(a) * (1 - 2*u), t * 1.35, 0.5 * math.Sin(a) * (1 - 2*u), 0}
				break
			}
			t := rng.Float64()*2 - 1
			a := t*2.2*math.Pi + float64(i%2)*math.Pi
			p = point3{0.5*math.Cos(a) + jitter(0.03), t * 1.35, 0.5*math.Sin(a) + jitter(0.03), 0}
		}
		p.phase = rng.Float64() * 2 * math.Pi
		v.points = append(v.points, p)
	}
	return v
}

func (v visual) caption() string {
	desc := v.palette.name + " " + shapeNames[v.shape]
	switch v.shape {
	case shapeGalaxy:
		desc += fmt.Sprintf(" · %d arms", v.arms)
	case shapeOrb:
		desc += fmt.Sprintf(" · %d bands", v.arms)
	}
	if v.spin > 0 {
		return desc + " · ↻"
	}
	return desc + " · ↺"
}

var brailleBits = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

type visFrameData struct {
	cells [visRows][visCols]rune
	light [visRows][visCols]float64
}

// frameAt projects the cloud at wall-clock time ms into braille cells. Depth
// drives brightness and size: near particles light up and spill into a
// neighbouring sub-pixel.
func (v visual) frameAt(ms int64) *visFrameData {
	angle := float64(ms%visPeriod) / visPeriod * 2 * math.Pi * v.spin
	ca, sa := math.Cos(angle), math.Sin(angle)
	ct, st := math.Cos(v.tilt), math.Sin(v.tilt)
	t := float64(ms) / 1000

	fd := &visFrameData{}
	for r := range fd.light {
		for c := range fd.light[r] {
			fd.light[r][c] = -1
		}
	}
	plot := func(sx, sy int, l float64) {
		if sx < 0 || sy < 0 || sx >= visSubW || sy >= visSubH {
			return
		}
		r, c := sy/4, sx/2
		fd.cells[r][c] |= brailleBits[sy%4][sx%2]
		fd.light[r][c] = max(fd.light[r][c], l)
	}
	scale := float64(visSubH) * 0.44
	for _, p := range v.points {
		x := p.x*ca + p.z*sa
		z := -p.x*sa + p.z*ca
		y := p.y*ct - z*st
		z = p.y*st + z*ct
		if v.shape == shapeHelix {
			x, y = y, x
		}
		f := 2.4 / (3.2 - z)
		sx := int(math.Round(float64(visSubW)/2 + x*f*scale))
		sy := int(math.Round(float64(visSubH)/2 - y*f*scale))
		depth := (z + 1) / 2
		l := depth + 0.18*math.Sin(t*2.1+p.phase)
		plot(sx, sy, l)
		if depth > 0.8 {
			plot(sx+1, sy, l)
		}
	}
	return fd
}

func (v visual) shade(l float64) int {
	return max(0, min(len(v.styles)-1, int(l*float64(len(v.styles)))))
}

func (v visual) render(ms int64) string {
	fd := v.frameAt(ms)
	var b strings.Builder
	for r := range fd.cells {
		b.WriteString("    ")
		for c := range fd.cells[r] {
			if fd.cells[r][c] == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(v.styles[v.shade(fd.light[r][c])].Render(string(0x2800 + fd.cells[r][c])))
		}
		if r < len(fd.cells)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// presentVisual animates the cloud in place on a terminal until stop is
// called, leaving the last frame on screen; elsewhere it logs one frame.
func presentVisual(v visual, logf func(string, ...any)) (stop func()) {
	if !isTTY(os.Stdout) {
		for _, line := range strings.Split(v.render(0), "\n") {
			logf("%s", line)
		}
		logf("  %s", v.caption())
		return func() {}
	}
	lines := visRows + 1
	draw := func(first bool) {
		if !first {
			fmt.Printf("\033[%dA", lines)
		}
		for _, line := range strings.Split(v.render(time.Now().UnixMilli()), "\n") {
			fmt.Printf("\r%s\033[K\n", line)
		}
		fmt.Printf("\r      %s\033[K\n", styleDim.Render(v.caption()))
	}
	draw(true)
	done, quit := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(visFrame)
		defer tk.Stop()
		for {
			select {
			case <-quit:
				return
			case <-tk.C:
				draw(false)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(quit)
			<-done
		})
	}
}

func cross(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func normalize(a [3]float64) [3]float64 {
	n := math.Sqrt(a[0]*a[0]+a[1]*a[1]+a[2]*a[2]) + 1e-12
	return [3]float64{a[0] / n, a[1] / n, a[2] / n}
}
