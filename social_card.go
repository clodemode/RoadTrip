package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// The trip card: a 1200 by 630 PNG drawn by the binary from a cardSpec.
// Contract: spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does.
// The sky, the horizon and Odo are baked layers (tools/og_card/bake.mjs); the
// route, the discs and the words are drawn here. Same spec, same bytes.

//go:embed cardlayers/odo.png cardlayers/horizon.png cardlayers/tokens.json
var cardLayerFS embed.FS

// cardRendererVersion is part of the card's address: change it with the drawing.
const cardRendererVersion = 1

const (
	cardW, cardH = 1200, 630
	cardColumnW  = 548.0 // the left column, from x 72
)

// cardSpec is everything a card shows. Points are the store's public ones, as
// [lng, lat]; the drawing fits them into the route box itself.
type cardSpec struct {
	Name  string     `json:"name"`
	Miles int        `json:"miles"` // 0: unknown, the line is left out
	Days  int        `json:"days"`  // 0: unknown
	Dates string     `json:"dates"` // "": unknown
	State string     `json:"state"` // the pill's words
	Legs  []cardLeg  `json:"legs"`
	Discs []cardDisc `json:"discs"`
}

type cardLeg struct {
	Ord    int          `json:"ord"`
	Dotted bool         `json:"dotted,omitempty"`
	Pts    [][2]float64 `json:"pts"`
}

type cardDisc struct {
	Ord   int        `json:"ord"`
	Label string     `json:"label"`
	Pt    [2]float64 `json:"pt"`
}

type cardTokens struct {
	LegBg, LegBg2, LegInk, LegMuted, LegAccent string
}

type cardAssets struct {
	odo, horizon          image.Image
	tok                   cardTokens
	bold, medium, regular *opentype.Font
}

var (
	cardAssetsOnce sync.Once
	cardAssetsVal  *cardAssets
	cardAssetsErr  error
)

func loadCardAssets() (*cardAssets, error) {
	cardAssetsOnce.Do(func() {
		as := &cardAssets{}
		img := func(name string) image.Image {
			b, err := cardLayerFS.ReadFile("cardlayers/" + name)
			if err != nil {
				cardAssetsErr = err
				return nil
			}
			m, err := png.Decode(bytes.NewReader(b))
			if err != nil {
				cardAssetsErr = fmt.Errorf("%s: %w", name, err)
			}
			return m
		}
		as.odo, as.horizon = img("odo.png"), img("horizon.png")
		if cardAssetsErr != nil {
			return
		}
		tb, err := cardLayerFS.ReadFile("cardlayers/tokens.json")
		if err == nil {
			err = json.Unmarshal(tb, &as.tok)
		}
		if err != nil {
			cardAssetsErr = err
			return
		}
		for _, f := range []struct {
			dst *(*opentype.Font)
			ttf []byte
		}{{&as.bold, gobold.TTF}, {&as.medium, gomedium.TTF}, {&as.regular, goregular.TTF}} {
			if *f.dst, err = opentype.Parse(f.ttf); err != nil {
				cardAssetsErr = err
				return
			}
		}
		cardAssetsVal = as
	})
	return cardAssetsVal, cardAssetsErr
}

func hexColor(s string) color.RGBA {
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	v, _ := strconv.ParseUint(s, 16, 32)
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}

// --- text ----------------------------------------------------------------------

func newFace(f *opentype.Font, size float64) font.Face {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		panic(err) // a parsed Go font with a positive size cannot fail
	}
	return face
}

func textWidth(face font.Face, s string, spacing float64) float64 {
	var w fixed.Int26_6
	prev := rune(-1)
	for _, r := range s {
		if prev >= 0 {
			w += face.Kern(prev, r)
		}
		adv, _ := face.GlyphAdvance(r)
		w += adv + fixed.Int26_6(spacing*64)
		prev = r
	}
	return float64(w) / 64
}

// drawText sets s with its baseline at y, from x; align "c" centres on x.
func drawText(dst *image.RGBA, face font.Face, x, y float64, s string, c color.Color, spacing float64, centre bool) {
	if centre {
		x -= textWidth(face, s, spacing) / 2
	}
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face}
	dot := fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}
	prev := rune(-1)
	for _, r := range s {
		if prev >= 0 {
			dot.X += face.Kern(prev, r)
		}
		d.Dot = dot
		d.DrawString(string(r))
		adv, _ := face.GlyphAdvance(r)
		dot.X += adv + fixed.Int26_6(spacing*64)
		prev = r
	}
}

