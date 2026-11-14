package mail

import (
	"bufio"
	"context"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseSMTP(t *testing.T) {
	s, err := ParseSMTP("smtp://u%40x:p%40ss@host:587", "Refract <no-reply@example.com>")
	if err != nil {
		t.Fatal(err)
	}
	if s.user != "u@x" || s.pass != "p@ss" || s.addr != "host:587" || s.plainOK {
		t.Errorf("parsed = %+v", s)
	}
	if s, _ := ParseSMTP("smtp://host", "a@example.com"); s == nil || s.addr != "host:587" {
		t.Errorf("default port: %+v", s)
	}
	if s, _ := ParseSMTP("smtp://127.0.0.1:1025", "a@example.com"); s == nil || !s.plainOK {
		t.Errorf("loopback: %+v", s)
	}
	for _, bad := range []string{"smtps://u:p@ss@host:465", "http://u:p%40ss@host", "smtp://u:p%40ss@:587", "smtp://u:p%40ss@host:587/%zz"} {
		_, err := ParseSMTP(bad, "a@example.com")
		if err == nil {
			t.Errorf("accepted %q", bad)
		} else if strings.Contains(err.Error(), "p@ss") || strings.Contains(err.Error(), "p%40ss") {
			t.Errorf("error leaks the password: %v", err)
		}
	}
	if _, err := ParseSMTP("smtp://host", "not an address"); err == nil {
		t.Error("accepted a bad sender")
	}
}

func TestCompose(t *testing.T) {
	d := Data{Email: "ana@example.com", Link: "https://r.test/verify#token=abc", Site: "r.test"}
	for _, k := range []Kind{Verify, Reset, Deleted} {
		en, err := Compose(k, "en", "ana@example.com", d)
		if err != nil {
			t.Fatal(k, err)
		}
		es, err := Compose(k, "es", "ana@example.com", d)
		if err != nil {
			t.Fatal(k, err)
		}
		for _, m := range []Message{en, es} {
			if m.Subject == "" || m.Body == "" || strings.Contains(m.Body, "<no value>") || strings.HasPrefix(m.Subject, "Subject") {
				t.Errorf("%s: %+v", k, m)
			}
			if k != Deleted && !strings.Contains(m.Body, d.Link) {
				t.Errorf("%s: no link in %q", k, m.Body)
			}
		}
		if en.Subject == es.Subject || en.Body == es.Body {
			t.Errorf("%s: Spanish equals English", k)
		}
		if fr, _ := Compose(k, "fr", "x@example.com", d); fr.Body != en.Body {
			t.Errorf("%s: fr isn't English", k)
		}
	}
	d.SignUp = true
	for _, lang := range []string{"en", "es"} {
		plain, _ := Compose(Reset, lang, "x@example.com", Data{Email: "x", Link: "l", Site: "s"})
		tried, _ := Compose(Reset, lang, "x@example.com", d)
		if plain.Body == tried.Body {
			t.Errorf("%s: SignUp changes nothing in reset", lang)
		}
		same := Data{Email: "x", Link: "l", Site: "s"}
		v1, _ := Compose(Verify, lang, "x@example.com", same)
		same.SignUp = true
		v2, _ := Compose(Verify, lang, "x@example.com", same)
		if v1.Body != v2.Body {
			t.Errorf("%s: SignUp changes verify", lang)
		}
	}
}

// fakeServer is a minimal SMTP server on loopback that records what it is told.
type fakeServer struct {
	addr     string
	startTLS bool
	mu       sync.Mutex
	cmds     []string
	data     string
}

func newFakeServer(t *testing.T, startTLS bool) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeServer{addr: ln.Addr().String(), startTLS: startTLS}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeServer) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(s string) { c.Write([]byte(s + "\r\n")) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		f.cmds = append(f.cmds, line)
		f.mu.Unlock()
		switch verb := strings.ToUpper(strings.Fields(line + " x")[0]); verb {
		case "EHLO":
			if f.startTLS {
				say("250-fake\r\n250 STARTTLS")
			} else {
				say("250 fake")
			}
		case "STARTTLS":
			say("220 go ahead")
			c.Write([]byte("this is not a TLS handshake")) // the client's handshake fails
			return
		case "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			say("250 queued")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeServer) saw(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cmds {
		if strings.HasPrefix(strings.ToUpper(c), prefix) {
			return true
		}
	}
	return false
}

