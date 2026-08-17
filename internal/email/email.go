package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/brand"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/observability"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type Message struct{ From, To, Subject, HTML, Text string }
type Sender interface {
	Send(context.Context, Message) (string, error)
}
type DevSender struct{}

func (DevSender) Send(_ context.Context, m Message) (string, error) {
	return "dev-" + uuid.NewString(), nil
}

type FileSender struct{ Dir string }

func (s FileSender) Send(_ context.Context, m Message) (string, error) {
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return "", err
	}
	id := "dev-" + uuid.NewString()
	body := []byte("To: " + m.To + "\nSubject: " + m.Subject + "\n\n" + m.Text + "\n")
	path := filepath.Join(s.Dir, id+".eml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return id, nil
}

type ResendSender struct {
	URL, APIKey string
	Client      *http.Client
}

type DeliveryError struct {
	Status    int
	Retryable bool
}

func (e *DeliveryError) Error() string { return fmt.Sprintf("email provider status %d", e.Status) }

func (s *ResendSender) Send(ctx context.Context, m Message) (string, error) {
	started := time.Now()
	ctx, finish := observability.StartSpan(ctx, "provider.resend.send")
	id, err := s.send(ctx, m)
	finish(err)
	observability.Provider("resend", observability.Outcome(err), time.Since(started))
	return id, err
}

func (s *ResendSender) send(ctx context.Context, m Message) (string, error) {
	payload := struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		HTML    string   `json:"html"`
		Text    string   `json:"text"`
	}{m.From, []string{m.To}, m.Subject, m.HTML, m.Text}
	b, _ := json.Marshal(payload)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(b))
	if e != nil {
		return "", e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	r, e := s.Client.Do(req)
	if e != nil {
		return "", e
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return "", &DeliveryError{Status: r.StatusCode, Retryable: r.StatusCode == http.StatusTooManyRequests || r.StatusCode >= 500}
	}
	var out struct {
		ID string `json:"id"`
	}
	if e := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&out); e != nil {
		return "", e
	}
	if out.ID == "" {
		return "", fmt.Errorf("email provider returned no message id")
	}
	return out.ID, nil
}

const GmailSendScope = "https://www.googleapis.com/auth/gmail.send"

type GmailSender struct {
	URL    string
	Client *http.Client
}

func NewGmailSender(ctx context.Context, credentialsJSON []byte, impersonatedUser, apiURL string) (*GmailSender, error) {
	impersonatedUser = strings.TrimSpace(impersonatedUser)
	if len(credentialsJSON) == 0 || impersonatedUser == "" {
		return nil, errors.New("gmail credentials and impersonated user are required")
	}
	impersonatedAddress, err := mail.ParseAddress(impersonatedUser)
	if err != nil || impersonatedAddress.Name != "" || !strings.EqualFold(impersonatedAddress.Address, impersonatedUser) {
		return nil, errors.New("invalid gmail impersonated user")
	}
	config, err := google.JWTConfigFromJSON(credentialsJSON, GmailSendScope)
	if err != nil {
		return nil, fmt.Errorf("parse gmail service account credentials: %w", err)
	}
	config.Subject = impersonatedUser
	if apiURL == "" {
		apiURL = "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"
	}
	client := config.Client(ctx)
	client.Timeout = 10 * time.Second
	return &GmailSender{URL: apiURL, Client: client}, nil
}

func (s *GmailSender) Send(ctx context.Context, message Message) (string, error) {
	started := time.Now()
	ctx, finish := observability.StartSpan(ctx, "provider.gmail.send")
	id, err := s.send(ctx, message)
	finish(err)
	observability.Provider("gmail", observability.Outcome(err), time.Since(started))
	return id, err
}

func (s *GmailSender) send(ctx context.Context, message Message) (string, error) {
	if s.Client == nil || strings.TrimSpace(s.URL) == "" {
		return "", errors.New("gmail sender is not configured")
	}
	raw, err := gmailRawMessage(message)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Raw string `json:"raw"`
	}{Raw: base64.RawURLEncoding.EncodeToString(raw)})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.Client.Do(request)
	if err != nil {
		var retrieveError *oauth2.RetrieveError
		if errors.As(err, &retrieveError) && retrieveError.Response != nil {
			return "", &DeliveryError{Status: retrieveError.Response.StatusCode, Retryable: gmailRetryable(retrieveError.Response.StatusCode, retrieveError.Body)}
		}
		return "", err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		return "", readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", &DeliveryError{Status: response.StatusCode, Retryable: gmailRetryable(response.StatusCode, body)}
	}
	var result struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.ID == "" {
		return "", errors.New("gmail returned no message id")
	}
	return result.ID, nil
}

