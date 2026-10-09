package notifications

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime/quotedprintable"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/config"
)

// fakeSMTP is a minimal SMTP server on a loopback listener that records one message per session.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	messages []recorded
	// silent makes the server accept connections but never answer (timeout tests).
	silent    atomic.Bool // set by tests while the accept loop runs
	rejectRcp atomic.Bool
}

type recorded struct{ from, to, data string }

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go f.serve()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.session(c)
	}
}

func (f *fakeSMTP) session(c net.Conn) {
	defer c.Close()
	if f.silent.Load() {
		_, _ = io.Copy(io.Discard, c)
		return
	}
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	w("220 fake ESMTP")
	var rec recorded
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			w("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			rec.from = strings.TrimSpace(line[10:])
			w("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if f.rejectRcp.Load() {
				w("550 5.1.1 <" + strings.TrimSpace(line[8:]) + "> no such user")
				continue
			}
			rec.to = strings.TrimSpace(line[8:])
			w("250 ok")
		case cmd == "DATA":
			w("354 go ahead")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			rec.data = sb.String()
			f.mu.Lock()
			f.messages = append(f.messages, rec)
			f.mu.Unlock()
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("502 unknown")
		}
	}
}

func (f *fakeSMTP) got() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.messages...)
}

func emailCfg(port int) config.Email {
	return config.Email{Provider: "smtp", SMTPHost: "127.0.0.1", SMTPPort: port, SMTPTLS: "none",
		From: "FundZim <no-reply@fundzim.invalid>"}
}