// truncate cuts s with an ellipsis until it is no wider than max.
func truncate(face font.Face, s string, max, spacing float64) string {
	if textWidth(face, s, spacing) <= max {
		return s
	}
	rs := []rune(s)
	for len(rs) > 1 {
		rs = rs[:len(rs)-1]
		t := strings.TrimRight(string(rs), " ") + "…"
		if textWidth(face, t, spacing) <= max {
			return t
		}
	}
	return "…"
}

// fitName sets the trip name: one line shrunk from 96 px to a floor of 56,
// else two lines at the largest size (72 down to 56) that holds them, the
// second cut with an ellipsis if even that is too long. It never exceeds max.
func fitName(f *opentype.Font, name string, max float64) (size float64, lines []string) {
	spacing := func(s float64) float64 { return -s * 0.03 }
	for s := 96.0; s >= 56; s -= 2 {
		if textWidth(newFace(f, s), name, spacing(s)) <= max {
			return s, []string{name}
		}
	}
	words := strings.Fields(name)
	for s := 72.0; s >= 56; s -= 2 {
		face := newFace(f, s)
		l1 := ""
		i := 0
		for ; i < len(words); i++ {
			try := strings.TrimSpace(l1 + " " + words[i])
			if textWidth(face, try, spacing(s)) > max && l1 != "" {
				break
			}
			l1 = try
		}
		l2 := strings.Join(words[i:], " ")
		if textWidth(face, l1, spacing(s)) <= max && textWidth(face, l2, spacing(s)) <= max {
			if l2 == "" {
				return s, []string{l1}
			}
			return s, []string{l1, l2}
		}
	}
	face := newFace(f, 56)
	l1 := ""
	i := 0
	for ; i < len(words); i++ {
		try := strings.TrimSpace(l1 + " " + words[i])
		if textWidth(face, try, spacing(56)) > max && l1 != "" {
			break
		}
		l1 = try
	}
	l1 = truncate(face, l1, max, spacing(56))
	l2 := truncate(face, strings.Join(words[i:], " "), max, spacing(56))
	if l2 == "" || l2 == "…" && i >= len(words) {
		return 56, []string{l1}
	}
	return 56, []string{l1, l2}
}

// --- shapes --------------------------------------------------------------------

type pt struct{ x, y float64 }
type poly []pt

func area(p poly) float64 {
	a := 0.0
	for i := range p {
		j := (i + 1) % len(p)
		a += p[i].x*p[j].y - p[j].x*p[i].y
	}
	return a / 2
}

// fillPolys fills the union of polys in one colour. All are wound the same way
// so overlaps add up to full cover and never cancel.
func fillPolys(dst *image.RGBA, c color.Color, polys []poly) {
	if len(polys) == 0 {
		return
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range polys {
		for _, q := range p {
			minX, maxX = math.Min(minX, q.x), math.Max(maxX, q.x)
			minY, maxY = math.Min(minY, q.y), math.Max(maxY, q.y)
		}
	}
	r := image.Rect(int(math.Floor(minX))-1, int(math.Floor(minY))-1, int(math.Ceil(maxX))+1, int(math.Ceil(maxY))+1).Intersect(dst.Bounds())
	if r.Empty() {
		return
	}
	var ras vector.Rasterizer
	ras.Reset(r.Dx(), r.Dy())
	for _, p := range polys {
		if len(p) < 3 {
			continue
		}
		q := p
		if area(p) < 0 {
			q = make(poly, len(p))
			for i := range p {
				q[len(p)-1-i] = p[i]
			}
		}
		ras.MoveTo(float32(q[0].x-float64(r.Min.X)), float32(q[0].y-float64(r.Min.Y)))
		for _, v := range q[1:] {
			ras.LineTo(float32(v.x-float64(r.Min.X)), float32(v.y-float64(r.Min.Y)))
		}
		ras.ClosePath()
	}
	ras.Draw(dst, r, image.NewUniform(c), image.Point{})
}

func disc(c pt, r float64) poly {
	n := int(math.Max(16, math.Ceil(r*2.5)))
	p := make(poly, n)
	for i := range p {
		a := 2 * math.Pi * float64(i) / float64(n)
		p[i] = pt{c.x + r*math.Cos(a), c.y + r*math.Sin(a)}
	}
	return p
}

// segment is a quad round a segment, half-width r.
func segment(a, b pt, r float64) poly {
	dx, dy := b.x-a.x, b.y-a.y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return nil
	}
	nx, ny := -dy/l*r, dx/l*r
	return poly{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}}
}

