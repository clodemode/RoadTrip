package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The photo card, below the scenarios: what the drawing and the bytes hold.
// spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does.

// testPhoto is a drawn picture (never a real photograph): a sky-to-ground
// gradient with a few shapes, so a crop, a stretch or a turn is visible.
func testPhoto(w, h int, seed int64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(seed))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		c := color.RGBA{uint8(110 + 120*t), uint8(170 - 60*t), uint8(220 - 150*t), 255}
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	for i := 0; i < 5; i++ {
		cx, cy, r := rng.Intn(w), rng.Intn(h), w/10+rng.Intn(w/8+1)
		col := color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255}
		for y := max(0, cy-r); y < min(h, cy+r); y++ {
			for x := max(0, cx-r); x < min(w, cx+r); x++ {
				if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
					img.SetRGBA(x, y, col)
				}
			}
		}
	}
	return img
}

func jpegBytes(t testing.TB, img image.Image, q int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: q}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// putTestPhoto stores a generated photo as a post on the first leg of the
// app's active ride, its two files on the public media path.
func putTestPhoto(t *testing.T, app *App, w, h int, seed int64, title, body string) *Post {
	t.Helper()
	orig := jpegBytes(t, testPhoto(w, h, seed), 90)
	thumb := jpegBytes(t, testPhoto(w/2, h/2, seed), 82)
	sum := sha256.Sum256(orig)
	sha := hex.EncodeToString(sum[:])
	rel := "2026/11/" + sha[:16] + ".jpg"
	trel := "2026/11/" + sha[:16] + "_t.jpg"
	for n, b := range map[string][]byte{rel: orig, trel: thumb} {
		full := app.mediaFile(n)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ph := &Photo{Path: rel, ThumbPath: trel, TZ: "America/Chicago", Width: w, Height: h, SHA256: sha}
	if err := app.store.CreatePhoto(ph); err != nil {
		t.Fatal(err)
	}
	legs := mustLegs(t, app, app.activeSlug())
	p := &Post{LegID: legs[0].ID, Kind: "photo", Title: title, Body: body, TZ: "America/Chicago",
		OccurredAt: utcString(time.Now().Add(-time.Hour)), PhotoID: ph.ID, PinSource: pinNone}
	if err := app.store.CreatePost(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// lookDir is where a run that wants to be looked at writes its cards.
func lookDir(t *testing.T) string {
	d := os.Getenv("PHOTO_CARD_OUT")
	if d == "" {
		return ""
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPhotoCardForEveryShapeIs1200x630JPEGUnder300KBAndSteady(t *testing.T) {
	strip := photoStripFor("Alder to Elm", 2)
	for _, c := range []struct {
		name string
		w, h int
	}{{"landscape", 1600, 900}, {"portrait", 900, 1600}, {"square", 1000, 1000}, {"wide", 3000, 700}} {
		src := jpegBytes(t, testPhoto(c.w, c.h, 7), 90)
		img, _ := jpeg.Decode(bytes.NewReader(src))
		card, err := drawPhotoCard(img, 1, strip)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		b, err := encodePhotoCard(card)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
		if err != nil || cfg.Width != 1200 || cfg.Height != 630 {
			t.Errorf("%s: %dx%d (%v), want a 1200x630 JPEG", c.name, cfg.Width, cfg.Height, err)
		}
		if len(b) >= photoCardMaxBytes {
			t.Errorf("%s: %d bytes, want under %d", c.name, len(b), photoCardMaxBytes)
		}
		again, _ := drawPhotoCard(img, 1, strip)
		b2, _ := encodePhotoCard(again)
		if !bytes.Equal(b, b2) {
			t.Errorf("%s: the same inputs gave different bytes", c.name)
		}
		if d := lookDir(t); d != "" {
			if err := os.WriteFile(filepath.Join(d, "photo-card-"+c.name+".jpg"), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("%s: %d bytes", c.name, len(b))
	}
}

func TestPhotoCardStepsDownQualityForANoisyPicture(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	img := image.NewRGBA(image.Rect(0, 0, 1200, 630))
	for i := range img.Pix {
		img.Pix[i] = uint8(rng.Intn(256))
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	b, err := encodePhotoCard(img)
	if err == nil && len(b) >= photoCardMaxBytes {
		t.Fatalf("%d bytes and no error", len(b))
	}
	// plain noise may be impossible; a card that fits must be under the limit, and the loop must end
	if err == nil {
		t.Logf("noise fits at %d bytes", len(b))
	}
}

func TestPhotoCardFillCoversTheCardWithoutStretchingAndTurnsAnUprightPicture(t *testing.T) {
	// top half red, bottom half blue: orientation 6 turns the top to the right
	src := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 1600; x++ {
			if y < 450 {
				src.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				src.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
			}
		}
	}
	card := photoCardFill(src, 6)
	if r := card.RGBAAt(1100, 300); r.R < 200 || r.B > 60 {
		t.Errorf("right side is %v, want red (the top of the picture)", r)
	}
	if l := card.RGBAAt(100, 300); l.B < 200 || l.R > 60 {
		t.Errorf("left side is %v, want blue", l)
	}
	// no stretch: a square picture with a centred disc keeps a round disc
	sq := image.NewRGBA(image.Rect(0, 0, 1000, 1000))
	for i := range sq.Pix {
		sq.Pix[i] = 255
	}
	for y := 0; y < 1000; y++ {
		for x := 0; x < 1000; x++ {
			if (x-500)*(x-500)+(y-500)*(y-500) < 200*200 {
				sq.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}
	c := photoCardFill(sq, 1)
	// the crop keeps 1000x525 of the square about its centre at scale 1.2: the disc is 480 wide and 480 tall, clipped to 630 rows
	dark := func(x, y int) bool { return c.RGBAAt(x, y).R < 128 }
	if !dark(600, 315) || dark(600+250, 315) || dark(600-250, 315) || !dark(600+230, 315) {
		t.Error("the disc is not 480 px wide about the centre: the picture was stretched or off centre")
	}
}

func TestPhotoCardBytesHoldNoEXIFOrOtherMetadata(t *testing.T) {
	src := jpegWithGPS(t, 6)
	if !readEXIF(bytes.NewReader(src)).hasGPS || !bytes.Contains(src, []byte("Exif")) {
		t.Fatal("the source does not carry EXIF: the test proves nothing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "src.jpg")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := renderPhotoCard(path, 6, photoStripFor("Alder to Elm", 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"Exif", "http://ns.adobe.com", "ICC_PROFILE", "GPS"} {
		if bytes.Contains(b, []byte(bad)) {
			t.Errorf("the card holds %q", bad)
		}
	}
	// walk the segments: only JFIF (APP0) and the coding tables may precede the scan
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			t.Fatalf("lost the marker walk at %d", i)
		}
		m := b[i+1]
		if m == 0xDA {
			break
		}
		if m >= 0xE1 && m <= 0xEF || m == 0xFE {
			t.Errorf("the card has a metadata segment, marker %#x", m)
		}
		i += 2 + int(binary.BigEndian.Uint16(b[i+2:]))
	}
}

func TestPhotoCardRefusesAnOversizedSourceWithoutAllocatingIt(t *testing.T) {
	small := jpegBytes(t, testPhoto(64, 64, 1), 80)
	// patch the frame header to claim 9000 x 9000 (81 MP)
	i := bytes.Index(small, []byte{0xFF, 0xC0})
	if i < 0 {
		t.Fatal("no baseline frame header")
	}
	binary.BigEndian.PutUint16(small[i+5:], 9000)
	binary.BigEndian.PutUint16(small[i+7:], 9000)
	path := filepath.Join(t.TempDir(), "big.jpg")
	if err := os.WriteFile(path, small, 0o644); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := renderPhotoCard(path, 1, photoStripFor("x", 1))
	runtime.ReadMemStats(&after)
	if err != errPhotoCardTooBig {
		t.Fatalf("err = %v, want %v", err, errPhotoCardTooBig)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
		t.Errorf("refusing allocated %d bytes", grew)
	}
}

func TestPhotoCardReadsTheThumbnailWhenTheOriginalIsTooBigOrUnreadable(t *testing.T) {
	app := tokenApp(t, testToken)
	big := jpegBytes(t, testPhoto(64, 64, 1), 80)
	i := bytes.Index(big, []byte{0xFF, 0xC0})
	binary.BigEndian.PutUint16(big[i+5:], 9000)
	binary.BigEndian.PutUint16(big[i+7:], 9000)
	thumb := jpegBytes(t, testPhoto(400, 300, 2), 80)
	if err := os.MkdirAll(app.mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, b := range map[string][]byte{"o.jpg": big, "o_t.jpg": thumb} {
		if err := os.WriteFile(app.mediaFile(n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rel, o, err := app.photoCardSource(&Photo{Path: "o.jpg", ThumbPath: "o_t.jpg"})
	if err != nil || rel != "o_t.jpg" || o != 1 {
		t.Errorf("got %q %d %v, want the thumbnail", rel, o, err)
	}
	if _, _, err := app.photoCardSource(&Photo{Path: "nope.jpg", ThumbPath: "nope_t.jpg"}); err == nil {
		t.Error("a photo with no file gave a source")
	}
}

func TestPhotoCardLongNameIsCutAndStaysInsideTheCard(t *testing.T) {
	img, _ := jpeg.Decode(bytes.NewReader(jpegBytes(t, testPhoto(1200, 630, 5), 90)))
	long := strings.Repeat("The very long trip name that goes on and on ", 6)
	with, err := drawPhotoCard(img, 1, photoStripFor(long, 3))
	if err != nil {
		t.Fatal(err)
	}
	without, _ := drawPhotoCard(img, 1, photoStripFor("", 3))
	differs := false
	for y := cardH - photoStripH + 3; y < cardH; y++ {
		for x := 0; x < cardW; x++ {
			if with.RGBAAt(x, y) != without.RGBAAt(x, y) {
				differs = true
				if x >= cardW-40 {
					t.Fatalf("text reaches x=%d, inside the right margin", x)
				}
			}
		}
	}
	if !differs {
		t.Error("the long name drew nothing")
	}
	if d := lookDir(t); d != "" {
		b, _ := encodePhotoCard(with)
		_ = os.WriteFile(filepath.Join(d, "photo-card-longname.jpg"), b, 0o644)
	}
}

func TestPhotoCardStripNeverHasAClockTimeOrAPlace(t *testing.T) {
	for day := 0; day <= 3; day++ {
		s := photoStripFor("Alder to Elm", day)
		for _, v := range []string{s.Name, s.Line, s.alt()} {
			if regexp.MustCompile(`\d{1,2}:\d{2}|\b(AM|PM)\b|\bnear\b`).MatchString(v) {
				t.Errorf("day %d: %q holds a time or a place word", day, v)
			}
		}
	}
	if s := photoStripFor("T", 0); s.Line != "" || s.alt() != "A photo from T" {
		t.Errorf("no leg: %+v %q", s, s.alt())
	}
}

func TestPhotoCardHashFollowsThePhotoTheRenditionAndTheWords(t *testing.T) {
	s := photoStripFor("A", 1)
	h := photoCardHash("aa", "x.jpg", s)
	if photoCardHash("aa", "x.jpg", s) != h {
		t.Error("not steady")
	}
	for _, o := range []string{photoCardHash("ab", "x.jpg", s), photoCardHash("aa", "y.jpg", s),
		photoCardHash("aa", "x.jpg", photoStripFor("A", 2)), photoCardHash("aa", "x.jpg", photoStripFor("B", 1))} {
		if o == h {
			t.Error("a changed input kept the hash")
		}
	}
}

func TestPhotoCardCacheIsBoundedAndRaceFree(t *testing.T) {
	var a App
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a.photoCardPut(fmt.Sprint("h", i), []byte{byte(i)})
			_ = a.photoCardGet(fmt.Sprint("h", i/2))
		}(i)
	}
	wg.Wait()
	if n := len(a.cards.photo); n > photoCardKeep || len(a.cards.photoOrder) != n {
		t.Errorf("holds %d cards (order %d), want at most %d", n, len(a.cards.photoOrder), photoCardKeep)
	}
}

func TestCutWordsCutsAtAWordWithAnEllipsis(t *testing.T) {
	if got := cutWords("  one   two\nthree ", 50); got != "one two three" {
		t.Errorf("got %q", got)
	}
	got := cutWords("alpha beta gamma delta epsilon", 14)
	if got != "alpha beta…" || len([]rune(got)) > 14 {
		t.Errorf("got %q", got)
	}
	if got := cutWords(strings.Repeat("x", 30), 10); len([]rune(got)) > 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("a word longer than the limit: %q", got)
	}
}

// photoPaths are the two public files of a post's photo.
func photoPaths(app *App, p *Post) []string {
	q, err := app.store.Post(p.ID)
	if err != nil || q.Photo == nil {
		return nil
	}
	return []string{q.Photo.Path, q.Photo.ThumbPath}
}

func TestPhotoPageTextWithNoDayLineReadsPhotoAndDoesNotPanic(t *testing.T) {
	title, desc := photoPageText(photoStripFor("Alder to Elm", 0), "", "")
	if title != "Photo" || desc != "A photo from Alder to Elm, posted along the way." {
		t.Errorf("title %q, description %q", title, desc)
	}
	title, _ = photoPageText(photoStripFor("Alder to Elm", 2), "", "")
	if title != "Photo from day 2" {
		t.Errorf("title %q", title)
	}
	if title, desc = photoPageText(photoStripFor("T", 0), "Pie", "A note."); title != "Pie" || desc != "A note." {
		t.Errorf("own words: %q %q", title, desc)
	}
}
