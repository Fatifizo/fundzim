package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"

	"github.com/Fatifizo/fundzim/internal/storage"
)

// Limits bound what the decoder may be asked to allocate. They are checked from the image header (image.DecodeConfig)
// BEFORE any pixel data is decoded, so a small file declaring a huge canvas (a decompression bomb) is refused
// without allocating it.
type Limits struct {
	MaxPixels int64 // width × height
	MaxSide   int   // per side, in pixels
}

// DefaultLimits: 40 megapixels and 8000 px per side.
var DefaultLimits = Limits{MaxPixels: 40_000_000, MaxSide: 8000}

// ProcessError is a content rejection with a stable reason code (ck_campaign_media_rejected_reason).
type ProcessError struct {
	Code string
	Err  error
}

func (e *ProcessError) Error() string {
	if e.Err != nil {
		return "media: " + e.Code + ": " + e.Err.Error()
	}
	return "media: " + e.Code
}

func reject(code string, format string, a ...any) error {
	return &ProcessError{Code: code, Err: fmt.Errorf(format, a...)}
}

// RejectCode returns the reason code of a ProcessError ("" for anything else).
func RejectCode(err error) string {
	var pe *ProcessError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// Processed is a re-encoded derivative.
type Processed struct {
	Data          []byte
	ContentType   string
	Width, Height int
}

// activeMarkers are refused ANYWHERE in an image (the storage sniffer already refuses markup in the browser sniffing
// window). Only long markers are used so that compressed pixel data cannot plausibly contain one by chance; XMP
// metadata (<?xpacket, <x:xmpmeta, <rdf:RDF) is legitimate and not matched. The re-encode drops everything that is
// not pixels anyway; refusing these files keeps obvious polyglots out of storage altogether.
var activeMarkers = [][]byte{[]byte("<script"), []byte("<html"), []byte("<!doctype html"), []byte("<?php"), []byte("<iframe"),
	[]byte("javascript:"), []byte("<body")}

var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// Process validates an image and re-encodes it with the Go standard library only, dropping every byte that is not
// pixel data: EXIF (including GPS), XMP, ICC profiles, comments, PNG text/time/eXIf chunks and anything appended
// after the image. The EXIF orientation of a JPEG is applied to the pixels first so the derivative displays the same
// way without metadata.
//
// declaredType is the type recorded for the stored object (the storage pipeline already required the declared type
// to equal the sniffed one); the content must still sniff, and decode, as exactly that type. maxBytes bounds the
// derivative (the storage upload cap).
func Process(content []byte, declaredType string, lim Limits, maxBytes int64) (Processed, error) {
	if lim == (Limits{}) {
		lim = DefaultLimits
	}
	sniffed := storage.Sniff(content)
	if sniffed == "" {
		if (bytes.HasPrefix(content, pngMagic) || bytes.HasPrefix(content, []byte{0xFF, 0xD8, 0xFF})) && hasActiveContent(content) {
			return Processed{}, reject(ReasonActiveContent, "markup in the image")
		}
		return Processed{}, reject(ReasonUnsupported, "not a JPEG or PNG image")
	}
	if sniffed != declaredType || (sniffed != storage.TypeJPEG && sniffed != storage.TypePNG) {
		return Processed{}, reject(ReasonTypeMismatch, "content is %s, declared %s", sniffed, declaredType)
	}
	if hasActiveContent(content) {
		return Processed{}, reject(ReasonActiveContent, "markup in the image")
	}
	if sniffed == storage.TypePNG {
		if err := checkPNGStructure(content); err != nil {
			return Processed{}, err
		}
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return Processed{}, reject(ReasonDecodeFailed, "header: %v", err)
	}
	if want := map[string]string{storage.TypeJPEG: "jpeg", storage.TypePNG: "png"}[sniffed]; format != want {
		return Processed{}, reject(ReasonTypeMismatch, "decoder format %q for %s", format, sniffed)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return Processed{}, reject(ReasonDecodeFailed, "empty canvas")
	}
	if cfg.Width > lim.MaxSide || cfg.Height > lim.MaxSide || int64(cfg.Width)*int64(cfg.Height) > lim.MaxPixels {
		return Processed{}, reject(ReasonTooLarge, "%dx%d exceeds the limits", cfg.Width, cfg.Height)
	}

	var img image.Image
	orientation := 1
	switch sniffed {
	case storage.TypeJPEG:
		orientation = jpegOrientation(content)
		img, err = jpeg.Decode(bytes.NewReader(content))
	case storage.TypePNG:
		img, err = png.Decode(bytes.NewReader(content))
	}
	if err != nil {
		return Processed{}, reject(ReasonDecodeFailed, "decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != cfg.Width || b.Dy() != cfg.Height {
		return Processed{}, reject(ReasonDecodeFailed, "decoded size differs from the header")
	}
	img = orient(img, orientation)

	out, err := encode(img, sniffed, maxBytes)
	if err != nil {
		return Processed{}, err
	}
	// The derivative must itself be a clean image of the same type (defence in depth: never serve what we cannot sniff).
	if storage.Sniff(out) != sniffed {
		return Processed{}, reject(ReasonDecodeFailed, "derivative does not sniff as %s", sniffed)
	}
	b := img.Bounds()
	return Processed{Data: out, ContentType: sniffed, Width: b.Dx(), Height: b.Dy()}, nil
}

func hasActiveContent(content []byte) bool {
	lc := bytes.ToLower(content)
	for _, m := range activeMarkers {
		if bytes.Contains(lc, m) {
			return true
		}
	}
	return false
}

// checkPNGStructure walks the chunk list: lengths within the file, IHDR first, IEND last and nothing after it
// (a PNG has no legitimate trailer; appended data is how PNG/ZIP and PNG/HTML polyglots are built).
func checkPNGStructure(content []byte) error {
	off := len(pngMagic)
	first := true
	for chunks := 0; ; chunks++ {
		if chunks > 100_000 {
			return reject(ReasonDecodeFailed, "too many chunks")
		}
		if off+12 > len(content) {
			return reject(ReasonDecodeFailed, "truncated chunk")
		}
		n := binary.BigEndian.Uint32(content[off : off+4])
		typ := string(content[off+4 : off+8])
		if n > uint32(len(content)) || off+12+int(n) > len(content) {
			return reject(ReasonDecodeFailed, "chunk length out of range")
		}
		if first && typ != "IHDR" {
			return reject(ReasonDecodeFailed, "IHDR is not the first chunk")
		}
		first = false
		off += 12 + int(n)
		if typ == "IEND" {
			if off != len(content) {
				return reject(ReasonTrailingData, "%d bytes after IEND", len(content)-off)
			}
			return nil
		}
	}
}

func encode(img image.Image, contentType string, maxBytes int64) ([]byte, error) {
	var buf bytes.Buffer
	switch contentType {
	case storage.TypeJPEG:
		// The stdlib encoder writes SOI, DQT, SOF, DHT, SOS and EOI only: no APPn (EXIF/XMP/ICC) and no COM segment.
		for _, q := range []int{90, 82, 75} {
			buf.Reset()
			if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
				return nil, reject(ReasonDecodeFailed, "encode: %v", err)
			}
			if maxBytes <= 0 || int64(buf.Len()) <= maxBytes {
				return buf.Bytes(), nil
			}
		}
	case storage.TypePNG:
		// The stdlib encoder writes IHDR, PLTE/tRNS (paletted images only), IDAT and IEND: no tEXt/zTXt/iTXt, tIME,
		// eXIf, iCCP, gAMA or any other ancillary chunk.
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, reject(ReasonDecodeFailed, "encode: %v", err)
		}
		if maxBytes <= 0 || int64(buf.Len()) <= maxBytes {
			return buf.Bytes(), nil
		}
	}
	return nil, reject(ReasonDerivativeTooLarge, "derivative exceeds %d bytes", maxBytes)
}

