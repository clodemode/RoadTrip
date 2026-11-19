package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
)

// The photo card: the picture a visitor's photo page shows, cropped to fill
// 1200 by 630, with a strip along the foot that names the trip and the day.
// The strip never holds a place, a checkpoint, a caption or a clock time. The
// bytes are re-encoded from pixels, so no EXIF or other metadata of the source
// reaches the card. A photo a visitor cannot open (see visitorPhoto) has no card.
// Contract: spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does.

// photoCardRendererVersion is part of the card's address: change it with the drawing.
const photoCardRendererVersion = 1

const (
	photoStripH = 120
	// photoCardMaxPixels refuses a source that would cost too much to decode on
	// the small machine (photo.go admits 60 MP; the card reads the thumbnail
	// instead of a source above this).
	photoCardMaxPixels = 20_000_000
	photoCardMaxBytes  = 300 * 1024
	photoCardKeep      = 6 // cards held in memory
)

var (
	errPhotoCardSource = errors.New("no rendition of the photo can be drawn")
	errPhotoCardTooBig = errors.New("the photo has too many pixels to draw a card from")
	photoCardSlot      = make(chan struct{}, 1) // one decode at a time
)

// photoStrip is every word on the strip, and all of it.
type photoStrip struct {
	Name string // the trip's name
	Line string // "Day N" or empty
}

func photoStripFor(rideName string, day int) photoStrip {
	s := photoStrip{Name: rideName}
	if day > 0 {
		s.Line = "Day " + strconv.Itoa(day)
	}
	return s
}

// alt is the card in plain words, for og:image:alt.
func (s photoStrip) alt() string {
	t := "A photo from " + s.Name
	if s.Line != "" {
		t += ", " + strings.ToLower(s.Line[:1]) + s.Line[1:]
	}
	return t
}

// visitorPhoto is the one rule for which photo a visitor may open: a post with
// a photo, on a leg of the ride the site shows. The photo page and its card
// both ask here.
func (a *App) visitorPhoto(id int64) (*Post, *rideView, bool) {
	rv, err := a.loadRide()
	if err != nil {
		return nil, nil, false
	}
	post, err := a.store.Post(id)
	if err != nil || post.Photo == nil || !legInRide(rv.Legs, post.LegID) {
		return nil, nil, false
	}
	return post, rv, true
}

// photoStripOf is the strip for a post's photo: the trip name and the day of
// the leg the post is on.
func photoStripOf(post *Post, rv *rideView) photoStrip {
	day := 0
	for i, l := range rv.Legs {
		if l.ID == post.LegID {
			day = i + 1
		}
	}
	return photoStripFor(rv.Ride.Name, day)
}

// photoCardSource picks the public rendition to draw from: the picture the
// photo page shows, unless it has too many pixels, then the thumbnail. It
// reads headers only.
func (a *App) photoCardSource(p *Photo) (rel string, orientation int, err error) {
	for _, c := range []string{p.Path, p.ThumbPath} {
		f, err := os.Open(a.mediaFile(c))
		if err != nil {
			continue
		}
		cfg, err := jpeg.DecodeConfig(f)
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > photoCardMaxPixels {
			f.Close()
			continue
		}
		o := 1 // the thumbnail is already upright
		if c == p.Path {
			_, _ = f.Seek(0, 0)
			o = readEXIF(f).orientation
		}
		f.Close()
		return c, o, nil
	}
	return "", 1, errPhotoCardSource
}

// photoCardHash covers the photo's content identity (media is content
// addressed), the rendition read, the strip text and the drawing's version.
func photoCardHash(sha, rel string, s photoStrip) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("photocard/%d/%s/%s/%s/%s", photoCardRendererVersion, sha, rel, s.Name, s.Line)))
	return hex.EncodeToString(h[:6])
}

