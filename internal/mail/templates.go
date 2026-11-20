package mail

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
)

//go:embed templates/*.tmpl
var templateFiles embed.FS

var (
	textTemplates = texttemplate.Must(texttemplate.ParseFS(templateFiles, "templates/*.txt.tmpl"))
	htmlTemplates = htmltemplate.Must(htmltemplate.ParseFS(templateFiles, "templates/*.html.tmpl"))
)

// LoginData fills the login email.
type LoginData struct {
	Code         string
	ValidMinutes int
	ServiceName  string
	ServiceURL   string
}

// DigestItem is one deleted article in a notification digest.
type DigestItem struct {
	Title       string
	AccountName string
	Reason      string // WeChat's own wording
	Link        string // where to read the backup
}

// DigestData fills the notification email.
type DigestData struct {
	Items       []DigestItem
	Deleted     int
	ServiceName string
	ServiceURL  string
	// LastThisMonth: this email uses up the account's MonthlyLimit.
	MonthlyLimit  int
	LastThisMonth bool
}

// AlertData fills an operator alert.
type AlertData struct {
	Subject string
	Body    string
}

// Render builds a message from the named template pair (name.txt.tmpl and,
// if it exists, name.html.tmpl). The subject is the text template's first
// line, prefixed "Subject: ".
func Render(name, to string, data any) (Message, error) {
	var text bytes.Buffer
	if err := textTemplates.ExecuteTemplate(&text, name+".txt.tmpl", data); err != nil {
		return Message{}, err
	}
	subject, body, _ := strings.Cut(text.String(), "\n")
	m := Message{
		To:      to,
		Subject: strings.TrimSpace(strings.TrimPrefix(subject, "Subject:")),
		Text:    strings.TrimLeft(body, "\n"),
	}
	if t := htmlTemplates.Lookup(name + ".html.tmpl"); t != nil {
		var html bytes.Buffer
		if err := t.Execute(&html, data); err != nil {
			return Message{}, err
		}
		m.HTML = html.String()
	}
	return m, nil
}
