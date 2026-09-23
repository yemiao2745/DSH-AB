package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"os"
	"testing"
)

// icoEntry is one image inside a Vista+ .ico file: either a PNG-compressed image
// or an uncompressed BMP (DIB) image, which is what Windows expects below 256x256.
// Only the container is read here; what the icon looks like is judged by eye and
// by the built artifact.
type icoEntry struct {
	width, height int
	data          []byte
}

// pngSignature starts every PNG-compressed .ico entry.
var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// readICO decodes the .ico container the build produces.
func readICO(t *testing.T, path string) []icoEntry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(raw) < 6 {
		t.Fatalf("%s is too short to be an icon", path)
	}
	count := int(binary.LittleEndian.Uint16(raw[4:6]))
	entries := make([]icoEntry, 0, count)
	for i := 0; i < count; i++ {
		off := 6 + 16*i
		if off+16 > len(raw) {
			t.Fatalf("%s: entry %d header is truncated", path, i)
		}
		w, h := int(raw[off]), int(raw[off+1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		size := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		start := int(binary.LittleEndian.Uint32(raw[off+12 : off+16]))
		if start+size > len(raw) {
			t.Fatalf("%s: entry %d payload is out of range", path, i)
		}
		entries = append(entries, icoEntry{width: w, height: h, data: raw[start : start+size]})
	}
	return entries
}

// payloadSize reports the pixel size the entry's own payload declares: a PNG's
// IHDR, or a DIB's BITMAPINFOHEADER, whose height counts the XOR and the AND half.
func payloadSize(t *testing.T, e icoEntry) (int, int) {
	t.Helper()
	if bytes.HasPrefix(e.data, pngSignature) {
		cfg, err := png.DecodeConfig(bytes.NewReader(e.data))
		if err != nil {
			t.Fatalf("entry %dx%d is not a decodable PNG: %v", e.width, e.height, err)
		}
		return cfg.Width, cfg.Height
	}
	d := e.data
	if len(d) < 40 {
		t.Fatalf("entry %dx%d: DIB payload is %d bytes, shorter than a header", e.width, e.height, len(d))
	}
	if hdrSize := int(binary.LittleEndian.Uint32(d[0:4])); hdrSize != 40 {
		t.Fatalf("entry %dx%d: DIB header is size=%d, want 40", e.width, e.height, hdrSize)
	}
	w := int(int32(binary.LittleEndian.Uint32(d[4:8])))
	h := int(int32(binary.LittleEndian.Uint32(d[8:12])))
	return w, h / 2 // the DIB holds the XOR image over the AND mask
}

// TestIconEntriesMatchTheirDeclaredSize pins the container itself: an entry that
// claims 16x16 must contain a 16x16 image. This is the one thing about the icon a
// test can decide on its own; the pixels (margins, ink share, corner alpha, glyph
// size at every size) used to be decoded and thresholded here as well, which only
// ever re-measured the same static asset.
func TestIconEntriesMatchTheirDeclaredSize(t *testing.T) {
	entries := readICO(t, "assets/dsh.ico")
	if len(entries) == 0 {
		t.Fatal("assets/dsh.ico has no entries")
	}
	for _, e := range entries {
		w, h := payloadSize(t, e)
		if w != e.width || h != e.height {
			t.Fatalf("entry declares %dx%d but holds a %dx%d image", e.width, e.height, w, h)
		}
	}
}