func TestSendOverLoopback(t *testing.T) {
	f := newFakeServer(t, false)
	s, err := ParseSMTP("smtp://u:secret@"+f.addr, "Refract <no-reply@refract.example.com>")
	if err != nil {
		t.Fatal(err)
	}
	m := Message{To: "ana@example.com", Subject: "Confirma tu correo electrónico", Body: "Hola, señora:\nhttps://r.test/verify#token=abc\n"}
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if !f.saw("MAIL FROM:<NO-REPLY@REFRACT.EXAMPLE.COM>") || !f.saw("RCPT TO:<ANA@EXAMPLE.COM>") {
		t.Errorf("envelope: %v", f.cmds)
	}
	if f.saw("AUTH") {
		t.Error("credentials were sent without TLS")
	}
	msg, err := mail.ReadMessage(strings.NewReader(f.data))
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if strings.ContainsRune(msg.Header.Get("Subject"), 'ó') || subject != m.Subject {
		t.Errorf("subject = %q (decoded %q)", msg.Header.Get("Subject"), subject)
	}
	for k, want := range map[string]string{
		"Content-Type": "text/plain; charset=utf-8", "Content-Transfer-Encoding": "quoted-printable",
		"Mime-Version": "1.0", "Auto-Submitted": "auto-generated", "To": "ana@example.com",
	} {
		if got := msg.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if from, _ := mail.ParseAddress(msg.Header.Get("From")); from == nil || from.Address != "no-reply@refract.example.com" || from.Name != "Refract" {
		t.Errorf("From = %q", msg.Header.Get("From"))
	}
	if id := msg.Header.Get("Message-Id"); !strings.HasSuffix(id, "@refract.example.com>") {
		t.Errorf("Message-ID = %q", id)
	}
	if _, err := msg.Header.Date(); err != nil {
		t.Errorf("Date: %v", err)
	}
	var body strings.Builder
	buf := make([]byte, 4096)
	r := quotedprintable.NewReader(msg.Body)
	for {
		n, err := r.Read(buf)
		body.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if strings.ReplaceAll(body.String(), "\r\n", "\n") != m.Body {
		t.Errorf("body = %q", body.String())
	}
}

func TestRefusesPlaintextOffLoopback(t *testing.T) {
	f := newFakeServer(t, false)
	s, _ := ParseSMTP("smtp://"+f.addr, "a@example.com")
	s.plainOK = false
	err := s.Send(context.Background(), Message{To: "b@example.com", Subject: "s", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("err = %v, want one naming STARTTLS", err)
	}
	if f.saw("MAIL") {
		t.Error("mail was sent in clear")
	}
}

func TestStartTLSIsUsedWhenOffered(t *testing.T) {
	f := newFakeServer(t, true)
	s, _ := ParseSMTP("smtp://u:p@"+f.addr, "a@example.com")
	if err := s.Send(context.Background(), Message{To: "b@example.com", Subject: "s", Body: "b"}); err == nil {
		t.Fatal("the handshake should have failed")
	}
	if !f.saw("STARTTLS") {
		t.Error("STARTTLS was not asked for")
	}
	if f.saw("MAIL") || f.saw("AUTH") {
		t.Errorf("sent in clear after STARTTLS: %v", f.cmds)
	}
}

func TestHeaderInjection(t *testing.T) {
	f := newFakeServer(t, false)
	s, _ := ParseSMTP("smtp://"+f.addr, "a@example.com")
	for _, m := range []Message{
		{To: "a@x\r\nBcc: b@y", Subject: "s", Body: "b"},
		{To: "a@x.com", Subject: "s\r\nBcc: b@y", Body: "b"},
	} {
		if err := s.Send(context.Background(), m); err == nil {
			t.Errorf("accepted %+v", m)
		}
	}
	if f.saw("MAIL") {
		t.Error("something was sent")
	}
}

func TestOutbox(t *testing.T) {
	fake := &Fake{Block: make(chan struct{})}
	o := NewOutbox(fake)
	start := time.Now()
	for i := 0; i < 300; i++ {
		o.Send(Message{To: "a@example.com", Subject: "s", Body: "b"})
	}
	if time.Since(start) > time.Second {
		t.Fatal("Send blocked")
	}
	if o.Drain(50 * time.Millisecond) {
		t.Error("Drain returned true with a blocked sender")
	}
	o.Send(Message{To: "a@example.com"}) // after Drain: no panic
	close(fake.Block)
	if !o.Drain(5 * time.Second) {
		t.Fatal("Drain timed out after unblocking")
	}
	// One in the worker's hands plus a full queue; the rest were dropped.
	if n := len(fake.Sent()); n < queueSize || n > queueSize+1 {
		t.Errorf("sent %d, want %d or %d", n, queueSize, queueSize+1)
	}
}