// photoCardAddress is og:image and og:image:alt for a photo page, or ok=false
// when the page must name the ride's trip card.
func (a *App) photoCardAddress(post *Post, rv *rideView) (img, alt string, ok bool) {
	rel, _, err := a.photoCardSource(post.Photo)
	if err != nil {
		return "", "", false
	}
	s := photoStripOf(post, rv)
	return a.siteURL + "/og/photo/" + strconv.FormatInt(post.ID, 10) + ".jpg?v=" + photoCardHash(post.Photo.SHA256, rel, s), s.alt(), true
}

// --- drawing -------------------------------------------------------------------

// photoCardFill scales src to cover 1200 by 630 about its centre, never
// stretching. It shrinks by a whole factor first, then orients the small
// image, so a large source is never copied at full size.
func photoCardFill(src image.Image, orientation int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if orientation >= 5 {
		w, h = h, w
	}
	sc := math.Max(cardW/float64(w), cardH/float64(h))
	if k := int(1 / sc); k >= 2 {
		src = boxDown(src, k)
	}
	src = orient(src, orientation)
	sb := src.Bounds()
	sw, sh := float64(sb.Dx()), float64(sb.Dy())
	sc = math.Max(cardW/sw, cardH/sh)
	cw, ch := cardW/sc, cardH/sc
	x0 := sb.Min.X + int(math.Round((sw-cw)/2))
	y0 := sb.Min.Y + int(math.Round((sh-ch)/2))
	r := image.Rect(x0, y0, x0+int(math.Round(cw)), y0+int(math.Round(ch))).Intersect(sb)
	dst := image.NewRGBA(image.Rect(0, 0, cardW, cardH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, r, xdraw.Src, nil)
	return dst
}

func lerpColor(a, b color.RGBA, t float64) color.RGBA {
	l := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 255}
}

// drawPhotoCard draws the card: the photo to fill, then the strip.
func drawPhotoCard(src image.Image, orientation int, strip photoStrip) (*image.RGBA, error) {
	as, err := loadCardAssets()
	if err != nil {
		return nil, err
	}
	img := photoCardFill(src, orientation)
	from, mid, to := hexColor(as.tok.MastFrom), hexColor(as.tok.MastMid), hexColor(as.tok.MastTo)
	top := cardH - photoStripH
	for x := 0; x < cardW; x++ {
		t := float64(x) / (cardW - 1)
		c := lerpColor(from, mid, t/0.55)
		if t > 0.55 {
			c = lerpColor(mid, to, (t-0.55)/0.45)
		}
		for y := top; y < cardH; y++ {
			img.SetRGBA(x, y, c)
		}
	}
	edge := hexColor(as.tok.MastEdge)
	for y := top; y < top+3; y++ {
		for x := 0; x < cardW; x++ {
			img.SetRGBA(x, y, edge)
		}
	}
	// Odo on his disc, at the left of the strip
	cy := float64(top) + photoStripH/2 + 3
	fillPolys(img, hexColor(as.tok.LegDisc), []poly{disc(pt{92, cy}, 44)})
	ob := as.odo.Bounds()
	ow := 78.0
	oh := ow * float64(ob.Dy()) / float64(ob.Dx())
	xdraw.CatmullRom.Scale(img, image.Rect(int(92-ow/2), int(cy-oh/2)+3, int(92+ow/2), int(cy-oh/2+oh)+3), as.odo, ob, xdraw.Over, nil)
	// the words
	ink, tag := hexColor(as.tok.MastInk), hexColor(as.tok.MastTag)
	const maxW = cardW - 160 - 48
	size := 40.0
	for size > 30 && textWidth(newFace(as.bold, size), strip.Name, 0) > maxW {
		size -= 2
	}
	nf := newFace(as.bold, size)
	name := truncateWords(nf, strip.Name, maxW)
	if strip.Line == "" {
		drawText(img, nf, 160, cy+size*0.35, name, ink, 0, false)
	} else {
		drawText(img, nf, 160, cy-4, name, ink, 0, false)
		drawText(img, newFace(as.regular, 27), 160, cy+34, strip.Line, tag, 0, false)
	}
	return img, nil
}

