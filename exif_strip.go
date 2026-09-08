package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// The served original carries no location.
//
// /media/ is public, immutable and cached forever, so the JPEG bytes are the
// one place a privacy decision cannot be taken back: the map pin can be
// fuzzed, re-pinned or hidden after the fact, but a file that was crawled with
// a GPS IFD in it is published for good. Until 2026-09-08 the original kept
// its whole EXIF block — every post with a photo would have published its
// exact capture point the moment the site opened, including the ones taken at
// home and at a host's house.
//
// So every original this app writes goes through scrubJPEG first: on upload
// (savePhoto) and, once per volume, over everything already stored
// (sweepMediaGPS, meta key media_gps_stripped_v1).
//
// HOW, AND WHY THIS WAY. Two shapes were available: surgically delete the
// GPSInfo pointer (tag 0x8825) from IFD0, or rebuild the APP1 from the two
// values worth keeping. This is the rebuild.
//
//   - Deleting the entry is not enough on its own: TIFF offsets are absolute
//     from the TIFF header, so removing an entry means rewriting every value
//     offset after it — and merely zeroing the pointer would leave the
//     latitude and longitude sitting in the file as unreferenced bytes, which
//     is exactly what must not be published.
//   - The rebuild cannot leak by omission. The output is generated from two
//     parsed values, so anything not named here — GPS, MakerNote (which
//     carries a serial number), lens, software, the EXIF thumbnail, XMP —
//     cannot survive, whether or not this code knows the tag exists.
//   - It is deterministic, which makes it idempotent: the canonical APP1 this
//     writes parses back to the same two values and rebuilds byte-for-byte, so
//     the boot sweep can be re-run and a clean file is never rewritten.
//
// KEPT: Orientation (0x0112), because browsers honour it and the original is
// never rotated, and DateTimeOriginal (0x9003), because the photo's time is
// read from it. Everything else in APP1 goes.
//
// NOT TOUCHED: the pixels. This is segment surgery on the JPEG stream, not a
// re-encode — SOF/DQT/DHT/SOS and the entropy-coded scan are copied through
// byte for byte, so there is no quality loss and no float working buffer on a
// 512 MB machine (see the OOM history in photo.go). Other application
// segments — APP0 JFIF, APP2 ICC — pass through untouched.
//
// The photo row's own lat/lng is deliberately left alone: the database keeps
// the exact point so a post can be re-pinned, and only the served bytes lose
// it.

// JPEG markers, all preceded by 0xFF.
const (
	markerSOI  = 0xD8 // start of image
	markerEOI  = 0xD9 // end of image
	markerSOS  = 0xDA // start of scan — entropy-coded data follows
	markerAPP1 = 0xE1 // EXIF lives here, and so does XMP
	markerTEM  = 0x01 // standalone, no length
)

// exifPrefix opens the APP1 payload of an EXIF segment; the TIFF header
// follows it immediately and every offset inside is relative to that.
var exifPrefix = []byte("Exif\x00\x00")

// The TIFF tags this cares about. Everything else is dropped, including
// GPSInfo (0x8825) — which is the point of the exercise.
const (
	tagOrientation      = 0x0112
	tagExifIFDPointer   = 0x8769
	tagDateTimeOriginal = 0x9003
)

// TIFF field types.
const (
	typASCII = 2
	typShort = 3
	typLong  = 4
)

// exifKeep is the whole of what survives an original's EXIF.
type exifKeep struct {
	orientation int    // 1–8; 1 when the file does not say
	dateTime    string // DateTimeOriginal, zone-less; "" when absent
}

