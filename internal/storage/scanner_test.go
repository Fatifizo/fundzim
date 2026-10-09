package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeClamd serves the subset of the clamd protocol the scanner uses. reply decides the INSTREAM answer
// from the streamed content; hang makes INSTREAM never answer.
func fakeClamd(t *testing.T, reply func([]byte) string, hang bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				cmd, err := br.ReadString(0)
				if err != nil {
					return
				}
				switch cmd {
				case "zVERSION\x00":
					_, _ = c.Write([]byte("ClamAV 9.9.9/12345/Thu Oct  9 08:00:00 2026\x00"))
				case "zINSTREAM\x00":
					var data []byte
					for {
						var hdr [4]byte
						if _, err := io.ReadFull(br, hdr[:]); err != nil {
							return
						}
						n := binary.BigEndian.Uint32(hdr[:])
						if n == 0 {
							break
						}
						chunk := make([]byte, n)
						if _, err := io.ReadFull(br, chunk); err != nil {
							return
						}
						data = append(data, chunk...)
					}
					if hang {
						time.Sleep(5 * time.Second)
						return
					}
					_, _ = c.Write([]byte(reply(data) + "\x00"))
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func clamdReply(data []byte) string {
	if bytes.Contains(data, eicar) {
		return "stream: Win.Test.EICAR_HDB-1 FOUND"
	}
	return "stream: OK"
}

func TestClamdScannerVerdicts(t *testing.T) {
	addr := fakeClamd(t, clamdReply, false)
	s := &ClamdScanner{Addr: addr, Timeout: 2 * time.Second, ChunkSize: 7}
	v, err := s.Scan(context.Background(), bytes.NewReader(samplePDF()))
	if err != nil || !v.Clean || v.Engine != "ClamAV 9.9.9/12345" {
		t.Fatalf("clean: %+v %v", v, err)
	}
	v, err = s.Scan(context.Background(), bytes.NewReader(append(samplePDF(), eicar...)))
	if err != nil || v.Clean || v.Signature != "Win.Test.EICAR_HDB-1" {
		t.Fatalf("infected: %+v %v", v, err)
	}
	if o := Decide(v, err); o.Status != StatusRejected || o.Code != RejectMalware {
		t.Fatalf("decide infected: %+v", o)
	}
}

func TestScannerOutageNeverClean(t *testing.T) {
	// unreachable: a port nothing listens on
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	s := &ClamdScanner{Addr: addr, Timeout: time.Second}
	v, err := s.Scan(context.Background(), bytes.NewReader(samplePNG()))
	if err == nil {
		t.Fatalf("unreachable scanner returned a verdict: %+v", v)
	}
	o := Decide(v, err)
	if o.Status != StatusFailedScan || o.Code != ScanErrUnavailable {
		t.Fatalf("unreachable: %+v", o)
	}

	// timeout: clamd accepts the stream but never answers
	hang := &ClamdScanner{Addr: fakeClamd(t, clamdReply, true), Timeout: 300 * time.Millisecond}
	v, err = hang.Scan(context.Background(), bytes.NewReader(samplePNG()))
	if o := Decide(v, err); o.Status != StatusFailedScan || o.Code != ScanErrTimeout {
		t.Fatalf("timeout: %+v (%v)", o, err)
	}

	// protocol garbage and clamd errors
	for _, reply := range []string{"stream: INSTREAM size limit exceeded. ERROR", "garbage", "", "stream: FOUND", "stream: OK trailing"} {
		r := reply
		g := &ClamdScanner{Addr: fakeClamd(t, func([]byte) string { return r }, false), Timeout: time.Second}
		v, err := g.Scan(context.Background(), bytes.NewReader(samplePNG()))
		if o := Decide(v, err); o.Status != StatusFailedScan {
			t.Errorf("reply %q: %+v", reply, o)
		}
	}
}

func TestDecideOnlyCleanForExplicitVerdict(t *testing.T) {
	cases := []struct {
		v    Verdict
		err  error
		want string
	}{
		{Verdict{Clean: true, Engine: "x"}, nil, StatusClean},
		{Verdict{Clean: true, Engine: "x"}, errors.New("boom"), StatusFailedScan}, // a verdict with an error is ignored
		{Verdict{Clean: true}, nil, StatusFailedScan},                             // no engine: not a real scan
		{Verdict{}, context.DeadlineExceeded, StatusFailedScan},
		{Verdict{Clean: false, Engine: "x", Signature: "S"}, nil, StatusRejected},
	}
	for i, c := range cases {
		if got := Decide(c.v, c.err).Status; got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}

func TestDevScanner(t *testing.T) {
	v, err := DevScanner{}.Scan(context.Background(), bytes.NewReader(samplePNG()))
	if err != nil || !v.Clean || v.Engine != DevEngine || !strings.Contains(v.Engine, "NOT malware protection") {
		t.Fatalf("clean: %+v %v", v, err)
	}
	v, err = DevScanner{}.Scan(context.Background(), bytes.NewReader(append(samplePDF(), EICAR()...)))
	if err != nil || v.Clean {
		t.Fatalf("eicar not detected: %+v %v", v, err)
	}
}

func TestDevScannerRefusedInProduction(t *testing.T) {
	for _, env := range []string{"production", "staging"} {
		if _, err := NewScanner(ScannerDev, "", 0, env); !errors.Is(err, ErrDevScannerInProduction) {
			t.Errorf("%s: dev scanner allowed (%v)", env, err)
		}
	}
	for _, env := range []string{"development", "test"} {
		if _, err := NewScanner(ScannerDev, "", 0, env); err != nil {
			t.Errorf("%s: %v", env, err)
		}
	}
	if _, err := NewScanner(ScannerClamd, "", 0, "production"); err == nil {
		t.Error("clamd without address accepted")
	}
	if _, err := NewScanner("none", "", 0, "development"); err == nil {
		t.Error("unknown scanner accepted")
	}
}
