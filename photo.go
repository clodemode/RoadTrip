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
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/draw"
)

// Photo intake. JPEG only — there is no pure-Go HEIC decoder, and this ships
// on scratch with no libheif to shell out to. iPhones can be told to shoot
// JPEG (Settings → Camera → Formats → Most Compatible) or to share as JPEG.

const maxUploadBytes = 25 << 20 // 25 MB
const thumbWidth = 800

// maxMegapixels bounds the decode. A JPEG decodes to a YCbCr plane set of
// 1.5 bytes/pixel at 4:2:0 (what phones write) or 3 bytes/pixel at 4:4:4, so
// the decoded image — the one thing this path cannot stream — is the memory
// peak: measured 2026-09-02 in Docker, a 48.8 MP 4:2:0 JPEG (8064×6048, the
// iPhone 15/16 Pro full-resolution export) peaks the process at ~110 MB with
// the upload on disk, against ~309 MB when the upload and multipart copies
// were also in memory (the shape that OOM-killed the 256 MB VM). 60 MP admits
// every phone shipping today and refuses medium-format and stitched
// panoramas, which is what the message says to do about it.
const maxMegapixels = 60

// exifReadBytes bounds how far readEXIF will look. The APP1 EXIF segment is
// at most 64 KB and phones write it first; 2 MB covers an ICC/XMP segment
// ahead of it without walking a 20 MB file that has no EXIF at all.
const exifReadBytes = 2 << 20

// ErrNotJPEG is the message the phone sees. It says what to do, not just no.
var ErrNotJPEG = errors.New("only JPEG photos are accepted — HEIC is not supported. " +
	"On iPhone set Settings → Camera → Formats → Most Compatible, or share the photo as JPEG.")

func isJPEG(b []byte) bool {
	return len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF
}

// exifInfo is what we keep from the camera: when (as the camera's wall clock,
// zone-less), where, and which way up.
type exifInfo struct {
	taken       time.Time // zone-less wall clock; zero if absent
	lat, lng    float64
	hasGPS      bool
	orientation int
}

func readEXIF(r io.Reader) exifInfo {
	info := exifInfo{orientation: 1}
	x, err := exif.Decode(bufio.NewReader(io.LimitReader(r, exifReadBytes)))
	if err != nil || x == nil {
		return info
	}
	if tag, err := x.Get(exif.DateTimeOriginal); err == nil {
		if s, err := tag.StringVal(); err == nil {
			// EXIF DateTimeOriginal has no zone. It is parsed as a wall clock
			// here and pinned to the zone the author picked on the form.
			if t, err := time.Parse("2006:01:02 15:04:05", s); err == nil {
				info.taken = t
			}
		}
	}
	if lat, lng, err := x.LatLong(); err == nil && !(lat == 0 && lng == 0) {
		info.lat, info.lng, info.hasGPS = lat, lng, true
	}
	if tag, err := x.Get(exif.Orientation); err == nil {
		if o, err := tag.Int(0); err == nil && o >= 1 && o <= 8 {
			info.orientation = o
		}
	}
	return info
}