// parseExifKeep walks a TIFF block (an APP1 payload with the "Exif\0\0"
// prefix already removed) far enough to find the two values worth keeping.
// It is deliberately incurious about everything else and never follows an
// offset it has not bounds-checked; a malformed block yields defaults rather
// than an error, because the caller's answer to "unparseable" is the same as
// its answer to "no EXIF": write no APP1 at all.
func parseExifKeep(t []byte) exifKeep {
	keep := exifKeep{orientation: 1}
	if len(t) < 8 {
		return keep
	}
	var bo binary.ByteOrder
	switch {
	case t[0] == 'I' && t[1] == 'I':
		bo = binary.LittleEndian
	case t[0] == 'M' && t[1] == 'M':
		bo = binary.BigEndian
	default:
		return keep
	}
	if bo.Uint16(t[2:4]) != 42 {
		return keep
	}
	exifOff := 0
	eachIFDEntry(t, bo, int(bo.Uint32(t[4:8])), func(tag, typ uint16, count uint32, val []byte) {
		switch tag {
		case tagOrientation:
			if typ == typShort && count >= 1 {
				// A SHORT is left-justified in the 4-byte value field.
				if o := int(bo.Uint16(val[0:2])); o >= 1 && o <= 8 {
					keep.orientation = o
				}
			}
		case tagExifIFDPointer:
			if typ == typLong && count == 1 {
				exifOff = int(bo.Uint32(val))
			}
		}
	})
	if exifOff > 0 {
		eachIFDEntry(t, bo, exifOff, func(tag, typ uint16, count uint32, val []byte) {
			if tag != tagDateTimeOriginal || typ != typASCII {
				return
			}
			n := int(count)
			if n < 2 || n > 64 {
				return
			}
			var b []byte
			if n <= 4 {
				b = val[:n]
			} else {
				off := int(bo.Uint32(val))
				if off < 8 || off+n > len(t) {
					return
				}
				b = t[off : off+n]
			}
			keep.dateTime = string(trimNUL(b))
		})
	}
	return keep
}

// eachIFDEntry calls fn for every entry of the IFD at off. Bounds are checked
// once, for the whole entry array, so fn always gets a full 12-byte entry.
func eachIFDEntry(t []byte, bo binary.ByteOrder, off int, fn func(tag, typ uint16, count uint32, val []byte)) {
	if off < 8 || off+2 > len(t) {
		return
	}
	n := int(bo.Uint16(t[off:]))
	if n == 0 || n > 512 || off+2+n*12 > len(t) {
		return
	}
	for i := 0; i < n; i++ {
		e := t[off+2+i*12 : off+2+(i+1)*12]
		fn(bo.Uint16(e[0:2]), bo.Uint16(e[2:4]), bo.Uint32(e[4:8]), e[8:12])
	}
}

func trimNUL(b []byte) []byte {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return b[:i]
	}
	return b
}

// buildExifAPP1 renders the canonical APP1 payload for what is kept, or nil
// when there is nothing worth keeping — in which case the segment is dropped
// entirely rather than written empty.
//
// The layout is fixed and big-endian, which is what makes the whole filter
// idempotent: the same two values always produce the same bytes.
//
//	0  MM 00 2A, IFD0 at 8
//	8  IFD0: Orientation [, ExifIFDPointer], next = 0
//	   Exif sub-IFD: DateTimeOriginal, next = 0
//	   the DateTimeOriginal string, NUL-terminated
//
// There is no IFD1, so the EXIF thumbnail goes too.
func buildExifAPP1(k exifKeep) []byte {
	dt := []byte(k.dateTime)
	// An ASCII value of 4 bytes or fewer would have to be stored inline; no
	// real DateTimeOriginal is that short, so drop it rather than carry a
	// second encoding path that would almost never run.
	if len(dt) < 4 || len(dt) > 63 {
		dt = nil
	}
	if k.orientation <= 1 && len(dt) == 0 {
		return nil
	}
	if k.orientation < 1 || k.orientation > 8 {
		k.orientation = 1
	}

	n0 := 1
	if len(dt) > 0 {
		n0 = 2
	}
	ifd0End := 8 + 2 + 12*n0 + 4 // IFD0: count, entries, next-IFD offset
	exifOff := ifd0End
	strOff := exifOff + 2 + 12 + 4
	total := ifd0End
	if len(dt) > 0 {
		total = strOff + len(dt) + 1
	}

	bo := binary.BigEndian
	t := make([]byte, total)
	t[0], t[1] = 'M', 'M'
	bo.PutUint16(t[2:], 42)
	bo.PutUint32(t[4:], 8)

	bo.PutUint16(t[8:], uint16(n0))
	e := 10
	putIFDEntry(t[e:], bo, tagOrientation, typShort, 1, func(v []byte) {
		bo.PutUint16(v, uint16(k.orientation))
	})
	e += 12
	if len(dt) > 0 {
		putIFDEntry(t[e:], bo, tagExifIFDPointer, typLong, 1, func(v []byte) {
			bo.PutUint32(v, uint32(exifOff))
		})
		e += 12
	}
	bo.PutUint32(t[e:], 0) // no IFD1

	if len(dt) > 0 {
		bo.PutUint16(t[exifOff:], 1)
		putIFDEntry(t[exifOff+2:], bo, tagDateTimeOriginal, typASCII, uint32(len(dt)+1), func(v []byte) {
			bo.PutUint32(v, uint32(strOff))
		})
		bo.PutUint32(t[exifOff+2+12:], 0) // no next IFD
		copy(t[strOff:], dt)              // the terminating NUL is already zero
	}

	out := make([]byte, 0, len(exifPrefix)+len(t))
	out = append(out, exifPrefix...)
	return append(out, t...)
}

