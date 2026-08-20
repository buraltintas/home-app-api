package email

import (
	"bytes"
	"encoding/json"
	"html"
	"strings"
	"text/template"

	"github.com/burakaltintas/home-app-api/internal/brand"
)

// The feedback notice goes to us, not to a user, so it is written as an operator's email:
// plain, complete, and readable in a notification preview. It carries the message itself
// rather than a link, because the point is to read it without opening anything.
type feedbackPayload struct {
	Kind         string `json:"kind"`
	Message      string `json:"message"`
	ContactEmail string `json:"contact_email"`
	Author       string `json:"author"`
	Locale       string `json:"locale"`
}

var feedbackKindLabels = map[string]string{
	"suggestion": "Öneri",
	"problem":    "Sorun",
	"praise":     "Beğeni",
	"other":      "Diğer",
}

func renderFeedback(payload []byte) (Message, error) {
	var p feedbackPayload
	if e := json.Unmarshal(payload, &p); e != nil {
		return Message{}, e
	}
	kind := feedbackKindLabels[p.Kind]
	if kind == "" {
		kind = p.Kind
	}
	from := p.Author
	if from == "" {
		from = p.ContactEmail
	}
	if from == "" {
		from = "anonim"
	}
	subject := brand.ProductName + " · yeni geri bildirim: " + kind

	var text bytes.Buffer
	text.WriteString(kind + " — " + from + "\n")
	if p.Locale != "" {
		text.WriteString("Dil: " + strings.ToUpper(p.Locale) + "\n")
	}
	if p.ContactEmail != "" {
		text.WriteString("Yanıt adresi: " + p.ContactEmail + "\n")
	}
	text.WriteString("\n" + p.Message + "\n")

	var out bytes.Buffer
	if e := feedbackHTMLTemplate.Execute(&out, map[string]string{
		"Brand": brand.ProductName, "Tagline": brand.Tagline, "Kind": kind, "From": from,
		"Locale": strings.ToUpper(p.Locale), "Contact": p.ContactEmail,
		"Message": html.EscapeString(p.Message),
	}); e != nil {
		return Message{}, e
	}
	return Message{Subject: subject, HTML: out.String(), Text: text.String()}, nil
}

// Deliberately plain markup. This is read once, by one person, usually on a phone.
var feedbackHTMLTemplate = template.Must(template.New("feedback").Parse(`<!doctype html>
<html lang="tr"><head><meta charset="utf-8"><title>{{.Kind}}</title></head>
<body style="margin:0; padding:24px; background-color:#faf8f4; font-family:Arial,'Helvetica Neue',sans-serif; color:#16140f;">
  <div style="max-width:600px; margin:0 auto;">
    <p style="margin:0 0 4px 0; font-size:18px; font-weight:700;">{{.Brand}}</p>
    <p style="margin:0 0 20px 0; font-size:13px; color:#6B6559;">{{.Tagline}}</p>
    <div style="background-color:#ffffff; border:1px solid #E4E0D7; border-radius:12px; padding:20px;">
      <p style="margin:0 0 6px 0; font-size:12px; font-weight:700; letter-spacing:1.2px; text-transform:uppercase; color:#C2452D;">{{.Kind}}</p>
      <p style="margin:0 0 16px 0; font-size:14px; color:#6B6559;">{{.From}}{{if .Locale}} · {{.Locale}}{{end}}</p>
      <p style="margin:0; font-size:16px; line-height:26px; white-space:pre-wrap;">{{.Message}}</p>
      {{if .Contact}}<p style="margin:20px 0 0 0; font-size:14px;">Yanıt: <a href="mailto:{{.Contact}}" style="color:#16140f;">{{.Contact}}</a></p>{{end}}
    </div>
  </div>
</body></html>`))
