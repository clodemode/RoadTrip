package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The bytes under /media/ are the one privacy decision that cannot be taken
// back — the path is public, immutable and cached forever — so these tests
// hold the line at the file, not at the map pin: a stored original carries no
// coordinates, keeps the two tags the site actually uses, and a file that is
// already clean is never rewritten.

const testDateTimeOriginal = "2026:09:05 07:15:22"

// jpegNoEXIF is a real, decodable JPEG with no APP1 at all: SOI, the JFIF
// APP0 image/jpeg writes, and the scan.
func jpegNoEXIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 31), uint8(y * 31), 0x40, 0xff})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	b := buf.Bytes()
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		t.Fatalf("fixture is not a JPEG")
	}
	if bytes.Contains(b, []byte("Exif")) {
		t.Fatalf("fixture already carries EXIF")
	}
	return b
}

// exifAPP1WithGPS hand-builds an APP1 EXIF payload the way a phone writes one:
// little-endian TIFF, IFD0 with Orientation and pointers to both an Exif
// sub-IFD (DateTimeOriginal) and a GPS IFD (a latitude, N). Written by hand
// with encoding/binary rather than by a library, so the fixture cannot drift
// with whatever the parser under test happens to accept.
func exifAPP1WithGPS(orientation int, dto string) []byte {
	bo := binary.LittleEndian
	// Layout, offsets from the TIFF header:
	//    8 IFD0        3 entries -> 2 + 36 + 4 = 42 bytes, ends at 50
	//   50 Exif IFD    1 entry   -> 2 + 12 + 4 = 18 bytes, ends at 68
	//   68 GPS IFD     4 entries -> 2 + 48 + 4 = 54 bytes, ends at 122
	//  122 DateTimeOriginal, 20 bytes, ends at 142
	//  142 GPSLatitude, 3 rationals, 24 bytes, ends at 166
	//  166 GPSLongitude, 3 rationals, 24 bytes, ends at 190
	const (
		exifIFD = 50
		gpsIFD  = 68
		dtoAt   = 122
		latAt   = 142
		lngAt   = 166
		total   = 190
	)
	t := make([]byte, total)
	t[0], t[1] = 'I', 'I'
	bo.PutUint16(t[2:], 42)
	bo.PutUint32(t[4:], 8)

	entry := func(at int, tag, typ uint16, count uint32, fill func([]byte)) {
		bo.PutUint16(t[at:], tag)
		bo.PutUint16(t[at+2:], typ)
		bo.PutUint32(t[at+4:], count)
		fill(t[at+8 : at+12])
	}

	bo.PutUint16(t[8:], 3)
	entry(10, 0x0112, 3, 1, func(v []byte) { bo.PutUint16(v, uint16(orientation)) }) // Orientation
	entry(22, 0x8769, 4, 1, func(v []byte) { bo.PutUint32(v, exifIFD) })             // ExifIFDPointer
	entry(34, 0x8825, 4, 1, func(v []byte) { bo.PutUint32(v, gpsIFD) })              // GPSInfoIFDPointer
	bo.PutUint32(t[46:], 0)

	bo.PutUint16(t[exifIFD:], 1)
	entry(exifIFD+2, 0x9003, 2, 20, func(v []byte) { bo.PutUint32(v, dtoAt) }) // DateTimeOriginal
	bo.PutUint32(t[exifIFD+14:], 0)

	bo.PutUint16(t[gpsIFD:], 4)
	entry(gpsIFD+2, 0x0001, 2, 2, func(v []byte) { copy(v, "N\x00") }) // GPSLatitudeRef
	entry(gpsIFD+14, 0x0002, 5, 3, func(v []byte) { bo.PutUint32(v, latAt) })
	entry(gpsIFD+26, 0x0003, 2, 2, func(v []byte) { copy(v, "W\x00") }) // GPSLongitudeRef
	entry(gpsIFD+38, 0x0004, 5, 3, func(v []byte) { bo.PutUint32(v, lngAt) })
	bo.PutUint32(t[gpsIFD+50:], 0)

	copy(t[dtoAt:], dto) // 20 bytes with the trailing NUL already zero

	// 30° 15' 30.00" N, 97° 55' 12.00" W — Hudson Bend, near enough.
	rational := func(at int, rs [][2]uint32) {
		for i, r := range rs {
			bo.PutUint32(t[at+i*8:], r[0])
			bo.PutUint32(t[at+i*8+4:], r[1])
		}
	}
	rational(latAt, [][2]uint32{{30, 1}, {15, 1}, {3000, 100}})
	rational(lngAt, [][2]uint32{{97, 1}, {55, 1}, {1200, 100}})

	out := append([]byte{}, exifPrefix...)
	return append(out, t...)
}

