package storage

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

// MultipartSpec describes the only accepted shape of an upload request: named text fields first, then exactly
// one file part (the last part). Anything else is refused.
type MultipartSpec struct {
	FileField     string   // name of the file part, e.g. "file"
	Fields        []string // allowed text field names (each at most once)
	Required      []string // text fields that must be present
	MaxFileBytes  int64    // file cap (the caller passes UPLOAD_MAX_BYTES or a per-purpose cap)
	MaxFieldBytes int64    // per text field (default 256)
	// MaxOverhead bounds everything that is not file content (headers, boundaries, fields); default 16 KiB.
	MaxOverhead int64
}

// MultipartUpload is a parsed upload request. File streams the file part; it must be read to EOF (Upload
// does) — reaching EOF also verifies that no part follows the file (otherwise the read fails with
// ErrMalformedUpload, so Upload rejects the object before anything is stored).
type MultipartUpload struct {
	Fields              map[string]string
	DeclaredContentType string // the file part's Content-Type header (untrusted; compared with sniffing)
	File                io.Reader
}

// ParseMultipart reads the form fields of a multipart/form-data request and returns a streaming reader for
// its single file part. It never writes to disk and never exposes the client filename. The whole body is
// capped with http.MaxBytesReader at MaxFileBytes+MaxOverhead (the route must be exempt from the global body
// limit; see httpx.BodyLimitWithExemptions). Errors: ErrMalformedUpload (shape) or ErrTooLarge.
func ParseMultipart(w http.ResponseWriter, r *http.Request, spec MultipartSpec) (*MultipartUpload, error) {
	if spec.FileField == "" || spec.MaxFileBytes <= 0 {
		return nil, invalidInput("multipart spec needs FileField and MaxFileBytes")
	}
	if spec.MaxFieldBytes <= 0 {
		spec.MaxFieldBytes = 256
	}
	if spec.MaxOverhead <= 0 {
		spec.MaxOverhead = 16 << 10
	}
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		return nil, ErrMalformedUpload
	}
	limit := spec.MaxFileBytes + spec.MaxOverhead
	if r.ContentLength > limit {
		return nil, ErrTooLarge
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	mr := multipart.NewReader(r.Body, params["boundary"])
	allowed := map[string]bool{}
	for _, f := range spec.Fields {
		allowed[f] = true
	}
	out := &MultipartUpload{Fields: map[string]string{}}
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil, partErr(err) // includes io.EOF: no file part
		}
		name := part.FormName()
		if name == "" {
			return nil, ErrMalformedUpload
		}
		if name == spec.FileField {
			if part.FileName() == "" { // a file part must be a file (the name itself is ignored)
				return nil, ErrMalformedUpload
			}
			for _, req := range spec.Required {
				if _, ok := out.Fields[req]; !ok {
					return nil, ErrMalformedUpload
				}
			}
			out.DeclaredContentType = part.Header.Get("Content-Type")
			out.File = &lastPartReader{part: part, mr: mr}
			return out, nil
		}
		if !allowed[name] || part.FileName() != "" {
			return nil, ErrMalformedUpload
		}
		if _, dup := out.Fields[name]; dup {
			return nil, ErrMalformedUpload
		}
		b, err := io.ReadAll(io.LimitReader(part, spec.MaxFieldBytes+1))
		if err != nil {
			return nil, partErr(err)
		}
		if int64(len(b)) > spec.MaxFieldBytes {
			return nil, ErrMalformedUpload
		}
		out.Fields[name] = strings.TrimSpace(string(b))
	}
}

func partErr(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return ErrTooLarge
	}
	return errors.Join(ErrMalformedUpload, err)
}

// lastPartReader streams the file part and, at its end, requires the multipart stream to end too.
type lastPartReader struct {
	part *multipart.Part
	mr   *multipart.Reader
	done bool
}

func (l *lastPartReader) Read(p []byte) (int, error) {
	if l.done {
		return 0, io.EOF
	}
	n, err := l.part.Read(p)
	if errors.Is(err, io.EOF) {
		next, nerr := l.mr.NextPart()
		switch {
		case errors.Is(nerr, io.EOF):
			l.done = true
			return n, io.EOF
		case nerr != nil:
			return n, partErr(nerr)
		default:
			_ = next.Close()
			return n, ErrMalformedUpload // an extra part after the file
		}
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return n, err // Upload maps MaxBytesError to FILE_TOO_LARGE
	}
	return n, err
}