// stroke is a round-joined, round-capped line: a quad per segment and a disc at each vertex.
func stroke(line []pt, r float64) []poly {
	var out []poly
	for i, p := range line {
		out = append(out, disc(p, r))
		if i > 0 {
			if s := segment(line[i-1], p, r); s != nil {
				out = append(out, s)
			}
		}
	}
	return out
}

// dots places discs of radius r every step along a line, starting at its first point.
func dots(line []pt, step, r float64) []poly {
	var out []poly
	carry := 0.0
	for i := 1; i < len(line); i++ {
		a, b := line[i-1], line[i]
		l := math.Hypot(b.x-a.x, b.y-a.y)
		if l == 0 {
			continue
		}
		for d := carry; d <= l; d += step {
			t := d / l
			out = append(out, disc(pt{a.x + (b.x-a.x)*t, a.y + (b.y-a.y)*t}, r))
			carry = d + step - l
		}
		if carry > step {
			carry = 0
		}
	}
	if len(line) > 0 {
		out = append(out, disc(line[len(line)-1], r))
	}
	return out
}

// --- the card ------------------------------------------------------------------

// routeBox is where the route is fitted: centre of discs, kept clear of the words and the horizon.
var routeBox = struct{ x0, y0, x1, y1 float64 }{700, 100, 1110, 450}

// fitRoute projects every point with one scale for both axes (longitude by the
// cosine of the middle latitude) and centres the result in routeBox.
func fitRoute(spec *cardSpec) (legs [][]pt, discs []pt) {
	var all [][2]float64
	for _, l := range spec.Legs {
		all = append(all, l.Pts...)
	}
	for _, d := range spec.Discs {
		all = append(all, d.Pt)
	}
	if len(all) == 0 {
		return nil, nil
	}
	minLng, maxLng, minLat, maxLat := all[0][0], all[0][0], all[0][1], all[0][1]
	for _, p := range all {
		minLng, maxLng = math.Min(minLng, p[0]), math.Max(maxLng, p[0])
		minLat, maxLat = math.Min(minLat, p[1]), math.Max(maxLat, p[1])
	}
	kx := math.Cos((minLat + maxLat) / 2 * math.Pi / 180)
	w, h := (maxLng-minLng)*kx, maxLat-minLat
	bw, bh := routeBox.x1-routeBox.x0, routeBox.y1-routeBox.y0
	scale := 1.0
	switch {
	case w > 0 && h > 0:
		scale = math.Min(bw/w, bh/h)
	case w > 0:
		scale = bw / w
	case h > 0:
		scale = bh / h
	}
	ox := routeBox.x0 + (bw-w*scale)/2
	oy := routeBox.y0 + (bh-h*scale)/2
	proj := func(p [2]float64) pt {
		return pt{ox + (p[0]-minLng)*kx*scale, oy + (maxLat-p[1])*scale}
	}
	for _, l := range spec.Legs {
		line := make([]pt, len(l.Pts))
		for i, p := range l.Pts {
			line[i] = proj(p)
		}
		legs = append(legs, line)
	}
	for _, d := range spec.Discs {
		discs = append(discs, proj(d.Pt))
	}
	return legs, discs
}

