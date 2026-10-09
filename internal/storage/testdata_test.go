package storage

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
)

// samplePNG returns a small valid PNG.
func samplePNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// sampleJPEG returns a small valid JPEG.
func sampleJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

// samplePDF returns a minimal PDF document.
func samplePDF() []byte {
	return []byte("%PDF-1.4\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n2 0 obj << /Type /Pages /Kids [] /Count 0 >> endobj\n" +
		"trailer << /Root 1 0 R >>\n%%EOF\n")
}
