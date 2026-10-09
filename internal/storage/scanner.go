package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Verdict is a scanner's decision. Clean is only meaningful when Scan returned a nil error.
type Verdict struct {
	Clean     bool
	Signature string // malware/test signature name when !Clean
	Engine    string // engine and signature version, recorded in scan_engine
}

// Scanner scans one object's content. Any error means "unknown" — the pipeline records FAILED_SCAN and never
// treats the object as clean.
type Scanner interface {
	Scan(ctx context.Context, r io.Reader) (Verdict, error)
}

// ScanError classifies a scanner failure with a last_scan_error code.
type ScanError struct {
	Code string // ScanErrUnavailable | ScanErrTimeout | ScanErrScanner
	Err  error
}

func (e *ScanError) Error() string { return "scanner: " + e.Code + ": " + e.Err.Error() }
func (e *ScanError) Unwrap() error { return e.Err }

// scanErrorCode maps any error to a last_scan_error code.
func scanErrorCode(err error) string {
	var se *ScanError
	if errors.As(err, &se) {
		return se.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ScanErrTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ScanErrTimeout
	}
	return ScanErrScanner
}

// Scanner kinds (MALWARE_SCANNER).
const (
	ScannerClamd = "clamd"
	ScannerDev   = "dev"
)

// DevEngine is the engine name the dev scanner reports.
const DevEngine = "dev-scanner (NOT malware protection)"

// ErrDevScannerInProduction is returned when the dev scanner is requested with APP_ENV=production.
var ErrDevScannerInProduction = errors.New("storage: the dev scanner is not malware protection and is refused in production")

// NewScanner builds the configured scanner. appEnv is APP_ENV; the dev scanner is refused in production
// (config additionally refuses it outside development and test).
func NewScanner(kind, clamdAddr string, timeout time.Duration, appEnv string) (Scanner, error) {
	switch kind {
	case ScannerClamd:
		if clamdAddr == "" {
			return nil, invalidInput("CLAMAV_ADDR is required for the clamd scanner")
		}
		return &ClamdScanner{Addr: clamdAddr, Timeout: timeout}, nil
	case ScannerDev:
		if appEnv == "production" || appEnv == "staging" {
			return nil, ErrDevScannerInProduction
		}
		return DevScanner{}, nil
	}
	return nil, invalidInput("unknown scanner %q", kind)
}

// eicar is the standard antivirus test string (https://www.eicar.org/download-anti-malware-testfile/). The
// dev scanner detects only this; it is a pipeline test double, not malware protection.
var eicar = []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)

// EICAR returns a copy of the EICAR test string (tests and smoke checks).
func EICAR() []byte { return append([]byte(nil), eicar...) }

// DevScanner reports every object clean unless it contains the EICAR test string.
type DevScanner struct{}

// Scan implements Scanner.
func (DevScanner) Scan(ctx context.Context, r io.Reader) (Verdict, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return Verdict{}, &ScanError{Code: ScanErrScanner, Err: err}
	}
	if err := ctx.Err(); err != nil {
		return Verdict{}, &ScanError{Code: ScanErrTimeout, Err: err}
	}
	if bytes.Contains(b, eicar) {
		return Verdict{Clean: false, Signature: "Eicar-Test-Signature", Engine: DevEngine}, nil
	}
	return Verdict{Clean: true, Engine: DevEngine}, nil
}

// ClamdScanner talks to clamd over TCP (INSTREAM, z-prefixed NUL-terminated commands).
type ClamdScanner struct {
	Addr      string
	Timeout   time.Duration // whole-scan deadline (default 60 s)
	ChunkSize int           // INSTREAM chunk size (default 64 KiB)
}

func (c *ClamdScanner) dial(ctx context.Context) (net.Conn, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	d := net.Dialer{Timeout: min(5*time.Second, timeout)}
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return nil, &ScanError{Code: ScanErrUnavailable, Err: err}
	}
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	return conn, nil
}

func readReply(conn net.Conn) (string, error) {
	s, err := bufio.NewReaderSize(io.LimitReader(conn, 4096), 4096).ReadString(0)
	if err != nil && !(errors.Is(err, io.EOF) && s != "") {
		return "", classifyIO(err)
	}
	return strings.TrimRight(s, "\x00\n"), nil
}

func classifyIO(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &ScanError{Code: ScanErrTimeout, Err: err}
	}
	return &ScanError{Code: ScanErrScanner, Err: err}
}

// Version returns clamd's engine/signature version, e.g. "ClamAV 1.5.3/27790".
func (c *ClamdScanner) Version(ctx context.Context) (string, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("zVERSION\x00")); err != nil {
		return "", classifyIO(err)
	}
	v, err := readReply(conn)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(v, "ClamAV ") {
		return "", &ScanError{Code: ScanErrScanner, Err: fmt.Errorf("unexpected VERSION reply %q", truncate(v, 80))}
	}
	// drop the signature date: "ClamAV 1.5.3/27790/Thu Oct  9 08:00:00 2026"
	if p := strings.SplitN(v, "/", 3); len(p) >= 2 {
		v = p[0] + "/" + p[1]
	}
	return v, nil
}

// Scan implements Scanner using INSTREAM. Any protocol, network or clamd error is returned as a *ScanError.
func (c *ClamdScanner) Scan(ctx context.Context, r io.Reader) (Verdict, error) {
	engine, err := c.Version(ctx)
	if err != nil {
		return Verdict{}, err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return Verdict{}, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return Verdict{}, classifyIO(err)
	}
	size := c.ChunkSize
	if size <= 0 {
		size = 64 << 10
	}
	buf := make([]byte, 4+size)
	for {
		n, rerr := io.ReadFull(r, buf[4:])
		if n > 0 {
			binary.BigEndian.PutUint32(buf[:4], uint32(n))
			if _, err := conn.Write(buf[:4+n]); err != nil {
				// clamd closes the connection early when the stream exceeds StreamMaxLength; its reply says so
				if reply, rerr2 := readReply(conn); rerr2 == nil && reply != "" {
					return Verdict{}, &ScanError{Code: ScanErrScanner, Err: fmt.Errorf("clamd: %s", truncate(reply, 120))}
				}
				return Verdict{}, classifyIO(err)
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return Verdict{}, &ScanError{Code: ScanErrScanner, Err: fmt.Errorf("read object: %w", rerr)}
		}
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return Verdict{}, classifyIO(err)
	}
	reply, err := readReply(conn)
	if err != nil {
		if ctx.Err() != nil {
			return Verdict{}, &ScanError{Code: ScanErrTimeout, Err: ctx.Err()}
		}
		return Verdict{}, err
	}
	return parseClamdReply(reply, engine)
}

// parseClamdReply interprets an INSTREAM reply. Only an exact "stream: OK" is clean.
func parseClamdReply(reply, engine string) (Verdict, error) {
	switch {
	case reply == "stream: OK":
		return Verdict{Clean: true, Engine: engine}, nil
	case strings.HasPrefix(reply, "stream: ") && strings.HasSuffix(strings.TrimPrefix(reply, "stream: "), " FOUND"):
		sig := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(reply, "stream: "), " FOUND"))
		if sig == "" {
			return Verdict{}, &ScanError{Code: ScanErrScanner, Err: errors.New("clamd: FOUND without a signature")}
		}
		return Verdict{Clean: false, Signature: truncate(sig, 255), Engine: engine}, nil
	}
	return Verdict{}, &ScanError{Code: ScanErrScanner, Err: fmt.Errorf("clamd: unexpected reply %q", truncate(reply, 120))}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
