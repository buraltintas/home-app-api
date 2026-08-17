package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/burakaltintas/home-app-api/internal/brand"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/observability"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	data := struct {
		Code    string
		Minutes int
	}{code, minutes}
	tpl, ok := loginCodeTemplates[locale]
	if !ok {
		tpl = loginCodeTemplates[i18n.DefaultLocale]
	}
	var html, text bytes.Buffer
	if e := template.Must(template.New("html").Parse(tpl.HTML)).Execute(&html, data); e != nil {
		return Message{}, e
	}
	if e := template.Must(template.New("text").Parse(tpl.Text)).Execute(&text, data); e != nil {
		return Message{}, e
	}
	return Message{Subject: tpl.Subject, HTML: html.String(), Text: text.String()}, nil
}

type localizedEmailTemplate struct{ Subject, HTML, Text string }

var loginCodeTemplates = map[i18n.Locale]localizedEmailTemplate{
	i18n.LocaleTR: {brand.ProductName + " giriş kodunuz", `<h1>` + brand.ProductName + ` giriş kodunuz</h1><p><strong>{{.Code}}</strong></p><p>Bu kod {{.Minutes}} dakika geçerlidir. Kodu kimseyle paylaşmayın.</p>`, brand.ProductName + " giriş kodunuz: {{.Code}}\nBu kod {{.Minutes}} dakika geçerlidir. Kodu kimseyle paylaşmayın."},
	i18n.LocaleEN: {"Your " + brand.ProductName + " sign-in code", `<h1>Your ` + brand.ProductName + ` sign-in code</h1><p><strong>{{.Code}}</strong></p><p>This code is valid for {{.Minutes}} minutes. Do not share it with anyone.</p>`, "Your " + brand.ProductName + " sign-in code: {{.Code}}\nThis code is valid for {{.Minutes}} minutes. Do not share it with anyone."},
	i18n.LocaleDE: {"Ihr " + brand.ProductName + " Anmeldecode", `<h1>Ihr ` + brand.ProductName + ` Anmeldecode</h1><p><strong>{{.Code}}</strong></p><p>Dieser Code ist {{.Minutes}} Minuten gültig. Geben Sie ihn nicht weiter.</p>`, "Ihr " + brand.ProductName + " Anmeldecode: {{.Code}}\nDieser Code ist {{.Minutes}} Minuten gültig. Geben Sie ihn nicht weiter."},
	i18n.LocaleRU: {"Код входа в " + brand.ProductName, `<h1>Код входа в ` + brand.ProductName + `</h1><p><strong>{{.Code}}</strong></p><p>Код действителен {{.Minutes}} минут. Никому его не сообщайте.</p>`, "Код входа в " + brand.ProductName + ": {{.Code}}\nКод действителен {{.Minutes}} минут. Никому его не сообщайте."},
}
