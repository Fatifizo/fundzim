package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Fatifizo/fundzim/internal/storage"
)

func testImage(w, h int) *image.RGBA { return markedImage(w, h, 4) }

// markedImage draws a gradient with a red n×n marker block at the top-left corner (orientation checks).
func markedImage(w, h, n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 11), 90, 255})
		}
	}
	for y := 0; y < n && y < h; y++ {
		for x := 0; x < n && x < w; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	return img
}

func pngChunk(typ string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(typ)
	b.Write(data)
	c := crc32.NewIEEE()
	c.Write([]byte(typ))
	c.Write(data)
	_ = binary.Write(&b, binary.BigEndian, c.Sum32())
	return b.Bytes()
}

// pngChunks lists the chunk types of a PNG.
func pngChunks(t *testing.T, b []byte) []string {
	t.Helper()
	var out []string
	off := 8
	for off+12 <= len(b) {
		n := int(binary.BigEndian.Uint32(b[off:]))
		out = append(out, string(b[off+4:off+8]))
		off += 12 + n
	}
	return out
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// exifAPP1 builds an APP1 Exif segment (big-endian TIFF) with IFD0 {Orientation, Make, GPSInfo → GPS IFD with a
// latitude reference and a recognisable marker string}.
func exifAPP1(orientation uint16) []byte {
	var t bytes.Buffer
	t.WriteString("MM")
	_ = binary.Write(&t, binary.BigEndian, uint16(42))
	_ = binary.Write(&t, binary.BigEndian, uint32(8)) // IFD0 at 8
	mk := []byte("GPS-MARKER-CAMERA\x00")
	// IFD0: 3 entries (12 bytes each) + count + next = 2 + 36 + 4 = 42 → data area starts at 8 + 42 = 50
	makeOff := uint32(50)
	gpsOff := makeOff + uint32(len(mk))
	entry := func(tag, typ uint16, count, value uint32) {
		_ = binary.Write(&t, binary.BigEndian, tag)
		_ = binary.Write(&t, binary.BigEndian, typ)
		_ = binary.Write(&t, binary.BigEndian, count)
		_ = binary.Write(&t, binary.BigEndian, value)
	}
	_ = binary.Write(&t, binary.BigEndian, uint16(3))
	entry(0x010F, 2, uint32(len(mk)), makeOff)        // Make (ASCII)
	entry(0x0112, 3, 1, uint32(orientation)<<16)      // Orientation (SHORT, left-justified)
	entry(0x8825, 4, 1, gpsOff)                       // GPSInfo IFD pointer
	_ = binary.Write(&t, binary.BigEndian, uint32(0)) // no IFD1
	t.Write(mk)
	// GPS IFD: GPSLatitudeRef "S", GPSAreaInformation (undefined) with a marker
	area := []byte("ASCII\x00\x00\x00GPS-MARKER-HARARE")
	areaOff := gpsOff + 2 + 2*12 + 4
	_ = binary.Write(&t, binary.BigEndian, uint16(2))
	entry(0x0001, 2, 2, uint32('S')<<24)
	entry(0x001C, 7, uint32(len(area)), areaOff)
	_ = binary.Write(&t, binary.BigEndian, uint32(0))
	t.Write(area)
	payload := append([]byte("Exif\x00\x00"), t.Bytes()...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

func insertAfterSOI(jpg, seg []byte) []byte {
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if got := RejectCode(err); got != code {
		t.Fatalf("want rejection %s, got %v", code, err)
	}
}

func TestProcessJPEGStripsEXIFAndGPS(t *testing.T) {
	src := insertAfterSOI(encodeJPEG(t, testImage(40, 20)), exifAPP1(6))
	if !bytes.Contains(src, []byte("GPS-MARKER-HARARE")) || storage.Sniff(src) != storage.TypeJPEG {
		t.Fatal("fixture: EXIF/GPS not embedded")
	}
	if jpegOrientation(src) != 6 {
		t.Fatalf("fixture orientation = %d", jpegOrientation(src))
	}
	p, err := Process(src, storage.TypeJPEG, DefaultLimits, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"Exif", "GPS-MARKER", "MM\x00*"} {
		if bytes.Contains(p.Data, []byte(needle)) {
			t.Errorf("derivative still contains %q", needle)
		}
	}
	// no APPn or COM segment at all before the scan
	for off := 2; off+4 <= len(p.Data); {
		m := p.Data[off+1]
		if m == 0xDA {
			break
		}
		if m >= 0xE0 && m <= 0xEF || m == 0xFE {
			t.Errorf("derivative has metadata segment FF%02X", m)
		}
		off += 2 + int(binary.BigEndian.Uint16(p.Data[off+2:]))
	}
	// orientation 6 (rotate 90° clockwise) applied to the pixels: 40x20 becomes 20x40, top-left marker moves to top-right
	if p.Width != 20 || p.Height != 40 || p.ContentType != storage.TypeJPEG {
		t.Fatalf("derivative %dx%d %s", p.Width, p.Height, p.ContentType)
	}
	img, err := jpeg.Decode(bytes.NewReader(p.Data))
	if err != nil {
		t.Fatal(err)
	}
	r, g, _, _ := img.At(18, 1).RGBA()
	if r>>8 < 200 || g>>8 > 80 {
		t.Errorf("orientation not applied: pixel (18,1) = %d,%d", r>>8, g>>8)
	}
	if jpegOrientation(p.Data) != 1 {
		t.Error("derivative carries an orientation")
	}
}

func TestProcessPNGStripsAncillaryChunks(t *testing.T) {
	base := encodePNG(t, testImage(16, 8))
	// insert text, time, eXIf and iCCP-like chunks after IHDR (8 + 25 bytes)
	var b bytes.Buffer
	b.Write(base[:33])
	b.Write(pngChunk("tEXt", []byte("Comment\x00GPS-MARKER-TEXT")))
	b.Write(pngChunk("iTXt", []byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00<x:xmpmeta>GPS-MARKER-XMP</x:xmpmeta>")))
	b.Write(pngChunk("tIME", []byte{0x07, 0xEA, 10, 9, 12, 0, 0}))
	b.Write(pngChunk("eXIf", exifAPP1(1)[10:]))
	b.Write(base[33:])
	src := b.Bytes()
	if _, err := png.Decode(bytes.NewReader(src)); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	p, err := Process(src, storage.TypePNG, DefaultLimits, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	chunks := pngChunks(t, p.Data)
	for _, c := range chunks {
		if c != "IHDR" && c != "IDAT" && c != "IEND" {
			t.Errorf("derivative keeps chunk %s (all: %v)", c, chunks)
		}
	}
	if bytes.Contains(p.Data, []byte("GPS-MARKER")) {
		t.Error("derivative still contains the metadata text")
	}
	if p.Width != 16 || p.Height != 8 || p.ContentType != storage.TypePNG {
		t.Fatalf("derivative %dx%d %s", p.Width, p.Height, p.ContentType)
	}
}

// bombPNG declares a huge canvas in IHDR with a tiny (invalid) IDAT: only the header may be read.
func bombPNG(w, h uint32) []byte {
	var ihdr bytes.Buffer
	_ = binary.Write(&ihdr, binary.BigEndian, w)
	_ = binary.Write(&ihdr, binary.BigEndian, h)
	ihdr.Write([]byte{8, 6, 0, 0, 0})
	out := append([]byte{}, pngMagic...)
	out = append(out, pngChunk("IHDR", ihdr.Bytes())...)
	out = append(out, pngChunk("IDAT", []byte{0x78, 0x9C, 0x03, 0x00, 0x00, 0x00, 0x00, 0x01})...)
	return append(out, pngChunk("IEND", nil)...)
}

func TestProcessRefusesDecompressionBombs(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h uint32
	}{{"100k square", 100000, 100000}, {"too wide", 8001, 10}, {"too tall", 10, 8001}, {"over 40 MP", 7000, 7000}} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Process(bombPNG(c.w, c.h), storage.TypePNG, DefaultLimits, 10<<20)
			wantCode(t, err, ReasonTooLarge)
		})
	}
	// a JPEG header declaring 60000x60000 (SOF0 patched)
	j := encodeJPEG(t, testImage(8, 8))
	for i := 2; i+9 < len(j); i++ {
		if j[i] == 0xFF && j[i+1] == 0xC0 {
			binary.BigEndian.PutUint16(j[i+5:], 60000)
			binary.BigEndian.PutUint16(j[i+7:], 60000)
			break
		}
	}
	_, err := Process(j, storage.TypeJPEG, DefaultLimits, 10<<20)
	wantCode(t, err, ReasonTooLarge)
	// configurable limits
	_, err = Process(encodePNG(t, testImage(30, 30)), storage.TypePNG, Limits{MaxPixels: 800, MaxSide: 8000}, 10<<20)
	wantCode(t, err, ReasonTooLarge)
}

func TestProcessRefusesPolyglotsAndGarbage(t *testing.T) {
	png8 := encodePNG(t, testImage(8, 8))
	jpg8 := encodeJPEG(t, testImage(8, 8))
	html := []byte("<html><body><script>alert(document.cookie)</script></body></html>")
	padding := bytes.Repeat([]byte{0}, 2048) // beyond the browser sniffing window
	com := func(payload []byte) []byte {
		seg := []byte{0xFF, 0xFE, 0, 0}
		binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
		return append(seg, payload...)
	}
	cases := []struct {
		name, ctype, code string
		content           []byte
	}{
		{"garbage", storage.TypeJPEG, ReasonUnsupported, []byte("this is not an image at all, just text")},
		{"empty", storage.TypePNG, ReasonUnsupported, nil},
		{"html", storage.TypePNG, ReasonUnsupported, html},
		{"png with html in the sniffing window", storage.TypePNG, ReasonActiveContent,
			append(append([]byte{}, png8[:33]...), append(pngChunk("tEXt", append([]byte("c\x00"), html...)), png8[33:]...)...)},
		{"png with html appended late", storage.TypePNG, ReasonActiveContent, append(append(append([]byte{}, png8...), padding...), html...)},
		{"png with zip appended", storage.TypePNG, ReasonTrailingData, append(append([]byte{}, png8...), []byte("PK\x03\x04\x14\x00\x00\x00")...)},
		{"jpeg with script comment past the window", storage.TypeJPEG, ReasonActiveContent,
			insertAfterSOI(jpg8, com(append(padding, []byte("<script src=//x></script>")...)))},
		{"truncated jpeg", storage.TypeJPEG, ReasonDecodeFailed, jpg8[:len(jpg8)/2]},
		{"truncated png", storage.TypePNG, ReasonDecodeFailed, png8[:len(png8)-20]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Process(c.content, c.ctype, DefaultLimits, 10<<20)
			wantCode(t, err, c.code)
		})
	}
}

func TestProcessRefusesMismatchedDeclaredType(t *testing.T) {
	_, err := Process(encodeJPEG(t, testImage(8, 8)), storage.TypePNG, DefaultLimits, 10<<20)
	wantCode(t, err, ReasonTypeMismatch)
	_, err = Process(encodePNG(t, testImage(8, 8)), storage.TypeJPEG, DefaultLimits, 10<<20)
	wantCode(t, err, ReasonTypeMismatch)
	_, err = Process(encodePNG(t, testImage(8, 8)), "image/gif", DefaultLimits, 10<<20)
	wantCode(t, err, ReasonTypeMismatch)
	// valid images of the declared type pass
	if _, err := Process(encodePNG(t, testImage(8, 8)), storage.TypePNG, DefaultLimits, 10<<20); err != nil {
		t.Fatal(err)
	}
}

func TestProcessDerivativeSizeCap(t *testing.T) {
	_, err := Process(encodePNG(t, testImage(64, 64)), storage.TypePNG, DefaultLimits, 100)
	wantCode(t, err, ReasonDerivativeTooLarge)
}

func TestOrientAllEXIFValues(t *testing.T) {
	src := markedImage(3, 2, 1) // marker at (0,0)
	want := map[int][3]int{     // orientation → (width, height, marker position index x*10+y)
		1: {3, 2, 0}, 2: {3, 2, 20}, 3: {3, 2, 21}, 4: {3, 2, 1}, 5: {2, 3, 0}, 6: {2, 3, 10}, 7: {2, 3, 12}, 8: {2, 3, 2},
	}
	for o, w := range want {
		img := orient(src, o)
		b := img.Bounds()
		if b.Dx() != w[0] || b.Dy() != w[1] {
			t.Fatalf("orientation %d: %dx%d", o, b.Dx(), b.Dy())
		}
		x, y := w[2]/10, w[2]%10
		if r, g, _, _ := img.At(x, y).RGBA(); r>>8 != 255 || g>>8 != 0 {
			t.Errorf("orientation %d: marker not at (%d,%d)", o, x, y)
		}
	}
}

// TestTransitionsMatchMigration keeps the Go edge list identical to the SQL registry (CLAUDE.md engineering rules).
func TestTransitionsMatchMigration(t *testing.T) {
	raw, err := os.ReadFile("../../../migrations/20261009171100_campaign_media.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.SplitN(string(raw), "-- +goose Down", 2)[0]
	re := regexp.MustCompile(`\('campaign_media',\s*'([A-Z_]*)',\s*'([A-Z_]+)'\)`)
	var sqlEdges, goEdges []string
	for _, m := range re.FindAllStringSubmatch(up, -1) {
		sqlEdges = append(sqlEdges, m[1]+">"+m[2])
	}
	for from, tos := range Transitions {
		for _, to := range tos {
			goEdges = append(goEdges, from+">"+to)
		}
	}
	sort.Strings(sqlEdges)
	sort.Strings(goEdges)
	if strings.Join(sqlEdges, ",") != strings.Join(goEdges, ",") || len(goEdges) == 0 {
		t.Fatalf("edge lists differ:\nsql %v\ngo  %v", sqlEdges, goEdges)
	}
}

func TestVariantWidthsNeverUpscale(t *testing.T) {
	cases := map[int][]int{1: nil, 300: nil, 320: nil, 321: {320}, 640: {320}, 641: {320, 640}, 1024: {320, 640}, 1025: {320, 640, 1024},
		8000: {320, 640, 1024}}
	for src, want := range cases {
		if got := VariantWidthsFor(src); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("VariantWidthsFor(%d) = %v, want %v", src, got, want)
		}
	}
	for _, w := range []int{300, 301, 0, -1} {
		if _, err := ScaleToWidth(image.NewRGBA(image.Rect(0, 0, 300, 100)), w); err == nil {
			t.Errorf("ScaleToWidth(300px, %d) must refuse (no upscaling / invalid)", w)
		}
	}
}

func TestScaleToWidthKeepsAspectRatio(t *testing.T) {
	for _, c := range []struct{ sw, sh, w, wantH int }{
		{2000, 1000, 320, 160}, {2000, 1000, 640, 320}, {2000, 1000, 1024, 512},
		{1001, 333, 320, 106}, {1500, 4000, 640, 1707}, {5000, 3, 320, 1}, {700, 700, 640, 640},
	} {
		img, err := ScaleToWidth(image.NewRGBA(image.Rect(0, 0, c.sw, c.sh)), c.w)
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != c.w || b.Dy() != c.wantH || ScaledHeight(c.sw, c.sh, c.w) != c.wantH {
			t.Errorf("%dx%d → w %d: got %dx%d, want height %d", c.sw, c.sh, c.w, b.Dx(), b.Dy(), c.wantH)
		}
	}
	// a non-zero origin (sub-image) is handled
	sub := image.NewRGBA(image.Rect(0, 0, 500, 300)).SubImage(image.Rect(50, 20, 450, 220)) // 400x200 at (50,20)
	if img, err := ScaleToWidth(sub, 200); err != nil || img.Bounds().Dy() != 100 {
		t.Fatalf("sub-image: %v %v", err, img.Bounds())
	}
}

func TestScaleToWidthAveragesArea(t *testing.T) {
	// left half black, right half white → 2 px wide: one black, one white
	src := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			v := uint8(0)
			if x >= 4 {
				v = 255
			}
			src.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	out, _ := ScaleToWidth(src, 2)
	if r, _, _, _ := out.At(0, 0).RGBA(); r>>8 != 0 {
		t.Errorf("left = %d", r>>8)
	}
	if r, _, _, _ := out.At(1, 0).RGBA(); r>>8 != 255 {
		t.Errorf("right = %d", r>>8)
	}
	// a 1-px checkerboard averages to mid grey; a fractional ratio (3 → 2) weights the shared column half each
	cb := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			v := uint8(0)
			if (x+y)%2 == 0 {
				v = 255
			}
			cb.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	out, _ = ScaleToWidth(cb, 2)
	if r, _, _, _ := out.At(0, 0).RGBA(); r>>8 < 126 || r>>8 > 129 {
		t.Errorf("checkerboard = %d", r>>8)
	}
	three := image.NewRGBA(image.Rect(0, 0, 3, 1))
	three.Set(0, 0, color.RGBA{0, 0, 0, 255})
	three.Set(1, 0, color.RGBA{90, 90, 90, 255})
	three.Set(2, 0, color.RGBA{180, 180, 180, 255})
	out, _ = ScaleToWidth(three, 2)
	// left = (0 + 0.5*90) / 1.5 = 30; right = (0.5*90 + 180) / 1.5 = 150
	if l, _, _, _ := out.At(0, 0).RGBA(); l>>8 != 30 {
		t.Errorf("fractional left = %d", l>>8)
	}
	if r, _, _, _ := out.At(1, 0).RGBA(); r>>8 != 150 {
		t.Errorf("fractional right = %d", r>>8)
	}
	// transparent pixels become white (JPEG has no alpha)
	tr := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	out, _ = ScaleToWidth(tr, 2)
	if r, g, b, _ := out.At(0, 0).RGBA(); r>>8 != 255 || g>>8 != 255 || b>>8 != 255 {
		t.Errorf("transparent → %d,%d,%d", r>>8, g>>8, b>>8)
	}
}

func TestMakeVariantsFromDerivative(t *testing.T) {
	// a JPEG derivative (YCbCr fast path) and a PNG derivative (generic path)
	for _, c := range []struct {
		name, ctype string
		data        []byte
		widths      []int
	}{
		{"jpeg 1500x750", storage.TypeJPEG, encodeJPEG(t, testImage(1500, 750)), []int{320, 640, 1024}},
		{"png 700x350", storage.TypePNG, encodePNG(t, testImage(700, 350)), []int{320, 640}},
		{"png 300x300", storage.TypePNG, encodePNG(t, testImage(300, 300)), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			vs, err := MakeVariants(c.data, c.ctype, DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			if len(vs) != len(c.widths) {
				t.Fatalf("variants %d, want %v", len(vs), c.widths)
			}
			for i, v := range vs {
				cfg, format, err := image.DecodeConfig(bytes.NewReader(v.Data))
				if err != nil || format != "jpeg" || cfg.Width != c.widths[i] || cfg.Height != v.Height || v.Width != c.widths[i] {
					t.Fatalf("variant %d: %v %s %dx%d", c.widths[i], err, format, cfg.Width, cfg.Height)
				}
				if storage.Sniff(v.Data) != storage.TypeJPEG || bytes.Contains(v.Data, []byte("Exif")) || jpegOrientation(v.Data) != 1 {
					t.Fatal("variant is not a plain JPEG")
				}
				if len(v.Data) >= len(c.data) && c.ctype == storage.TypeJPEG {
					t.Errorf("variant %d (%d bytes) not smaller than the derivative (%d)", v.Width, len(v.Data), len(c.data))
				}
			}
		})
	}
	_, err := MakeVariants([]byte("garbage"), storage.TypeJPEG, DefaultLimits)
	wantCode(t, err, ReasonDecodeFailed)
}