func putIFDEntry(dst []byte, bo binary.ByteOrder, tag, typ uint16, count uint32, val func([]byte)) {
	bo.PutUint16(dst[0:], tag)
	bo.PutUint16(dst[2:], typ)
	bo.PutUint32(dst[4:], count)
	val(dst[8:12])
}

// scrubJPEG copies src to dst with every APP1 segment replaced by the
// canonical, location-free one (or dropped, when there is nothing to keep and
// for a non-EXIF APP1 such as XMP, which carries its own copy of the
// coordinates). Every other byte of the file is copied verbatim.
//
// It reports whether the output differs from the input at all, which is what
// lets a caller leave an already-clean file completely alone: no rewrite, no
// new mtime, byte-identical by not being touched.
//
// Memory: only one APP1 payload is ever held, and a JPEG segment is capped at
// 64 KB by the format. The scan data — the whole of a 15 MB photo — is
// streamed.
func scrubJPEG(dst io.Writer, src io.Reader) (bool, error) {
	br := bufio.NewReaderSize(src, 1<<16)
	var soi [2]byte
	if _, err := io.ReadFull(br, soi[:]); err != nil {
		return false, fmt.Errorf("jpeg: %w", err)
	}
	if soi[0] != 0xFF || soi[1] != markerSOI {
		return false, fmt.Errorf("jpeg: not a JPEG (no SOI)")
	}
	if _, err := dst.Write(soi[:]); err != nil {
		return false, err
	}

	changed := false
	for {
		// A marker is 0xFF followed by a non-0xFF byte; any number of extra
		// 0xFF fill bytes may precede it and are carried across as they are.
		b, err := br.ReadByte()
		if err == io.EOF {
			return changed, nil
		}
		if err != nil {
			return changed, err
		}
		if b != 0xFF {
			return changed, fmt.Errorf("jpeg: expected a marker, got 0x%02X", b)
		}
		pad := []byte{0xFF}
		var marker byte
		for {
			marker, err = br.ReadByte()
			if err != nil {
				return changed, fmt.Errorf("jpeg: %w", err)
			}
			if marker != 0xFF {
				break
			}
			pad = append(pad, 0xFF)
		}

		// SOS opens the entropy-coded scan and EOI closes the file; from
		// either point on, nothing is a segment any more. Copy the remainder
		// verbatim — that is the photo itself.
		if marker == markerSOS || marker == markerEOI {
			if _, err := dst.Write(append(pad, marker)); err != nil {
				return changed, err
			}
			if _, err := io.Copy(dst, br); err != nil {
				return changed, err
			}
			return changed, nil
		}
		// Standalone markers carry no length.
		if marker == markerTEM || marker == markerSOI || (marker >= 0xD0 && marker <= 0xD7) {
			if _, err := dst.Write(append(pad, marker)); err != nil {
				return changed, err
			}
			continue
		}

		var lb [2]byte
		if _, err := io.ReadFull(br, lb[:]); err != nil {
			return changed, fmt.Errorf("jpeg: %w", err)
		}
		n := int(binary.BigEndian.Uint16(lb[:]))
		if n < 2 {
			return changed, fmt.Errorf("jpeg: segment 0x%02X has length %d", marker, n)
		}

		if marker == markerAPP1 {
			payload := make([]byte, n-2)
			if _, err := io.ReadFull(br, payload); err != nil {
				return changed, fmt.Errorf("jpeg: %w", err)
			}
			var out []byte
			if bytes.HasPrefix(payload, exifPrefix) {
				out = buildExifAPP1(parseExifKeep(payload[len(exifPrefix):]))
			}
			if out == nil {
				changed = true // dropped: an XMP APP1, or an EXIF one with nothing to keep
				continue
			}
			if !bytes.Equal(out, payload) {
				changed = true
			}
			if _, err := dst.Write(append(pad, marker)); err != nil {
				return changed, err
			}
			var ob [2]byte
			binary.BigEndian.PutUint16(ob[:], uint16(len(out)+2))
			if _, err := dst.Write(ob[:]); err != nil {
				return changed, err
			}
			if _, err := dst.Write(out); err != nil {
				return changed, err
			}
			continue
		}

		if _, err := dst.Write(append(pad, marker)); err != nil {
			return changed, err
		}
		if _, err := dst.Write(lb[:]); err != nil {
			return changed, err
		}
		if _, err := io.CopyN(dst, br, int64(n-2)); err != nil {
			return changed, fmt.Errorf("jpeg: %w", err)
		}
	}
}