// jpegWithGPS is the fixture that matters: a decodable JPEG whose APP1 holds
// an orientation, a DateTimeOriginal and a latitude.
func jpegWithGPS(t *testing.T, orientation int) []byte {
	t.Helper()
	base := jpegNoEXIF(t)
	payload := exifAPP1WithGPS(orientation, testDateTimeOriginal)
	seg := []byte{0xFF, markerAPP1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	out := append([]byte{}, base[:2]...) // SOI
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, base[2:]...)
}

func scrub(t *testing.T, in []byte) ([]byte, bool) {
	t.Helper()
	var out bytes.Buffer
	changed, err := scrubJPEG(&out, bytes.NewReader(in))
	if err != nil {
		t.Fatalf("scrubJPEG: %v", err)
	}
	return out.Bytes(), changed
}

func TestFixtureActuallyCarriesGPS(t *testing.T) {
	// Without this the whole file could pass against a fixture that never had
	// a location in it.
	in := jpegWithGPS(t, 6)
	info := readEXIF(bytes.NewReader(in))
	if !info.hasGPS {
		t.Fatalf("fixture has no GPS — the test proves nothing")
	}
	if info.orientation != 6 {
		t.Errorf("fixture orientation = %d, want 6", info.orientation)
	}
	if info.taken.Format("2006:01:02 15:04:05") != testDateTimeOriginal {
		t.Errorf("fixture DateTimeOriginal = %v, want %s", info.taken, testDateTimeOriginal)
	}
}

func TestScrubDropsGPSAndKeepsTimeAndOrientation(t *testing.T) {
	in := jpegWithGPS(t, 6)
	out, changed := scrub(t, in)
	if !changed {
		t.Fatalf("scrubJPEG reported no change on a file with a GPS IFD")
	}

	info := readEXIF(bytes.NewReader(out))
	if info.hasGPS {
		t.Errorf("the scrubbed original still reports GPS (%.4f, %.4f)", info.lat, info.lng)
	}
	if info.orientation != 6 {
		t.Errorf("orientation = %d, want 6 — browsers rotate the original by this", info.orientation)
	}
	if got := info.taken.Format("2006:01:02 15:04:05"); got != testDateTimeOriginal {
		t.Errorf("DateTimeOriginal = %s, want %s", got, testDateTimeOriginal)
	}

	// Nothing that looks like a coordinate is left anywhere in the bytes: the
	// point of rebuilding rather than unlinking the IFD.
	if bytes.Contains(out, []byte("GPS")) {
		t.Errorf("the scrubbed file still contains the string GPS")
	}
	// The latitude's seconds rational (3000/100), as the fixture's
	// little-endian TIFF wrote it. Unreferenced bytes are still published
	// bytes, so the check is on the file, not on what a parser reports.
	if i := bytes.Index(out, []byte{0xB8, 0x0B, 0x00, 0x00, 0x64, 0x00, 0x00, 0x00}); i >= 0 {
		t.Errorf("the GPS latitude seconds rational survives at offset %d", i)
	}
	if len(out) >= len(in) {
		t.Errorf("scrubbed file is %d bytes, was %d — it should shrink", len(out), len(in))
	}
}

