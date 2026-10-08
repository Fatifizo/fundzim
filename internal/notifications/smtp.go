package notifications

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/config"
)

// SMTP timeouts (contract §2.4).
const (
	DialTimeout  = 5 * time.Second
	TotalTimeout = 15 * time.Second
)

// smtpSender speaks SMTP with the standard library. One connection per message keeps it simple and
// stateless; volumes are small (verification, reset and security notices).
type smtpSender struct {
	addr     string
	host     string
	tlsMode  string // none | starttls | tls
	user     string
	pass     config.Secret
	from     *mail.Address
	tlsConf  *tls.Config
	dial     func(ctx context.Context, network, addr string) (net.Conn, error)
	now      func() time.Time
	dialTO   time.Duration
	totalTO  time.Duration
	hostname string // EHLO name
}

func newSMTPSender(cfg config.Email) (*smtpSender, error) {
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, errors.New("notifications: EMAIL_FROM is not a valid address")
	}
	if cfg.SMTPHost == "" || cfg.SMTPPort <= 0 {
		return nil, errors.New("notifications: SMTP_HOST and SMTP_PORT are required")
	}
	switch cfg.SMTPTLS {
	case "none", "starttls", "tls":
	default:
		return nil, errors.New("notifications: SMTP_TLS must be none, starttls or tls")
	}
	d := &net.Dialer{Timeout: DialTimeout}
	return &smtpSender{
		addr:     net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort)),
		host:     cfg.SMTPHost,
		tlsMode:  cfg.SMTPTLS,
		user:     cfg.SMTPUser,
		pass:     cfg.SMTPPass,
		from:     from,
		tlsConf:  &tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12},
		dial:     d.DialContext,
		now:      time.Now,
		dialTO:   DialTimeout,
		totalTO:  TotalTimeout,
		hostname: "fundzim.local",
	}, nil
}

// Send delivers m. Errors name the SMTP stage and reply code only, never addresses or content.
func (s *smtpSender) Send(ctx context.Context, m Email) error {
	if inTx(ctx) {
		return ErrInTransaction
	}
	to, err := checkEmail(m)
	if err != nil {
		return err
	}
	msg, err := s.build(to, m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.totalTO)
	defer cancel()
	// wrap names the stage; once ctx has ended, a closed-connection error is really the timeout/cancel.
	wrap := func(stage string, err error) error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("smtp: %s: timeout", stage)
		}
		if ctx.Err() != nil {
			return fmt.Errorf("smtp: %s: cancelled", stage)
		}
		return fmt.Errorf("smtp: %s: %w", stage, sanitize(err))
	}

	dctx, dcancel := context.WithTimeout(ctx, s.dialTO)
	conn, err := s.dial(dctx, "tcp", s.addr)
	dcancel()
	if err != nil {
		return wrap("connect", err)
	}
	// One deadline for the whole conversation, and closing the connection if ctx ends first.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer conn.Close()

	if s.tlsMode == "tls" {
		tc := tls.Client(conn, s.tlsConf)
		if err := tc.HandshakeContext(ctx); err != nil {
			return wrap("tls handshake", err)
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return wrap("greeting", err)
	}
	defer c.Close()
	if err := c.Hello(s.hostname); err != nil {
		return wrap("hello", err)
	}
	if s.tlsMode == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("smtp: server does not offer STARTTLS (SMTP_TLS=starttls)")
		}
		if err := c.StartTLS(s.tlsConf); err != nil {
			return wrap("starttls", err)
		}
	}
	if s.user != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("smtp: server does not offer AUTH but credentials are configured")
		}
		// PlainAuth itself refuses to send credentials over an unencrypted connection to a remote host.
		if err := c.Auth(smtp.PlainAuth("", s.user, s.pass.Reveal(), s.host)); err != nil {
			return wrap("auth", err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return wrap("MAIL FROM", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return wrap("RCPT TO", err)
	}
	w, err := c.Data()
	if err != nil {
		return wrap("DATA", err)
	}
	if _, err := w.Write(msg); err != nil {
		return wrap("write", err)
	}
	if err := w.Close(); err != nil {
		return wrap("end of data", err)
	}
	_ = c.Quit()
	return nil
}

func checkEmail(m Email) (*mail.Address, error) {
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return nil, fmt.Errorf("%w: header fields must not contain line breaks", ErrInvalidMessage)
	}
	to, err := mail.ParseAddress(m.To)
	if err != nil || strings.Contains(m.To, ",") {
		return nil, fmt.Errorf("%w: recipient must be exactly one email address", ErrInvalidMessage)
	}
	if strings.TrimSpace(m.Subject) == "" || strings.TrimSpace(m.Text) == "" {
		return nil, fmt.Errorf("%w: subject and text body are required", ErrInvalidMessage)
	}
	return to, nil
}

// build renders an RFC 5322 message: text/plain, or multipart/alternative when HTML is given. Bodies are
// quoted-printable, so long lines and non-ASCII text are safe.
func (s *smtpSender) build(to *mail.Address, m Email) ([]byte, error) {
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", s.from.String())
	h("To", to.String())
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", s.now().UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+randHex(16)+"@"+domainOf(s.from.Address)+">")
	h("MIME-Version", "1.0")
	h("Auto-Submitted", "auto-generated")
	if m.HTML == "" {
		h("Content-Type", `text/plain; charset="utf-8"`)
		h("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		if err := writeQP(&b, m.Text); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	boundary := "fz-" + randHex(12)
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, part := range []struct{ ctype, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString(`Content-Type: ` + part.ctype + `; charset="utf-8"` + "\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := writeQP(&b, part.body); err != nil {
			return nil, err
		}
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes(), nil
}

func writeQP(b *bytes.Buffer, s string) error {
	w := quotedprintable.NewWriter(b)
	if _, err := w.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n"))); err != nil {
		return err
	}
	return w.Close()
}

func randHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func domainOf(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return addr[i+1:]
	}
	return "fundzim.invalid"
}

// sanitize reduces SMTP protocol errors to their reply code: server replies can echo addresses.
func sanitize(err error) error {
	var te *textproto.Error
	if errors.As(err, &te) {
		return fmt.Errorf("server replied %d", te.Code)
	}
	return classifyNetErr(err)
}

// classifyNetErr keeps network error categories (timeout, refused) without addresses.
func classifyNetErr(err error) error {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("timeout")
	case errors.As(err, &ne) && ne.Timeout():
		return errors.New("timeout")
	case strings.Contains(err.Error(), "connection refused"):
		return errors.New("connection refused")
	case errors.Is(err, net.ErrClosed), errors.Is(err, context.Canceled):
		return errors.New("connection closed or cancelled")
	default:
		return errors.New("network or protocol error")
	}
}