// jpegOrientation reads the EXIF orientation (tag 0x0112 in IFD0 of an APP1 "Exif" segment); 1 when absent or
// malformed. Parsing is bounds-checked and stops at the first scan.
func jpegOrientation(b []byte) int {
	off := 2
	for off+4 <= len(b) {
		if b[off] != 0xFF {
			return 1
		}
		marker := b[off+1]
		if marker == 0xFF { // fill byte
			off++
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // start of scan / end of image
			return 1
		}
		if marker >= 0xD0 && marker <= 0xD7 || marker == 0x01 { // no length
			off += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(b[off+2 : off+4]))
		if n < 2 || off+2+n > len(b) {
			return 1
		}
		seg := b[off+4 : off+2+n]
		if marker == 0xE1 && len(seg) >= 6 && string(seg[:6]) == "Exif\x00\x00" {
			if o := tiffOrientation(seg[6:]); o != 0 {
				return o
			}
		}
		off += 2 + n
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(t[2:4]) != 42 {
		return 0
	}
	ifd := int(bo.Uint32(t[4:8]))
	if ifd < 8 || ifd+2 > len(t) {
		return 0
	}
	count := int(bo.Uint16(t[ifd : ifd+2]))
	for i := 0; i < count; i++ {
		e := ifd + 2 + 12*i
		if e+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[e:e+2]) == 0x0112 && bo.Uint16(t[e+2:e+4]) == 3 && bo.Uint32(t[e+4:e+8]) == 1 {
			if v := int(bo.Uint16(t[e+8 : e+10])); v >= 1 && v <= 8 {
				return v
			}
			return 0
		}
	}
	return 0
}

