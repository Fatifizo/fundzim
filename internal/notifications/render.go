package notifications

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Templates use {{variable}} placeholders only: no conditionals, loops, functions or includes, so a template cannot
// execute code or reach data the engine did not pass. A variable must be in the template's allow-list; its value is
// cleaned (control characters removed, length bounded) and escaped for HTML.

var placeholderRE = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]{0,40})\s*\}\}`)
var strayBracesRE = regexp.MustCompile(`\{\{|\}\}`)

// ErrRender is a template that cannot be rendered with the given variables.
var ErrRender = errors.New("notifications: template render failed")

// Content is one template version's channel texts.
type Content struct {
	EmailSubject, EmailText, SMSText *string
	WhatsAppName, WhatsAppLanguage   *string
	InAppTitle, InAppBody            string
}

// Placeholders returns the distinct variable names used in s, sorted.
func Placeholders(s string) []string {
	seen := map[string]bool{}
	for _, m := range placeholderRE.FindAllStringSubmatch(s, -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidateContent checks a template version before it is saved: every placeholder is allowed, no stray braces, no
// header-breaking characters in the subject, SMS within three segments.
func ValidateContent(c Content, allowed []string) error {
	texts := []string{c.InAppTitle, c.InAppBody}
	for _, p := range []*string{c.EmailSubject, c.EmailText, c.SMSText} {
		if p != nil {
			texts = append(texts, *p)
		}
	}
	for _, t := range texts {
		for _, v := range Placeholders(t) {
			if !contains(allowed, v) {
				return fmt.Errorf("%w: variable %q is not allowed for this template", ErrRender, v)
			}
		}
		if strayBracesRE.MatchString(placeholderRE.ReplaceAllString(t, "")) {
			return fmt.Errorf("%w: unbalanced placeholder braces", ErrRender)
		}
	}
	if c.EmailSubject != nil && strings.ContainsAny(*c.EmailSubject, "\r\n") {
		return fmt.Errorf("%w: the subject cannot contain line breaks", ErrRender)
	}
	if c.SMSText != nil {
		if est := EstimateSMS(*c.SMSText); est.Segments > MaxSMSSegments {
			return fmt.Errorf("%w: the SMS text needs %d segments (at most %d)", ErrRender, est.Segments, MaxSMSSegments)
		}
	}
	return nil
}

// ContentHash is SHA-256 over the channel texts joined by the unit separator (U+001F); the migration seeds compute the
// same form in SQL.
func ContentHash(c Content) []byte {
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	h := sha256.Sum256([]byte(strings.Join([]string{s(c.EmailSubject), s(c.EmailText), s(c.SMSText), c.InAppTitle, c.InAppBody}, "\x1f")))
	return h[:]
}

const maxVarLen = 200

// cleanValue removes control characters (CR and LF included: no header injection) and bounds the length.
func cleanValue(v string) string {
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, v)
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > maxVarLen {
		v = string([]rune(v)[:maxVarLen])
	}
	return v
}

// fill substitutes placeholders; a missing variable is an error (never an empty gap in a message).
func fill(t string, vars map[string]string, esc func(string) string) (string, error) {
	var missing string
	out := placeholderRE.ReplaceAllStringFunc(t, func(m string) string {
		name := placeholderRE.FindStringSubmatch(m)[1]
		v, ok := vars[name]
		if !ok {
			missing = name
			return ""
		}
		return esc(cleanValue(v))
	})
	if missing != "" {
		return "", fmt.Errorf("%w: variable %q has no value", ErrRender, missing)
	}
	return out, nil
}

func identity(s string) string { return s }

// Rendered is one channel's message.
type Rendered struct {
	Subject, Text, HTML string // email
	SMS                 string
	WhatsAppName        string
	WhatsAppLanguage    string
	WhatsAppParams      []string // values in placeholder order of the in-app body (template variables)
	Title, Body         string   // in-app
}

const emailFooter = "This is an automated message from FundZim."
const testLabel = "[TEST] "

// Render renders one channel of a template version. Synthetic (TEST) notifications are labelled.
func Render(c Content, vars map[string]string, channel string, synthetic bool) (Rendered, error) {
	var r Rendered
	var err error
	label := ""
	if synthetic {
		label = testLabel
	}
	switch channel {
	case ChInApp:
		if r.Title, err = fill(c.InAppTitle, vars, identity); err != nil {
			return r, err
		}
		if r.Body, err = fill(c.InAppBody, vars, identity); err != nil {
			return r, err
		}
		r.Title = label + r.Title
	case ChEmail:
		if c.EmailSubject == nil || c.EmailText == nil {
			return r, fmt.Errorf("%w: no email content", ErrRender)
		}
		if r.Subject, err = fill(*c.EmailSubject, vars, identity); err != nil {
			return r, err
		}
		r.Subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(label + r.Subject)
		if r.Text, err = fill(*c.EmailText, vars, identity); err != nil {
			return r, err
		}
		r.Text += "\n\n" + emailFooter
		// HTML is built only from escaped text: the template's own characters and every variable value
		escaped, err := fill(html.EscapeString(*c.EmailText), vars, html.EscapeString)
		if err != nil {
			return r, err
		}
		var b strings.Builder
		for _, para := range strings.Split(strings.ReplaceAll(escaped, "\r", ""), "\n\n") {
			b.WriteString("<p>" + strings.ReplaceAll(strings.TrimSpace(para), "\n", "<br>") + "</p>")
		}
		b.WriteString("<p>" + html.EscapeString(emailFooter) + "</p>")
		r.HTML = b.String()
	case ChSMS:
		if c.SMSText == nil {
			return r, fmt.Errorf("%w: no SMS content", ErrRender)
		}
		if r.SMS, err = fill(*c.SMSText, vars, identity); err != nil {
			return r, err
		}
		r.SMS = label + r.SMS
		if EstimateSMS(r.SMS).Segments > MaxSMSSegments {
			return r, fmt.Errorf("%w: SMS too long", ErrRender)
		}
	case ChWhatsApp:
		if c.WhatsAppName == nil || c.WhatsAppLanguage == nil {
			return r, fmt.Errorf("%w: no WhatsApp template", ErrRender)
		}
		r.WhatsAppName, r.WhatsAppLanguage = *c.WhatsAppName, *c.WhatsAppLanguage
		for _, v := range Placeholders(c.InAppBody) {
			val, ok := vars[v]
			if !ok {
				return r, fmt.Errorf("%w: variable %q has no value", ErrRender, v)
			}
			r.WhatsAppParams = append(r.WhatsAppParams, cleanValue(val))
		}
	default:
		return r, fmt.Errorf("%w: unknown channel", ErrRender)
	}
	return r, nil
}

// HasChannel reports whether a template version has content for a channel.
func (c Content) HasChannel(channel string) bool {
	switch channel {
	case ChInApp:
		return true
	case ChEmail:
		return c.EmailSubject != nil && c.EmailText != nil
	case ChSMS:
		return c.SMSText != nil
	case ChWhatsApp:
		return c.WhatsAppName != nil && c.WhatsAppLanguage != nil
	}
	return false
}

// ---- SMS length --------------------------------------------------------------------------------------------------

// MaxSMSSegments bounds a notification SMS (engineering default; no provider pricing is assumed).
const MaxSMSSegments = 3

// gsm7Basic is the GSM 03.38 default alphabet; gsm7Ext characters take two septets (escape + character).
const gsm7Basic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
const gsm7Ext = "^{}\\[~]|€\f"

// SMSEstimate is the encoding and segment count of a text.
type SMSEstimate struct {
	Encoding string // GSM7 | UCS2
	Units    int    // septets (GSM7) or UTF-16 code units (UCS2)
	Segments int
}

// EstimateSMS estimates segments the way the GSM standards count them (160/153 septets, 70/67 UCS-2 units). A
// provider may count differently: PROVIDER_CONFIRMATION_REQUIRED.
func EstimateSMS(s string) SMSEstimate {
	septets, gsm := 0, true
	for _, r := range s {
		switch {
		case strings.ContainsRune(gsm7Basic, r):
			septets++
		case strings.ContainsRune(gsm7Ext, r):
			septets += 2
		default:
			gsm = false
		}
	}
	if gsm {
		return SMSEstimate{Encoding: "GSM7", Units: septets, Segments: segments(septets, 160, 153)}
	}
	units := 0
	for _, r := range s {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return SMSEstimate{Encoding: "UCS2", Units: units, Segments: segments(units, 70, 67)}
}

func segments(n, single, multi int) int {
	if n == 0 {
		return 0
	}
	if n <= single {
		return 1
	}
	return (n + multi - 1) / multi
}
