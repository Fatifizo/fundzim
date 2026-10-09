package storage

import (
	"bytes"
	"mime"
	"strings"
)

// sniffWindow is how far browsers look when content-sniffing; markup inside it is refused in images.
const sniffWindow = 1024

var (
	magicPNG  = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	magicJPEG = []byte{0xFF, 0xD8, 0xFF}
	magicPDF  = []byte("%PDF-")
	// markup that a browser (or a careless viewer) could interpret as active content
	markupMarkers = [][]byte{[]byte("<script"), []byte("<html"), []byte("<!doctype"), []byte("<svg"), []byte("<?php"),
		[]byte("<iframe"), []byte("<body"), []byte("<?xml"), []byte("javascript:")}
)

// Sniff returns the media type detected from content's magic bytes, or "" when the content is not on the
// allow-list (JPEG, PNG, PDF) or carries markup in the browser sniffing window (polyglot/HTML disguised as
// an image). It never looks at a client filename or declared type.
func Sniff(content []byte) string {
	switch {
	case bytes.HasPrefix(content, magicPNG):
		// the IHDR chunk must follow the signature (length 13, type IHDR)
		if len(content) < 16 || !bytes.Equal(content[8:16], []byte{0, 0, 0, 13, 'I', 'H', 'D', 'R'}) {
			return ""
		}
		if hasMarkup(content) {
			return ""
		}
		return TypePNG
	case bytes.HasPrefix(content, magicJPEG):
		// SOI followed by a marker byte (APPn, DQT, SOF, …): FF D8 FF xx with xx in C0..FE
		if len(content) < 4 || content[3] < 0xC0 || content[3] == 0xFF {
			return ""
		}
		if hasMarkup(content) {
			return ""
		}
		return TypeJPEG
	case bytes.HasPrefix(content, magicPDF):
		// "%PDF-1.x" / "%PDF-2.0" version digits
		if len(content) < 8 || content[5] < '1' || content[5] > '2' || content[6] != '.' {
			return ""
		}
		return TypePDF
	}
	return ""
}

func hasMarkup(content []byte) bool {
	w := content
	if len(w) > sniffWindow {
		w = w[:sniffWindow]
	}
	lw := bytes.ToLower(w)
	for _, m := range markupMarkers {
		if bytes.Contains(lw, m) {
			return true
		}
	}
	return false
}

// NormalizeDeclaredType parses a client Content-Type and maps aliases. It returns "" when unparseable.
func NormalizeDeclaredType(declared string) string {
	mt, _, err := mime.ParseMediaType(strings.TrimSpace(declared))
	if err != nil {
		return ""
	}
	switch mt {
	case "image/jpg", "image/pjpeg":
		return TypeJPEG
	case "application/x-pdf":
		return TypePDF
	}
	return mt
}

// checkContent applies the upload content rules for bucket: non-empty, ≤ max, sniffed type on the class's
// allow-list, declared type equal to the sniffed one. It returns the sniffed type or a reject reason + error.
func checkContent(bucket, declared string, content []byte, tooLarge bool) (sniffed, reason string, err error) {
	if tooLarge {
		return "", RejectTooLarge, ErrTooLarge
	}
	if len(content) == 0 {
		return "", RejectEmpty, ErrEmptyFile
	}
	allowed := allowedTypes[bucket]
	d := NormalizeDeclaredType(declared)
	sniffed = Sniff(content)
	if sniffed == "" || !allowed[sniffed] {
		return "", RejectUnsupported, ErrUnsupportedType
	}
	if !allowed[d] {
		// declared something we would never accept (e.g. text/html, application/octet-stream)
		return "", RejectTypeMismatch, ErrTypeMismatch
	}
	if d != sniffed {
		return "", RejectTypeMismatch, ErrTypeMismatch
	}
	return sniffed, "", nil
}