// scrubJPEGInPlace rewrites path without its EXIF location, atomically: a
// temp file beside it, fsynced, renamed over the original. A file that is
// already clean is not written at all — it keeps its bytes and its mtime —
// which is what makes the boot sweep safe to re-run.
func scrubJPEGInPlace(path string) (bool, error) {
	in, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer in.Close()

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".scrub-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // a no-op once the file has been renamed away
	}()

	w := bufio.NewWriterSize(tmp, 1<<16)
	changed, err := scrubJPEG(w, in)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := w.Flush(); err != nil {
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		return false, err
	}
	mode := os.FileMode(0o644)
	if fi, err := in.Stat(); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := tmp.Chmod(mode); err != nil {
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return false, err
	}
	// The rename itself has to reach the volume, or a machine that dies here
	// comes back to a file that is neither the old one nor the new one.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return true, nil
}

// sweepMediaGPS rewrites every original already on the volume, once. It is run
// from main under the meta key media_gps_stripped_v1, so it never runs twice
// on a volume that completed it — and, because the key is only recorded when
// this returns nil, a boot that hit an error tries again on the next one.
//
// Thumbnails are not swept: they are re-encoded here with image/jpeg, whose
// encoder writes a JFIF APP0 and nothing else, so they have never carried an
// EXIF block to strip.
func sweepMediaGPS(store *Store, mediaDir string) error {
	paths, err := store.PhotoPaths()
	if err != nil {
		return err
	}
	var rewritten, clean, missing int
	var failed []string
	for _, rel := range paths {
		p := filepath.Join(mediaDir, filepath.FromSlash(rel))
		switch changed, err := scrubJPEGInPlace(p); {
		case os.IsNotExist(err):
			missing++
		case err != nil:
			failed = append(failed, fmt.Sprintf("%s: %v", rel, err))
		case changed:
			rewritten++
		default:
			clean++
		}
	}
	log.Printf("media_gps_stripped_v1: %d original(s) — %d rewritten without EXIF location, %d already clean, %d missing, %d error(s)",
		len(paths), rewritten, clean, missing, len(failed))
	for _, f := range failed {
		log.Printf("media_gps_stripped_v1: %s", f)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d original(s) could not be rewritten", len(failed))
	}
	return nil
}