// orient applies an EXIF orientation (2–8) to the pixels.
func orient(src image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // mirror horizontal
				dx, dy = w-1-x, y
			case 3: // rotate 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirror vertical
				dx, dy = x, h-1-y
			case 5: // transpose
				dx, dy = y, x
			case 6: // rotate 90 clockwise
				dx, dy = h-1-y, x
			case 7: // transverse
				dx, dy = h-1-y, w-1-x
			case 8: // rotate 270 clockwise
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// VariantWidths are the srcset widths produced for approved media (never at or above the derivative's own width).
var VariantWidths = []int{320, 640, 1024}

// variantQuality is the JPEG quality of width variants.
const variantQuality = 80

// VariantWidthsFor returns the widths to produce for an image srcWidth pixels wide (no upscaling: widths at or above
// the source width are skipped).
func VariantWidthsFor(srcWidth int) []int {
	var out []int
	for _, w := range VariantWidths {
		if w < srcWidth {
			out = append(out, w)
		}
	}
	return out
}

// ScaledHeight is the height of an srcW×srcH image scaled to width w (aspect ratio kept, rounded, at least 1).
func ScaledHeight(srcW, srcH, w int) int {
	h := int((int64(srcH)*int64(w)*2 + int64(srcW)) / (2 * int64(srcW)))
	if h < 1 {
		h = 1
	}
	return h
}

// ScaleToWidth downscales img to width w with an area-averaging box filter (each destination pixel is the coverage-
// weighted mean of the source pixels under it). It never upscales: w ≥ the source width returns an error. Transparent
// pixels are composited on white, since the result is encoded as JPEG. Memory is one source row and one destination
// row of accumulators beyond the output image.
func ScaleToWidth(img image.Image, w int) (*image.RGBA, error) {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if w <= 0 || w >= sw || sh <= 0 {
		return nil, fmt.Errorf("media: cannot scale %dx%d to width %d (downscale only)", sw, sh, w)
	}
	h := ScaledHeight(sw, sh, w)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xs := boxWeights(sw, w)
	ys := boxWeights(sh, h)
	row := make([]float64, 4*sw) // one source row, premultiplied RGBA (0..65535)
	hrow := make([]float64, 4*w) // that row reduced horizontally
	acc := make([]float64, 4*w)  // current destination row
	cached := -1
	for dy := 0; dy < h; dy++ {
		for i := range acc {
			acc[i] = 0
		}
		for _, yw := range ys[dy] {
			if yw.src != cached {
				readRow(img, b, yw.src, row)
				for i := range hrow {
					hrow[i] = 0
				}
				for dx := 0; dx < w; dx++ {
					for _, xw := range xs[dx] {
						o := 4 * xw.src
						hrow[4*dx] += row[o] * xw.w
						hrow[4*dx+1] += row[o+1] * xw.w
						hrow[4*dx+2] += row[o+2] * xw.w
						hrow[4*dx+3] += row[o+3] * xw.w
					}
				}
				cached = yw.src
			}
			for i := range acc {
				acc[i] += hrow[i] * yw.w
			}
		}
		for dx := 0; dx < w; dx++ {
			a := acc[4*dx+3]
			o := dst.PixOffset(dx, dy)
			for c := 0; c < 3; c++ {
				v := acc[4*dx+c] + (65535 - a) // premultiplied colour over white
				dst.Pix[o+c] = clamp8(v)
			}
			dst.Pix[o+3] = 255
		}
	}
	return dst, nil
}

type boxWeight struct {
	src int
	w   float64
}

// boxWeights maps each of n destination cells to the source cells it covers, with weights summing to 1.
func boxWeights(srcN, n int) [][]boxWeight {
	out := make([][]boxWeight, n)
	scale := float64(srcN) / float64(n)
	for d := 0; d < n; d++ {
		lo, hi := float64(d)*scale, float64(d+1)*scale
		for s := int(lo); s < srcN && float64(s) < hi; s++ {
			cov := min(float64(s+1), hi) - max(float64(s), lo)
			if cov > 0 {
				out[d] = append(out[d], boxWeight{src: s, w: cov / scale})
			}
		}
	}
	return out
}

func clamp8(v float64) uint8 {
	v = v/257 + 0.5
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return uint8(v)
}

// readRow fills row with the premultiplied RGBA (0..65535) of source row y (relative to b.Min), with fast paths for
// the decoder outputs (YCbCr for JPEG, RGBA/NRGBA for PNG).
func readRow(img image.Image, b image.Rectangle, y int, row []float64) {
	sy := b.Min.Y + y
	switch src := img.(type) {
	case *image.YCbCr:
		for x := 0; x < b.Dx(); x++ {
			sx := b.Min.X + x
			yi, ci := src.YOffset(sx, sy), src.COffset(sx, sy)
			r, g, bl := color.YCbCrToRGB(src.Y[yi], src.Cb[ci], src.Cr[ci])
			row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = float64(r)*257, float64(g)*257, float64(bl)*257, 65535
		}
	case *image.RGBA:
		for x := 0; x < b.Dx(); x++ {
			p := src.Pix[src.PixOffset(b.Min.X+x, sy):]
			row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = float64(p[0])*257, float64(p[1])*257, float64(p[2])*257, float64(p[3])*257
		}
	default:
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, a := img.At(b.Min.X+x, sy).RGBA()
			row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = float64(r), float64(g), float64(bl), float64(a)
		}
	}
}