func TestScrubbedFileStillDecodes(t *testing.T) {
	in := jpegWithGPS(t, 6)
	want, err := jpeg.DecodeConfig(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("fixture DecodeConfig: %v", err)
	}
	out, _ := scrub(t, in)
	got, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("scrubbed DecodeConfig: %v", err)
	}
	if got.Width != want.Width || got.Height != want.Height {
		t.Errorf("scrubbed dimensions %d×%d, want %d×%d", got.Width, got.Height, want.Width, want.Height)
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Errorf("scrubbed file does not decode: %v", err)
	}

	// The pixels are copied through, not re-encoded: the scan is identical.
	sos := bytes.Index(in, []byte{0xFF, markerSOS})
	sosOut := bytes.Index(out, []byte{0xFF, markerSOS})
	if sos < 0 || sosOut < 0 {
		t.Fatalf("no SOS marker found")
	}
	if !bytes.Equal(in[sos:], out[sosOut:]) {
		t.Errorf("the entropy-coded scan changed — this must not re-encode")
	}
}

func TestScrubIsIdempotent(t *testing.T) {
	once, _ := scrub(t, jpegWithGPS(t, 6))
	twice, changed := scrub(t, once)
	if changed {
		t.Errorf("a second pass reported a change — the sweep would rewrite clean files forever")
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("second pass differs: %d bytes vs %d", len(twice), len(once))
	}
}

func TestScrubPassesThroughAJPEGWithNoEXIF(t *testing.T) {
	in := jpegNoEXIF(t)
	out, changed := scrub(t, in)
	if changed {
		t.Errorf("a file with no APP1 was reported as changed")
	}
	if !bytes.Equal(in, out) {
		t.Errorf("a file with no APP1 came back different: %d bytes vs %d", len(out), len(in))
	}
}

func TestScrubDropsAnEXIFBlockWithNothingWorthKeeping(t *testing.T) {
	// Orientation 1 and no DateTimeOriginal: the whole segment goes rather
	// than being written empty.
	in := jpegWithGPS(t, 1)
	// Blank the DateTimeOriginal in the fixture's APP1.
	i := bytes.Index(in, []byte(testDateTimeOriginal))
	if i < 0 {
		t.Fatalf("fixture has no DateTimeOriginal to blank")
	}
	for j := 0; j < len(testDateTimeOriginal); j++ {
		in[i+j] = 0
	}
	out, changed := scrub(t, in)
	if !changed {
		t.Fatalf("no change reported")
	}
	if bytes.Contains(out, exifPrefix) {
		t.Errorf("an APP1 survived with nothing worth keeping in it")
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(out)); err != nil {
		t.Errorf("DecodeConfig after dropping APP1: %v", err)
	}
}

func TestScrubDropsANonEXIFAPP1(t *testing.T) {
	// XMP rides in APP1 too, and carries its own copy of the coordinates.
	base := jpegNoEXIF(t)
	xmp := append([]byte("http://ns.adobe.com/xap/1.0/\x00"),
		[]byte(`<x:xmpmeta><exif:GPSLatitude>30,15.5N</exif:GPSLatitude></x:xmpmeta>`)...)
	seg := []byte{0xFF, markerAPP1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(xmp)+2))
	in := append([]byte{}, base[:2]...)
	in = append(in, seg...)
	in = append(in, xmp...)
	in = append(in, base[2:]...)

	out, changed := scrub(t, in)
	if !changed {
		t.Fatalf("an XMP APP1 was not reported as a change")
	}
	if bytes.Contains(out, []byte("GPSLatitude")) {
		t.Errorf("the XMP coordinates survived")
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(out)); err != nil {
		t.Errorf("DecodeConfig after dropping XMP: %v", err)
	}
}

