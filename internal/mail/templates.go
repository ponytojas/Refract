package mail

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed templates/*/*.txt
var files embed.FS

type Kind string

const (
	Verify  Kind = "verify"
	Reset   Kind = "reset"
	Deleted Kind = "deleted"
)

// Data is what the templates may use.
type Data struct {
	Email, Link string
	Site        string // REFRACT_PUBLIC_URL's host
	SignUp      bool   // reset: someone tried to sign up with this address
}

// Compose renders a message in lang ("es"; anything else is English). The
// first line of a template is "Subject: …", then a blank line, then the body.
func Compose(k Kind, lang, to string, d Data) (Message, error) {
	if lang != "es" {
		lang = "en"
	}
	t, err := template.New(string(k)).Option("missingkey=error").ParseFS(files, fmt.Sprintf("templates/%s/%s.txt", lang, k))
	if err != nil {
		return Message{}, err
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, string(k)+".txt", d); err != nil {
		return Message{}, err
	}
	head, body, ok := strings.Cut(buf.String(), "\n\n")
	subject, found := strings.CutPrefix(head, "Subject: ")
	if !ok || !found {
		return Message{}, fmt.Errorf("template %s/%s has no subject", lang, k)
	}
	return Message{To: to, Subject: strings.TrimSpace(subject), Body: strings.TrimLeft(body, "\n")}, nil
}