// Variant is one encoded width variant.
type Variant struct {
	Width, Height int
	Data          []byte
}

// MakeVariants decodes a derivative (already metadata-free) and encodes the JPEG width variants smaller than it.
// Each variant is encoded from pixels, so it carries no metadata either.
func MakeVariants(derivative []byte, contentType string, lim Limits) ([]Variant, error) {
	if lim == (Limits{}) {
		lim = DefaultLimits
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(derivative))
	if err != nil {
		return nil, reject(ReasonDecodeFailed, "header: %v", err)
	}
	if cfg.Width > lim.MaxSide || cfg.Height > lim.MaxSide || int64(cfg.Width)*int64(cfg.Height) > lim.MaxPixels {
		return nil, reject(ReasonTooLarge, "%dx%d exceeds the limits", cfg.Width, cfg.Height)
	}
	widths := VariantWidthsFor(cfg.Width)
	if len(widths) == 0 {
		return nil, nil
	}
	var img image.Image
	switch contentType {
	case storage.TypeJPEG:
		img, err = jpeg.Decode(bytes.NewReader(derivative))
	case storage.TypePNG:
		img, err = png.Decode(bytes.NewReader(derivative))
	default:
		return nil, reject(ReasonUnsupported, "type %s", contentType)
	}
	if err != nil {
		return nil, reject(ReasonDecodeFailed, "decode: %v", err)
	}
	out := make([]Variant, 0, len(widths))
	for _, w := range widths {
		scaled, err := ScaleToWidth(img, w)
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: variantQuality}); err != nil {
			return nil, reject(ReasonDecodeFailed, "encode: %v", err)
		}
		out = append(out, Variant{Width: w, Height: scaled.Bounds().Dy(), Data: buf.Bytes()})
	}
	return out, nil
}