func TestSMTPSendsTextAndHTML(t *testing.T) {
	srv := newFakeSMTP(t)
	s, err := NewEmailSender(emailCfg(srv.port()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Send(ctx, Email{To: "Ada <ada@example.test>", Subject: "Verify your email ✓", Text: "Hello\nline two"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(ctx, Email{To: "bob@example.test", Subject: "Hi", Text: "plain", HTML: "<p>html</p>"}); err != nil {
		t.Fatal(err)
	}
	msgs := srv.got()
	if len(msgs) != 2 {
		t.Fatalf("got %d messages", len(msgs))
	}
	if msgs[0].from != "<no-reply@fundzim.invalid>" || msgs[0].to != "<ada@example.test>" {
		t.Fatalf("envelope %+v", msgs[0])
	}
	if !strings.Contains(msgs[0].data, "Subject: =?utf-8?q?") || !strings.Contains(msgs[0].data, "text/plain") {
		t.Fatalf("headers: %s", msgs[0].data)
	}
	body, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(msgs[0].data[strings.Index(msgs[0].data, "\r\n\r\n")+4:])))
	if !strings.Contains(string(body), "Hello\r\nline two") {
		t.Fatalf("body %q", body)
	}
	if !strings.Contains(msgs[1].data, "multipart/alternative") || !strings.Contains(msgs[1].data, "text/html") {
		t.Fatalf("multipart missing: %s", msgs[1].data)
	}
}

func TestSMTPRefusesInsideTransaction(t *testing.T) {
	srv := newFakeSMTP(t)
	s, _ := NewEmailSender(emailCfg(srv.port()))
	sms, _ := NewSMSSender(config.SMS{Provider: "dev_mailpit"}, s)
	ctx := inTxContext(t)
	if err := s.Send(ctx, Email{To: "a@example.test", Subject: "s", Text: "t"}); !errors.Is(err, ErrInTransaction) {
		t.Fatalf("email: %v", err)
	}
	if err := sms.Send(ctx, SMS{To: "+263771234567", Body: "123456"}); !errors.Is(err, ErrInTransaction) {
		t.Fatalf("sms: %v", err)
	}
	ds, _ := NewSMSSender(config.SMS{Provider: "disabled"}, nil)
	if err := ds.Send(ctx, SMS{To: "+263771234567", Body: "x"}); !errors.Is(err, ErrInTransaction) {
		t.Fatalf("disabled sms: %v", err)
	}
	if len(srv.got()) != 0 {
		t.Fatal("nothing may be sent inside a transaction")
	}
}

// inTxContext simulates code running inside db.WithTx. db's context marker is unexported, so the unit
// test swaps the package's inTx probe; tests/integration/worker_outbox_test.go checks the real
// db.WithTx context.
func inTxContext(t *testing.T) context.Context {
	t.Helper()
	type marker struct{}
	orig := inTx
	inTx = func(ctx context.Context) bool { return ctx.Value(marker{}) != nil || orig(ctx) }
	t.Cleanup(func() { inTx = orig })
	return context.WithValue(context.Background(), marker{}, true)
}

func TestSMTPTimeoutAndUnreachable(t *testing.T) {
	srv := newFakeSMTP(t)
	srv.silent.Store(true)
	s, _ := newSMTPSender(emailCfg(srv.port()))
	s.totalTO = 300 * time.Millisecond
	start := time.Now()
	err := s.Send(context.Background(), Email{To: "a@example.test", Subject: "s", Text: "t"})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("want timeout, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("total timeout not enforced")
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	s2, _ := newSMTPSender(emailCfg(port))
	if err := s2.Send(context.Background(), Email{To: "a@example.test", Subject: "s", Text: "t"}); err == nil ||
		!strings.Contains(err.Error(), "connect") {
		t.Fatalf("want connect error, got %v", err)
	}
}

func TestSMTPErrorsDoNotEchoAddresses(t *testing.T) {
	srv := newFakeSMTP(t)
	srv.rejectRcp.Store(true)
	s, _ := NewEmailSender(emailCfg(srv.port()))
	err := s.Send(context.Background(), Email{To: "secret.person@example.test", Subject: "s", Text: "t"})
	if err == nil || strings.Contains(err.Error(), "secret.person") || !strings.Contains(err.Error(), "550") {
		t.Fatalf("got %v", err)
	}
}

func TestEmailValidationRejectsHeaderInjection(t *testing.T) {
	srv := newFakeSMTP(t)
	s, _ := NewEmailSender(emailCfg(srv.port()))
	for _, m := range []Email{
		{To: "a@example.test\r\nBcc: x@example.test", Subject: "s", Text: "t"},
		{To: "a@example.test", Subject: "s\r\nBcc: x@example.test", Text: "t"},
		{To: "a@example.test, b@example.test", Subject: "s", Text: "t"},
		{To: "not-an-address", Subject: "s", Text: "t"},
		{To: "a@example.test", Subject: "", Text: "t"},
	} {
		if err := s.Send(context.Background(), m); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("%+v: %v", m, err)
		}
	}
	if len(srv.got()) != 0 {
		t.Fatal("invalid messages must not be sent")
	}
}

func TestDevMailpitSMS(t *testing.T) {
	srv := newFakeSMTP(t)
	e, _ := NewEmailSender(emailCfg(srv.port()))
	s, err := NewSMSSender(config.SMS{Provider: "dev_mailpit"}, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), SMS{To: "+263771234567", Body: "Your FundZim code is 123456"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), SMS{To: "0771234567", Body: "x"}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("non-E.164 must be rejected: %v", err)
	}
	msgs := srv.got()
	if len(msgs) != 1 || msgs[0].to != "<263771234567@sms.dev.invalid>" {
		t.Fatalf("%+v", msgs)
	}
	if !strings.Contains(msgs[0].data, "Subject: SMS to +263*******67") {
		t.Fatalf("subject not masked: %s", msgs[0].data)
	}
}

func TestDisabledSendersReturnExplicitError(t *testing.T) {
	e, err := NewEmailSender(config.Email{Provider: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Send(context.Background(), Email{To: "a@example.test", Subject: "s", Text: "t"}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	s, _ := NewSMSSender(config.SMS{Provider: "disabled"}, nil)
	if err := s.Send(context.Background(), SMS{To: "+263771234567", Body: "x"}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := NewSMSSender(config.SMS{Provider: "dev_mailpit"}, nil); err == nil {
		t.Fatal("dev_mailpit without email sender must fail")
	}
	if _, err := NewEmailSender(config.Email{Provider: "smtp", SMTPHost: "h", SMTPPort: 25, SMTPTLS: "bogus", From: "a@b.test"}); err == nil {
		t.Fatal("bad TLS mode must fail")
	}
}

func TestMaskPhone(t *testing.T) {
	if got := MaskPhone("+263771234567"); got != "+263*******67" {
		t.Fatal(got)
	}
}
