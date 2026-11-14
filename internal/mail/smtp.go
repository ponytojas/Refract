// Package mail sends the plain-text emails of the account flows: confirming an
// address, choosing a new password, saying an account was deleted. Standard
// library only; SMTP with STARTTLS.
package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	netmail "net/mail"
)

type Message struct{ To, Subject, Body string }

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SMTP sends through a mail server (REFRACT_SMTP_URL).
type SMTP struct {
	addr, host, user, pass string
	from                   netmail.Address
	plainOK                bool // loopback host: STARTTLS not required (Mailpit)
}

// ParseSMTP reads REFRACT_SMTP_URL (smtp://[user[:pass]@]host[:port], default
// port 587) and REFRACT_MAIL_FROM ("Name <addr>" or "addr"). Errors never
// contain the password.
func ParseSMTP(rawURL, from string) (*SMTP, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("REFRACT_SMTP_URL is not a valid URL")
	}
	if u.Scheme != "smtp" {
		return nil, fmt.Errorf("REFRACT_SMTP_URL must start with smtp:// (STARTTLS on port 587), not %q", u.Scheme+"://")
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("REFRACT_SMTP_URL has no host: %s", u.Redacted())
	}
	addr, err := netmail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("REFRACT_MAIL_FROM is not an email address (Name <addr@example.com> or addr@example.com)")
	}
	port := cmpPort(u.Port())
	s := &SMTP{addr: net.JoinHostPort(u.Hostname(), port), host: u.Hostname(), from: *addr}
	if u.User != nil {
		s.user = u.User.Username()
		s.pass, _ = u.User.Password()
	}
	switch strings.ToLower(s.host) {
	case "localhost", "127.0.0.1", "::1":
		s.plainOK = true
	}
	return s, nil
}

func cmpPort(p string) string {
	if p == "" {
		return "587"
	}
	return p
}

// Send delivers one message. STARTTLS is used whenever offered and is required
// unless the server is on this machine, so credentials and mail never cross
// the network in clear.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("refusing a header with a line break")
	}
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("bad recipient: %w", err)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return err
	}
	defer c.Close()
	encrypted := false
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
		encrypted = true
	} else if !s.plainOK {
		return errors.New("the mail server doesn't offer STARTTLS")
	}
	if s.user != "" && encrypted { // never a password in clear, even to this machine
		if err := c.Auth(smtp.PlainAuth("", s.user, s.pass, s.host)); err != nil {
			return err
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, s.render(to.Address, m)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (s *SMTP) render(to string, m Message) string {
	id := make([]byte, 12)
	rand.Read(id)
	domain := "localhost"
	if _, d, ok := strings.Cut(s.from.Address, "@"); ok {
		domain = d
	}
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", s.from.String())
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "quoted-printable")
	h("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	w := quotedprintable.NewWriter(&b)
	w.Write([]byte(strings.ReplaceAll(m.Body, "\n", "\r\n")))
	w.Close()
	return b.String()
}