// encodePhotoCard is the card as JPEG bytes, under 300 KB: quality 82, stepped
// down until it fits. No metadata is written: image/jpeg emits none.
func encodePhotoCard(img *image.RGBA) ([]byte, error) {
	var out []byte
	for q := 82; q >= 40; q -= 6 {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		out = buf.Bytes()
		if len(out) < photoCardMaxBytes {
			return out, nil
		}
	}
	return out, fmt.Errorf("the card is %d bytes at the lowest quality", len(out))
}

// renderPhotoCard decodes the file (header-checked first) and draws the card.
func renderPhotoCard(path string, orientation int, strip photoStrip) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > photoCardMaxPixels {
		return nil, errPhotoCardTooBig
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	src, err := jpeg.Decode(bufio.NewReaderSize(f, 1<<16))
	if err != nil {
		return nil, err
	}
	img, err := drawPhotoCard(src, orientation, strip)
	src = nil
	if err != nil {
		return nil, err
	}
	b, err := encodePhotoCard(img)
	img = nil
	debug.FreeOSMemory()
	return b, err
}

// --- the handler and its cache ----------------------------------------------------

func (a *App) photoCardGet(hash string) []byte {
	a.cards.mu.Lock()
	defer a.cards.mu.Unlock()
	return a.cards.photo[hash]
}

// photoCardPut keeps the card; only the last photoCardKeep are held.
func (a *App) photoCardPut(hash string, b []byte) {
	a.cards.mu.Lock()
	defer a.cards.mu.Unlock()
	if a.cards.photo == nil {
		a.cards.photo = map[string][]byte{}
	}
	if _, have := a.cards.photo[hash]; !have {
		a.cards.photoOrder = append(a.cards.photoOrder, hash)
	}
	a.cards.photo[hash] = b
	for len(a.cards.photoOrder) > photoCardKeep {
		delete(a.cards.photo, a.cards.photoOrder[0])
		a.cards.photoOrder = a.cards.photoOrder[1:]
	}
}

// photoCardJPEG answers GET /og/photo/<id>.jpg with the photo's current card;
// the ?v= hash is ignored. A photo a visitor cannot open is a 404, as is one
// that cannot be drawn. The route is not in visibilityOpen, so a private site
// closes it.
func (a *App) photoCardJPEG(w http.ResponseWriter, r *http.Request) {
	idStr, ok := strings.CutSuffix(r.PathValue("file"), ".jpg")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if !ok || err != nil {
		http.NotFound(w, r)
		return
	}
	post, rv, ok := a.visitorPhoto(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rel, o, err := a.photoCardSource(post.Photo)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	strip := photoStripOf(post, rv)
	hash := photoCardHash(post.Photo.SHA256, rel, strip)
	b := a.photoCardGet(hash)
	if b == nil {
		select {
		case photoCardSlot <- struct{}{}:
		case <-r.Context().Done():
			return
		}
		if b = a.photoCardGet(hash); b == nil {
			b, err = renderPhotoCard(a.mediaFile(rel), o, strip)
			if err == nil {
				a.photoCardPut(hash, b)
			}
		}
		<-photoCardSlot
		if err != nil {
			http.Error(w, "the card could not be drawn", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}

// truncateWords is truncate that cuts at a word: a name too long for the strip
// loses whole words from its end, and an ellipsis marks the cut.
func truncateWords(face font.Face, s string, max float64) string {
	if textWidth(face, s, 0) <= max {
		return s
	}
	words := strings.Fields(s)
	for n := len(words) - 1; n >= 1; n-- {
		if t := strings.Join(words[:n], " ") + "…"; textWidth(face, t, 0) <= max {
			return t
		}
	}
	return truncate(face, s, max, 0) // one word too long for the strip: cut inside it
}