// orient applies an EXIF orientation (1–8) to an image. It runs on the
// thumbnail, not the original — a 12 MP rotate would be a 48 MB RGBA on a
// 256 MB machine for no reason; the original keeps its EXIF orientation (the
// one tag the scrub in exif_strip.go preserves) and browsers honour it.
func orient(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	swap := o >= 5
	dw, dh := w, h
	if swap {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// makeThumb scales the decoded image so that, AFTER orientation is applied,
// it is thumbWidth wide (never upscaled), then orients it.
//
// Not draw.BiLinear straight from the full image: x/image/draw's Kernel
// scalers allocate a float64 working buffer of source-width × target-height
// × 32 bytes — 155 MB for an 8064-wide photo shrunk to 600 rows, which was
// the real memory peak of a 48 MP post, twice the decoded image itself
// (measured 2026-09-02: VmHWM 240 MB with the upload already on disk). So:
// an integer box average first (no working buffer, one pass over the
// planes) down to under 2× the target, then ApproxBiLinear for the last
// ≤2× step, which samples in place.
func makeThumb(src image.Image, o int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	// The dimension that becomes the width after rotation.
	wAfter := w
	if o >= 5 {
		wAfter = h
	}
	if k := wAfter / thumbWidth; k >= 2 {
		src = boxDown(src, k)
		b = src.Bounds()
		w, h = b.Dx(), b.Dy()
		if o >= 5 {
			wAfter = h
		} else {
			wAfter = w
		}
	}
	scale := 1.0
	if wAfter > thumbWidth {
		scale = float64(thumbWidth) / float64(wAfter)
	}
	tw := int(float64(w)*scale + 0.5)
	th := int(float64(h)*scale + 0.5)
	if tw < 1 {
		tw = 1
	}
	if th < 1 {
		th = 1
	}
	small := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.ApproxBiLinear.Scale(small, small.Bounds(), src, b, draw.Src, nil)
	return orient(small, o)
}

// boxDown shrinks src by an integer factor k, each output pixel the mean of
// a k×k block. It reads the YCbCr planes directly (what image/jpeg decodes
// a colour photo to) and falls back to the generic At() for anything else.
// The output is at most len(src)/k² — the only allocation.
func boxDown(src image.Image, k int) *image.RGBA {
	b := src.Bounds()
	dw, dh := b.Dx()/k, b.Dy()/k
	if dw < 1 || dh < 1 {
		return toRGBA(src)
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	n := uint32(k * k)
	yc, isYCbCr := src.(*image.YCbCr)
	for dy := 0; dy < dh; dy++ {
		for dx := 0; dx < dw; dx++ {
			var r, g, bl uint32
			for y := 0; y < k; y++ {
				sy := b.Min.Y + dy*k + y
				for x := 0; x < k; x++ {
					sx := b.Min.X + dx*k + x
					if isYCbCr {
						c := yc.YCbCrAt(sx, sy)
						pr, pg, pb := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
						r += uint32(pr)
						g += uint32(pg)
						bl += uint32(pb)
					} else {
						pr, pg, pb, _ := src.At(sx, sy).RGBA()
						r += pr >> 8
						g += pg >> 8
						bl += pb >> 8
					}
				}
			}
			i := dst.PixOffset(dx, dy)
			dst.Pix[i] = uint8(r / n)
			dst.Pix[i+1] = uint8(g / n)
			dst.Pix[i+2] = uint8(bl / n)
			dst.Pix[i+3] = 0xff
		}
	}
	return dst
}

func toRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Copy(dst, image.Point{}, src, b, draw.Src, nil)
	return dst
}

// savePhoto validates, hashes, decodes, thumbnails and stores an upload.
// loc is the zone the author picked on the form: the EXIF wall clock is
// read in that zone and stored as UTC alongside the zone name.
//
// Memory discipline — this is the one request that can kill the machine
// (2026-09-02 00:49Z: a phone photo OOM-killed the 256 MB VM, anon-rss 158 MB,
// four posts lost). The upload never lives in memory: it streams to a temp
// file beside the media dir while being hashed and size-capped; EXIF and
// image.DecodeConfig read from that file; the pixel count is checked against
// maxMegapixels BEFORE the one jpeg.Decode; the thumbnail is derived from
// that single decode; and both images are dropped before the DB write, with
// the heap handed back to the OS so the VM's RAM snapshot under suspend
// stays small.
func (a *App) savePhoto(r io.Reader, loc *time.Location) (*Photo, error) {
	if err := os.MkdirAll(a.mediaDir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(a.mediaDir, ".upload-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the file has been renamed into place
	defer tmp.Close()

	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, maxUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if size > maxUploadBytes {
		return nil, fmt.Errorf("photo is larger than %d MB", maxUploadBytes>>20)
	}
	var magic [4]byte
	if _, err := tmp.ReadAt(magic[:], 0); err != nil || !isJPEG(magic[:]) {
		return nil, ErrNotJPEG
	}
	sha := hex.EncodeToString(h.Sum(nil))
	if p, err := a.store.PhotoBySHA(sha); err == nil {
		return p, nil // same bytes already on the volume: reuse, do not duplicate
	}

	info := readEXIF(io.NewSectionReader(tmp, 0, size))

	// Header only — width and height without touching a pixel.
	cfg, err := jpeg.DecodeConfig(io.NewSectionReader(tmp, 0, size))
	if err != nil {
		return nil, fmt.Errorf("could not read the JPEG header: %w", err)
	}
	mp := float64(cfg.Width) * float64(cfg.Height) / 1e6
	if mp > maxMegapixels {
		return nil, fmt.Errorf("photo is %.0f MP (%d×%d); the limit is %d MP. "+
			"The phone's HEIF→JPEG export at Most Compatible is fine, or share a smaller copy.",
			mp, cfg.Width, cfg.Height, maxMegapixels)
	}

	// The one decode. Everything below derives from img and drops it.
	img, err := jpeg.Decode(bufio.NewReaderSize(io.NewSectionReader(tmp, 0, size), 1<<16))
	if err != nil {
		return nil, fmt.Errorf("could not decode JPEG: %w", err)
	}
	thumb := makeThumb(img, info.orientation)
	img = nil
	var tb bytes.Buffer
	if err := jpeg.Encode(&tb, thumb, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	tw, th := thumb.Bounds().Dx(), thumb.Bounds().Dy()
	thumb = nil
	// The decoded planes are garbage now; give the pages back to the kernel
	// before the DB write rather than at the next GC cycle.
	debug.FreeOSMemory()

	// Post-orientation dimensions — what a viewer sees.
	w, h2 := cfg.Width, cfg.Height
	if info.orientation >= 5 {
		w, h2 = h2, w
	}

	p := &Photo{SHA256: sha, TZ: loc.String(), Width: w, Height: h2}
	when := time.Now()
	if !info.taken.IsZero() {
		t := time.Date(info.taken.Year(), info.taken.Month(), info.taken.Day(),
			info.taken.Hour(), info.taken.Minute(), info.taken.Second(), 0, loc)
		p.TakenAt = utcString(t)
		when = t
	}
	if info.hasGPS {
		lat, lng := info.lat, info.lng
		p.Lat, p.Lng = &lat, &lng
	}

	// media/YYYY/MM/<sha-prefix>.jpg — content-addressed, so the path can be
	// cached forever and the same photo can never be stored twice.
	rel := filepath.Join(when.In(loc).Format("2006"), when.In(loc).Format("01"))
	if err := os.MkdirAll(filepath.Join(a.mediaDir, rel), 0o755); err != nil {
		return nil, err
	}
	p.Path = filepath.ToSlash(filepath.Join(rel, sha[:16]+".jpg"))
	p.ThumbPath = filepath.ToSlash(filepath.Join(rel, sha[:16]+"_t.jpg"))
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	// The bytes that get served lose their EXIF location before they are ever
	// under a public path. The photo ROW above keeps the exact point — a post
	// can still be re-pinned from it — but /media/ is public, immutable and
	// cached forever, so a coordinate that reaches it cannot be taken back.
	// See exif_strip.go for what survives (orientation, DateTimeOriginal) and
	// why the pixels are untouched.
	if _, err := scrubJPEGInPlace(tmpName); err != nil {
		return nil, fmt.Errorf("could not remove the photo's EXIF location: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return nil, err
	}
	// Same filesystem (the temp file sits in the media dir), so this is a
	// rename, not a copy.
	if err := os.Rename(tmpName, filepath.Join(a.mediaDir, p.Path)); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(a.mediaDir, p.ThumbPath), tb.Bytes(), 0o644); err != nil {
		return nil, err
	}
	if err := a.store.CreatePhoto(p); err != nil {
		return nil, err
	}
	log.Printf("photo %s: %d×%d %.1f MP %.1f MB, orientation %d, thumb %d×%d, exif gps=%v",
		sha[:16], cfg.Width, cfg.Height, mp, float64(size)/(1<<20), info.orientation, tw, th, info.hasGPS)
	return p, nil
}