func TestScrubJPEGInPlaceLeavesACleanFileUntouched(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clean.jpg")
	want := jpegNoEXIF(t)
	if err := os.WriteFile(p, want, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := scrubJPEGInPlace(p)
	if err != nil {
		t.Fatalf("scrubJPEGInPlace: %v", err)
	}
	if changed {
		t.Errorf("a clean file was reported as rewritten")
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("a clean file was rewritten — its mtime moved")
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("a clean file changed on disk")
	}
}

// --- the one-shot boot sweep ------------------------------------------------

func TestSweepMediaGPSRewritesOnlyWhatCarriesGPS(t *testing.T) {
	app := testApp(t)
	dir := filepath.Join(app.mediaDir, "2026", "09")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	dirty := jpegWithGPS(t, 6)
	clean := jpegNoEXIF(t)
	write := func(name string, b []byte) string {
		rel := "2026/09/" + name
		if err := os.WriteFile(filepath.Join(app.mediaDir, "2026", "09", name), b, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := app.store.CreatePhoto(&Photo{
			Path: rel, ThumbPath: "2026/09/t_" + name, SHA256: name, Width: 8, Height: 8,
		}); err != nil {
			t.Fatal(err)
		}
		return rel
	}
	dirtyRel := write("dirty.jpg", dirty)
	cleanRel := write("clean.jpg", clean)

	if err := sweepMediaGPS(app.store, app.mediaDir); err != nil {
		t.Fatalf("sweepMediaGPS: %v", err)
	}

	gotDirty, err := os.ReadFile(filepath.Join(app.mediaDir, filepath.FromSlash(dirtyRel)))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(gotDirty, dirty) {
		t.Errorf("the original with GPS was not rewritten")
	}
	if info := readEXIF(bytes.NewReader(gotDirty)); info.hasGPS {
		t.Errorf("the swept original still reports GPS")
	} else if info.orientation != 6 {
		t.Errorf("the sweep lost the orientation: got %d", info.orientation)
	}

	gotClean, err := os.ReadFile(filepath.Join(app.mediaDir, filepath.FromSlash(cleanRel)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotClean, clean) {
		t.Errorf("a clean original was rewritten by the sweep")
	}

	// Re-running it is a no-op, which is what makes a retried boot safe.
	if err := sweepMediaGPS(app.store, app.mediaDir); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	again, err := os.ReadFile(filepath.Join(app.mediaDir, filepath.FromSlash(dirtyRel)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, gotDirty) {
		t.Errorf("the second sweep changed a file it had already cleaned")
	}
}

func TestSweepMediaGPSToleratesAMissingFile(t *testing.T) {
	app := testApp(t)
	if err := app.store.CreatePhoto(&Photo{
		Path: "2026/09/gone.jpg", ThumbPath: "2026/09/gone_t.jpg", SHA256: "gone",
	}); err != nil {
		t.Fatal(err)
	}
	if err := sweepMediaGPS(app.store, app.mediaDir); err != nil {
		t.Errorf("a row whose file is gone should not fail the sweep: %v", err)
	}
}

// --- robots.txt --------------------------------------------------------------

func TestRobotsTxtIsOpenWhileTheSiteIsPrivate(t *testing.T) {
	app := testApp(t)
	app.sitePrivate = true

	req := httptest.NewRequest("GET", "/robots.txt", nil) // no cookie, no token
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /robots.txt = %d, want 200 (a crawler must not be redirected to a login)", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", ct)
	}
	body := w.Body.String()
	for _, want := range []string{"User-agent: *", "Disallow: /driver/", "Disallow: /media/", "Allow: /"} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("robots.txt is missing %q; got:\n%s", want, body)
		}
	}
}

func TestPrivateSiteStillRedirectsEverythingElse(t *testing.T) {
	// The robots.txt exemption must not have opened the gate generally.
	app := testApp(t)
	app.sitePrivate = true
	for _, path := range []string{"/", "/route.geojson", "/photos"} {
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 302 {
			t.Errorf("GET %s = %d while private, want 302", path, w.Code)
		}
	}
}