func gmailRawMessage(message Message) ([]byte, error) {
	from, err := mail.ParseAddress(strings.TrimSpace(message.From))
	if err != nil {
		return nil, errors.New("invalid email from address")
	}
	to, err := mail.ParseAddress(strings.TrimSpace(message.To))
	if err != nil {
		return nil, errors.New("invalid email recipient address")
	}
	if strings.ContainsAny(message.Subject, "\r\n") {
		return nil, errors.New("invalid email subject")
	}
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	boundary := multipartWriter.Boundary()
	for _, alternative := range []struct {
		contentType string
		body        string
	}{{"text/plain", message.Text}, {"text/html", message.HTML}} {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", alternative.contentType+`; charset="UTF-8"`)
		header.Set("Content-Transfer-Encoding", "quoted-printable")
		part, createErr := multipartWriter.CreatePart(header)
		if createErr != nil {
			return nil, createErr
		}
		quoted := quotedprintable.NewWriter(part)
		if _, err = quoted.Write([]byte(alternative.body)); err != nil {
			return nil, err
		}
		if err = quoted.Close(); err != nil {
			return nil, err
		}
	}
	if err = multipartWriter.Close(); err != nil {
		return nil, err
	}
	var raw bytes.Buffer
	fmt.Fprintf(&raw, "From: %s\r\n", from.String())
	fmt.Fprintf(&raw, "To: %s\r\n", to.String())
	fmt.Fprintf(&raw, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", message.Subject))
	fmt.Fprintf(&raw, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&raw, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&raw, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	raw.Write(body.Bytes())
	return raw.Bytes(), nil
}

func gmailRetryable(status int, body []byte) bool {
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500 {
		return true
	}
	if status != http.StatusForbidden {
		return false
	}
	var response struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil {
		return false
	}
	for _, providerError := range response.Error.Errors {
		switch providerError.Reason {
		case "rateLimitExceeded", "userRateLimitExceeded", "backendError":
			return true
		}
	}
	return false
}

type Worker struct {
	db     *pgxpool.Pool
	sender Sender
	from   string
	key    []byte
	log    *slog.Logger
}

func NewWorker(db *pgxpool.Pool, s Sender, from string, key []byte, log *slog.Logger) *Worker {
	return &Worker{db, s, from, key, log}
}

type job struct {
	ID                  uuid.UUID
	Recipient, Template string
	Payload             []byte
	Attempts            int
	Locale              i18n.Locale
}

func (w *Worker) Run(ctx context.Context) error {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			for i := 0; i < 10; i++ {
				ok, e := w.once(ctx)
				if e != nil {
					w.log.Error("email worker iteration failed", "error", e)
					break
				}
				if !ok {
					break
				}
			}
		}
	}
}
func (w *Worker) once(ctx context.Context) (bool, error) {
	tx, e := w.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return false, e
	}
	defer tx.Rollback(ctx)
	var j job
	e = tx.QueryRow(ctx, `SELECT id,recipient,template,payload,attempts,locale::text FROM email_outbox WHERE ((status IN ('pending','failed') AND available_at<=now()) OR (status='processing' AND locked_at<now()-interval '5 minutes')) ORDER BY available_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&j.ID, &j.Recipient, &j.Template, &j.Payload, &j.Attempts, &j.Locale)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	_, e = tx.Exec(ctx, `UPDATE email_outbox SET status='processing',locked_at=now(),attempts=attempts+1 WHERE id=$1`, j.ID)
	if e != nil {
		return false, e
	}
	if e = tx.Commit(ctx); e != nil {
		return false, e
	}
	msg, e := w.render(j)
	shouldRetry := false
	if e == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var providerID string
		providerID, e = w.sender.Send(ctx, msg)
		shouldRetry = retryable(e)
		if e == nil {
			_, e = w.db.Exec(context.Background(), `WITH u AS (UPDATE email_outbox SET status='sent',sent_at=now(),provider_message_id=$2,last_error=NULL WHERE id=$1) INSERT INTO email_deliveries(outbox_id,provider,provider_message_id,success) VALUES($1,'configured',$2,true)`, j.ID, providerID)
			observability.Worker("email", observability.Outcome(e), false)
			return true, e
		}
	}
	delay := time.Duration(1<<min(j.Attempts, 8)) * time.Minute
	status := "failed"
	available := any(delay.String())
	if !shouldRetry || j.Attempts+1 >= 10 {
		available = "infinity"
	}
	safeError := "provider delivery failed"
	_, dbErr := w.db.Exec(context.Background(), `WITH u AS (UPDATE email_outbox SET status=$2,available_at=CASE WHEN $3='infinity' THEN 'infinity'::timestamptz ELSE now()+$3::interval END,last_error=$4 WHERE id=$1) INSERT INTO email_deliveries(outbox_id,provider,success,error_code) VALUES($1,'configured',false,'DELIVERY_FAILED')`, j.ID, status, available, safeError)
	if dbErr != nil {
		observability.Worker("email", "failure", shouldRetry)
		return true, dbErr
	}
	observability.Worker("email", "failure", shouldRetry)
	return true, e
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	var delivery *DeliveryError
	if errors.As(err, &delivery) {
		return delivery.Retryable
	}
	// Network and timeout failures are transient. Rendering/configuration errors are
	// marked permanent before this helper is consulted.
	return true
}
func (w *Worker) render(j job) (Message, error) {
	if j.Template != "login_code" {
		return Message{}, fmt.Errorf("unknown template")
	}
	var p struct {
		EncryptedCode  string `json:"encrypted_code"`
		ExpiresMinutes int    `json:"expires_minutes"`
	}
	if e := json.Unmarshal(j.Payload, &p); e != nil {
		return Message{}, e
	}
	code, e := security.Open(w.key, p.EncryptedCode)
	if e != nil {
		return Message{}, e
	}
	message, e := RenderLoginCode(j.Locale, code, p.ExpiresMinutes)
	if e != nil {
		return Message{}, e
	}
	message.From, message.To = w.from, j.Recipient
	return message, nil
}

func RenderLoginCode(locale i18n.Locale, code string, minutes int) (Message, error) {
	copy, ok := loginCodeTemplates[locale]
	if !ok {
		locale = i18n.DefaultLocale
		copy = loginCodeTemplates[locale]
	}
	copy = resolveLoginCodeCopy(copy, code, minutes)
	data := loginCodeTemplateData{Locale: string(locale), Brand: brand.ProductName, Code: code, Minutes: minutes, Copy: copy}
	var html, text bytes.Buffer
	if e := loginCodeHTMLTemplate.Execute(&html, data); e != nil {
		return Message{}, e
	}
	if e := loginCodeTextTemplate.Execute(&text, data); e != nil {
		return Message{}, e
	}
	return Message{Subject: copy.Subject, HTML: html.String(), Text: text.String()}, nil
}

func resolveLoginCodeCopy(copy localizedEmailTemplate, code string, minutes int) localizedEmailTemplate {
	values := []*string{&copy.Subject, &copy.Preheader, &copy.Eyebrow, &copy.Title, &copy.Intro, &copy.CodeLabel, &copy.Expiry, &copy.Security, &copy.Ignore, &copy.Footer}
	for _, value := range values {
		*value = strings.ReplaceAll(*value, "{{.Code}}", code)
		*value = strings.ReplaceAll(*value, "{{.Minutes}}", fmt.Sprint(minutes))
	}
	return copy
}

type localizedEmailTemplate struct {
	Subject, Preheader, Eyebrow, Title, Intro, CodeLabel, Expiry, Security, Ignore, Footer string
}

type loginCodeTemplateData struct {
	Locale  string
	Brand   string
	Code    string
	Minutes int
	Copy    localizedEmailTemplate
}

var loginCodeTemplates = map[i18n.Locale]localizedEmailTemplate{
	i18n.LocaleTR: {
		Subject: brand.ProductName + " giriş kodunuz", Preheader: "Giriş kodunuz {{.Code}}. {{.Minutes}} dakika geçerlidir.", Eyebrow: "GÜVENLİ GİRİŞ", Title: "Giriş kodunuz hazır", Intro: brand.ProductName + " hesabınıza giriş yapmak için aşağıdaki kodu kullanın.", CodeLabel: "TEK KULLANIMLIK KOD", Expiry: "Bu kod {{.Minutes}} dakika geçerlidir.", Security: "Kodu kimseyle paylaşmayın. Ekibimiz sizden bu kodu asla istemez.", Ignore: "Bu girişi siz istemediyseniz bu e-postayı yok sayabilirsiniz.", Footer: "Gerçek mağazaları, gerçek ziyaretlerle keşfedin.",
	},
	i18n.LocaleEN: {
		Subject: "Your " + brand.ProductName + " sign-in code", Preheader: "Your sign-in code is {{.Code}}. It is valid for {{.Minutes}} minutes.", Eyebrow: "SECURE SIGN-IN", Title: "Your sign-in code is ready", Intro: "Use the code below to sign in to your " + brand.ProductName + " account.", CodeLabel: "ONE-TIME CODE", Expiry: "This code is valid for {{.Minutes}} minutes.", Security: "Do not share this code. Our team will never ask you for it.", Ignore: "If you did not request this sign-in, you can safely ignore this email.", Footer: "Discover real stores through real visits.",
	},
	i18n.LocaleDE: {
		Subject: "Ihr " + brand.ProductName + " Anmeldecode", Preheader: "Ihr Anmeldecode lautet {{.Code}} und ist {{.Minutes}} Minuten gültig.", Eyebrow: "SICHERE ANMELDUNG", Title: "Ihr Anmeldecode ist bereit", Intro: "Verwenden Sie den folgenden Code, um sich bei Ihrem " + brand.ProductName + " Konto anzumelden.", CodeLabel: "EINMALCODE", Expiry: "Dieser Code ist {{.Minutes}} Minuten gültig.", Security: "Geben Sie diesen Code nicht weiter. Unser Team wird Sie niemals danach fragen.", Ignore: "Wenn Sie diese Anmeldung nicht angefordert haben, können Sie diese E-Mail ignorieren.", Footer: "Entdecken Sie echte Geschäfte durch echte Besuche.",
	},
	i18n.LocaleRU: {
		Subject: "Код входа в " + brand.ProductName, Preheader: "Ваш код входа: {{.Code}}. Он действует {{.Minutes}} минут.", Eyebrow: "БЕЗОПАСНЫЙ ВХОД", Title: "Ваш код для входа готов", Intro: "Используйте код ниже, чтобы войти в аккаунт " + brand.ProductName + ".", CodeLabel: "ОДНОРАЗОВЫЙ КОД", Expiry: "Код действует {{.Minutes}} минут.", Security: "Никому не сообщайте этот код. Наша команда никогда не попросит его назвать.", Ignore: "Если вы не запрашивали вход, просто проигнорируйте это письмо.", Footer: "Открывайте настоящие магазины благодаря реальным посещениям.",
	},
}

var loginCodeHTMLTemplate = template.Must(template.New("login-code-html").Parse(`<!doctype html>
<html lang="{{.Locale}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light dark">
  <meta name="supported-color-schemes" content="light dark">
  <title>{{.Copy.Subject}}</title>
  <style>
    :root { color-scheme: light dark; supported-color-schemes: light dark; }
    body, table, td, p, h1 { margin: 0; padding: 0; }
    table { border-collapse: collapse; border-spacing: 0; }
    img { border: 0; display: block; }
    .email-canvas { background-color: #F7F5F0 !important; }
    .email-surface { background-color: #FFFEFB !important; border-color: #D9D5CC !important; }
    .email-ink { color: #262521 !important; }
    .email-muted { color: #706D65 !important; }
    .email-line { border-color: #D9D5CC !important; }
    .email-code { background-color: #EFECE5 !important; border-color: #D9D5CC !important; }
    .email-code-value { color: #833522 !important; }
    .email-accent { color: #A34A32 !important; }
    @media (prefers-color-scheme: dark) {
      .email-canvas { background-color: #171714 !important; }
      .email-surface { background-color: #24231F !important; border-color: #48463F !important; }
      .email-ink { color: #F7F5F0 !important; }
      .email-muted { color: #C5C0B6 !important; }
      .email-line { border-color: #48463F !important; }
      .email-code { background-color: #302521 !important; border-color: #68463B !important; }
      .email-code-value { color: #F0A58F !important; }
      .email-accent { color: #F0A58F !important; }
    }
    [data-ogsc] .email-canvas { background-color: #171714 !important; }
    [data-ogsc] .email-surface { background-color: #24231F !important; border-color: #48463F !important; }
    [data-ogsc] .email-ink { color: #F7F5F0 !important; }
    [data-ogsc] .email-muted { color: #C5C0B6 !important; }
    [data-ogsc] .email-line { border-color: #48463F !important; }
    [data-ogsc] .email-code { background-color: #302521 !important; border-color: #68463B !important; }
    [data-ogsc] .email-code-value { color: #F0A58F !important; }
    [data-ogsc] .email-accent { color: #F0A58F !important; }
    @media only screen and (max-width: 620px) {
      .email-shell { width: 100% !important; }
      .email-gutter { padding-left: 20px !important; padding-right: 20px !important; }
      .email-card { padding: 32px 24px !important; }
      .email-title { font-size: 27px !important; line-height: 34px !important; }
      .email-code-value { font-size: 30px !important; letter-spacing: 7px !important; }
    }
  </style>
</head>
<body class="email-canvas" style="margin:0; padding:0; width:100%; background-color:#F7F5F0; -webkit-text-size-adjust:100%; -ms-text-size-adjust:100%;">
  <div style="display:none; max-height:0; overflow:hidden; opacity:0; color:transparent; mso-hide:all;">{{.Copy.Preheader}}</div>
  <table role="presentation" width="100%" class="email-canvas" bgcolor="#F7F5F0" style="width:100%; background-color:#F7F5F0;">
    <tr>
      <td align="center" class="email-gutter" style="padding:40px 24px;">
        <table role="presentation" width="600" class="email-shell" style="width:600px; max-width:600px;">
          <tr>
            <td style="padding:0 0 20px 0;">
              <table role="presentation">
                <tr>
                  <td width="4" bgcolor="#A34A32" style="width:4px; background-color:#A34A32; font-size:0; line-height:0;">&nbsp;</td>
                  <td class="email-ink" style="padding-left:12px; color:#262521; font-family:Arial,'Helvetica Neue',sans-serif; font-size:20px; line-height:26px; font-weight:700; letter-spacing:-0.2px;">{{.Brand}}</td>
                </tr>
              </table>
            </td>
          </tr>
          <tr>
            <td class="email-surface email-card" bgcolor="#FFFEFB" style="padding:48px 48px 44px 48px; background-color:#FFFEFB; border:1px solid #D9D5CC;">
              <p class="email-accent" style="margin:0 0 12px 0; color:#A34A32; font-family:Arial,'Helvetica Neue',sans-serif; font-size:12px; line-height:16px; font-weight:700; letter-spacing:1.4px;">{{.Copy.Eyebrow}}</p>
              <h1 class="email-ink email-title" style="margin:0; color:#262521; font-family:Georgia,'Times New Roman',serif; font-size:32px; line-height:39px; font-weight:700; letter-spacing:-0.4px;">{{.Copy.Title}}</h1>
              <p class="email-muted" style="margin:16px 0 0 0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:16px; line-height:25px;">{{.Copy.Intro}}</p>
              <table role="presentation" width="100%" style="width:100%; margin-top:32px;">
                <tr>
                  <td class="email-code" align="center" bgcolor="#EFECE5" style="padding:22px 16px; background-color:#EFECE5; border:1px solid #D9D5CC; border-radius:6px;">
                    <p class="email-muted" style="margin:0 0 8px 0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:11px; line-height:16px; font-weight:700; letter-spacing:1.2px;">{{.Copy.CodeLabel}}</p>
                    <p class="email-code-value" style="margin:0; color:#833522; font-family:'Courier New',Courier,monospace; font-size:34px; line-height:42px; font-weight:700; letter-spacing:9px; white-space:nowrap;">{{.Code}}</p>
                  </td>
                </tr>
              </table>
              <p class="email-ink" style="margin:24px 0 0 0; color:#262521; font-family:Arial,'Helvetica Neue',sans-serif; font-size:15px; line-height:23px; font-weight:700;">{{.Copy.Expiry}}</p>
              <p class="email-muted" style="margin:8px 0 0 0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:14px; line-height:22px;">{{.Copy.Security}}</p>
              <table role="presentation" width="100%" style="width:100%; margin-top:28px;">
                <tr><td class="email-line" style="border-top:1px solid #D9D5CC; font-size:0; line-height:0;">&nbsp;</td></tr>
              </table>
              <p class="email-muted" style="margin:20px 0 0 0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:13px; line-height:20px;">{{.Copy.Ignore}}</p>
            </td>
          </tr>
          <tr>
            <td align="center" style="padding:22px 20px 0 20px;">
              <p class="email-muted" style="margin:0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:12px; line-height:18px;">{{.Copy.Footer}}</p>
              <p class="email-muted" style="margin:4px 0 0 0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:12px; line-height:18px;">bosagezme.com</p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`))

var loginCodeTextTemplate = template.Must(template.New("login-code-text").Parse(`{{.Brand}}

{{.Copy.Title}}
{{.Copy.Intro}}

{{.Copy.CodeLabel}}
{{.Code}}

{{.Copy.Expiry}}
{{.Copy.Security}}

{{.Copy.Ignore}}

{{.Copy.Footer}}
bosagezme.com`))