// drawCard draws the spec: the same spec always gives the same pixels.
func drawCard(spec *cardSpec) (*image.RGBA, error) {
	as, err := loadCardAssets()
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, cardW, cardH))
	bg, bg2 := hexColor(as.tok.LegBg), hexColor(as.tok.LegBg2)
	ink, muted, accent := hexColor(as.tok.LegInk), hexColor(as.tok.LegMuted), hexColor(as.tok.LegAccent)
	white := color.RGBA{255, 255, 255, 255}

	// the sky, then the horizon along the foot, then Odo standing on it
	for y := 0; y < cardH; y++ {
		t := math.Max(0, math.Min(1, (float64(y)/cardH-0.45)/0.55))
		c := color.RGBA{
			uint8(math.Round(float64(bg.R) + (float64(bg2.R)-float64(bg.R))*t)),
			uint8(math.Round(float64(bg.G) + (float64(bg2.G)-float64(bg.G))*t)),
			uint8(math.Round(float64(bg.B) + (float64(bg2.B)-float64(bg.B))*t)), 255}
		for x := 0; x < cardW; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	draw.Draw(img, image.Rect(0, cardH-210, cardW, cardH), as.horizon, image.Point{}, draw.Over)
	xdraw.CatmullRom.Scale(img, image.Rect(60, 448, 260, 586), as.odo, as.odo.Bounds(), xdraw.Over, nil)
	drawText(img, newFace(as.medium, 30), 286, 566, "Odo counts every mile.", ink, 0, false)

	// the words
	drawText(img, newFace(as.bold, 24), 72, 118, "ROAD TRIP", muted, 6, false)
	size, lines := fitName(as.bold, spec.Name, cardColumnW)
	nameFace := newFace(as.bold, size)
	base := 118 + 28 + 0.72*size
	for i, l := range lines {
		if i > 0 {
			base += 1.1 * size
		}
		drawText(img, nameFace, 68, base, l, ink, -size*0.03, false)
	}
	gap := 44 + 0.25*size
	if spec.Miles > 0 {
		s := thousands(spec.Miles) + " miles"
		if spec.Days > 0 {
			s += " · " + strconv.Itoa(spec.Days) + " day"
			if spec.Days != 1 {
				s += "s"
			}
		}
		base += gap
		drawText(img, newFace(as.medium, 42), 72, base, truncate(newFace(as.medium, 42), s, cardColumnW, 0), ink, 0, false)
		gap = 54
	} else if spec.Days > 0 {
		s := strconv.Itoa(spec.Days) + " day"
		if spec.Days != 1 {
			s += "s"
		}
		base += gap
		drawText(img, newFace(as.medium, 42), 72, base, s, ink, 0, false)
		gap = 54
	}
	if spec.Dates != "" {
		base += gap
		ds := 34.0
		for ds > 24 && textWidth(newFace(as.regular, ds), spec.Dates, 0) > cardColumnW {
			ds -= 2
		}
		drawText(img, newFace(as.regular, ds), 72, base, truncate(newFace(as.regular, ds), spec.Dates, cardColumnW, 0), muted, 0, false)
	}
	if spec.State != "" {
		pf := newFace(as.bold, 25)
		w := textWidth(pf, spec.State, 0) + 64
		top := base + 32
		fillPolys(img, accent, []poly{
			disc(pt{72 + 26, top + 26}, 26), disc(pt{72 + w - 26, top + 26}, 26),
			{{72 + 26, top}, {72 + w - 26, top}, {72 + w - 26, top + 52}, {72 + 26, top + 52}},
		})
		drawText(img, pf, 72+w/2, top+35, spec.State, white, 0, true)
	}

	// the route: casings, then each leg's colour (dotted after the car), the start ring, the day discs
	legs, discs := fitRoute(spec)
	var casing []poly
	for _, l := range legs {
		casing = append(casing, stroke(l, 8)...)
	}
	fillPolys(img, white, casing)
	for i, l := range legs {
		c := hexColor(legColor(spec.Legs[i].Ord))
		if spec.Legs[i].Dotted {
			fillPolys(img, c, dots(l, 18, 5))
		} else {
			fillPolys(img, c, stroke(l, 4.5))
		}
	}
	if len(legs) > 0 && len(legs[0]) > 0 {
		s := legs[0][0]
		fillPolys(img, color.RGBA{31, 41, 55, 255}, []poly{disc(s, 14.5)})
		fillPolys(img, white, []poly{disc(s, 9.5)})
	}
	// discs: full size unless two would touch; then they shrink to clear each
	// other, and a disc too small to hold its label loses the label, not the disc.
	r := 19.0
	for i := range discs {
		for j := i + 1; j < len(discs); j++ {
			d := math.Hypot(discs[i].x-discs[j].x, discs[i].y-discs[j].y)
			r = math.Min(r, math.Max(5, (d-4)/2-3.5))
		}
	}
	for i, d := range discs {
		c := hexColor(legColor(spec.Discs[i].Ord))
		fillPolys(img, white, []poly{disc(d, r+4)})
		fillPolys(img, c, []poly{disc(d, r)})
		if r >= 15 {
			drawText(img, newFace(as.bold, 17), d.x, d.y+6, spec.Discs[i].Label, white, 0, true)
		}
	}
	return img, nil
}

// encodeCard is drawCard as PNG bytes.
func encodeCard(spec *cardSpec) ([]byte, error) {
	img, err := drawCard(spec)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
