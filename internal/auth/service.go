package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	OTPTTL          time.Duration
	OTPMaxAttempts  int
	OTPEmailLimit   int
	OTPIPLimit      int
	OTPVisitorLimit int
	VisitorTTL      time.Duration
	RefreshTTL      time.Duration
	HashKey         []byte
	AppReviewEmail  string
	AppReviewCode   string
}
type Service struct {
	db     *pgxpool.Pool
	cfg    Config
	tokens *security.TokenManager
	google GoogleVerifier
	report *reporting.Service
	now    func() time.Time
}

func NewService(db *pgxpool.Pool, c Config, t *security.TokenManager, g GoogleVerifier, report *reporting.Service) *Service {
	return &Service{db: db, cfg: c, tokens: t, google: g, report: report, now: time.Now}
}

type TokenPair struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	TokenType        string    `json:"token_type"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	UserID           uuid.UUID `json:"user_id"`
	SessionID        uuid.UUID `json:"-"`
}
type Client struct {
	Type     string
	Metadata map[string]any
}

func NormalizeEmail(raw string) (string, error) {
	a, e := mail.ParseAddress(strings.TrimSpace(raw))
	if e != nil {
		return "", httpapi.ErrInvalidInput
	}
	s := strings.ToLower(strings.TrimSpace(a.Address))
	if len(s) > 254 {
		return "", httpapi.ErrInvalidInput
	}
	return s, nil
}

func (s *Service) RequestCode(ctx context.Context, email string, visitor *uuid.UUID, ipHash []byte) error {
	norm, e := NormalizeEmail(email)
	if e != nil {
		return e
	}
	isAppReview := s.cfg.AppReviewCode != "" && strings.EqualFold(norm, strings.TrimSpace(s.cfg.AppReviewEmail))
	code := s.cfg.AppReviewCode
	cipher := ""
	if !isAppReview {
		code, e = security.NumericCode(6)
		if e != nil {
			return e
		}
		cipher, e = security.Seal(s.cfg.HashKey, code)
		if e != nil {
			return e
		}
	}
	now := s.now()
	locale := i18n.FromContext(ctx)
	id := uuid.New()
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if visitor != nil {
		if _, e = tx.Exec(ctx, `INSERT INTO visitor_sessions(id,expires_at,locale) VALUES($1,now()+$2::interval,$3) ON CONFLICT(id) DO UPDATE SET last_seen_at=now(),expires_at=greatest(visitor_sessions.expires_at,excluded.expires_at),locale=excluded.locale`, *visitor, s.cfg.VisitorTTL.String(), locale); e != nil {
			return e
		}
	}
	var emailCount, ipCount, visitorCount int
	e = tx.QueryRow(ctx, `SELECT
	 count(*) FILTER(WHERE normalized_email=$1 AND created_at>=now()-interval '10 minutes'),
	 count(*) FILTER(WHERE $2::bytea IS NOT NULL AND request_ip_hash=$2 AND created_at>=now()-interval '1 hour'),
	 count(*) FILTER(WHERE $3::uuid IS NOT NULL AND visitor_session_id=$3 AND created_at>=now()-interval '1 hour')
	 FROM email_verification_codes WHERE created_at>=now()-interval '1 hour'`, norm, nilBytes(ipHash), visitor).Scan(&emailCount, &ipCount, &visitorCount)
	if e != nil {
		return e
	}
	if emailCount >= s.cfg.OTPEmailLimit || ipCount >= s.cfg.OTPIPLimit || visitorCount >= s.cfg.OTPVisitorLimit {
		return httpapi.E(429, "RATE_LIMITED", "Too many verification code requests")
	}
	_, e = tx.Exec(ctx, `UPDATE email_verification_codes SET invalidated_at=$2 WHERE normalized_email=$1 AND consumed_at IS NULL AND invalidated_at IS NULL`, norm, now)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO email_verification_codes(id,normalized_email,code_hash,visitor_session_id,request_ip_hash,max_attempts,expires_at,locale) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, norm, security.Hash(s.cfg.HashKey, code), visitor, ipHash, s.cfg.OTPMaxAttempts, now.Add(s.cfg.OTPTTL), locale)
	if e != nil {
		return e
	}
	if !isAppReview {
		payload, _ := json.Marshal(map[string]any{"encrypted_code": cipher, "expires_minutes": int(s.cfg.OTPTTL.Minutes())})
		_, e = tx.Exec(ctx, `INSERT INTO email_outbox(idempotency_key,template,recipient,payload,locale) VALUES($1,'login_code',$2,$3,$4)`, "otp:"+id.String(), norm, payload, locale)
		if e != nil {
			return e
		}
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.OTPRequested, IdempotencyKey: "otp-request:" + id.String(), VisitorSessionID: visitor}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Service) VerifyCode(ctx context.Context, email, code string, client Client) (TokenPair, error) {
	for attempt := 0; attempt < 3; attempt++ {
		pair, err := s.verifyCodeOnce(ctx, email, code, client)
		if err == nil {
			return pair, nil
		}
		if !identityRace(err) || attempt == 2 {
			return TokenPair{}, err
		}
	}
	return TokenPair{}, httpapi.E(500, "INTERNAL_ERROR", "Unexpected authentication failure")
}

func (s *Service) verifyCodeOnce(ctx context.Context, email, code string, client Client) (TokenPair, error) {
	norm, e := NormalizeEmail(email)
	if e != nil {
		return TokenPair{}, e
	}
	if len(code) != 6 {
		_, _ = s.report.Record(ctx, reporting.Event{Type: reporting.OTPVerificationFailed, IdempotencyKey: "otp-invalid-format:" + uuid.NewString()})
		return TokenPair{}, httpapi.E(401, "INVALID_CODE", "The verification code is invalid or expired")
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if e != nil {
		return TokenPair{}, e
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	var hash []byte
	var attempts, max int
	var expires time.Time
	var verificationLocale i18n.Locale
	e = tx.QueryRow(ctx, `SELECT id,code_hash,attempts,max_attempts,expires_at,locale::text FROM email_verification_codes WHERE normalized_email=$1 AND consumed_at IS NULL AND invalidated_at IS NULL ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, norm).Scan(&id, &hash, &attempts, &max, &expires, &verificationLocale)
	invalid := httpapi.E(401, "INVALID_CODE", "The verification code is invalid or expired")
	if errors.Is(e, pgx.ErrNoRows) || s.now().After(expires) || attempts >= max {
		_, _ = s.report.Record(ctx, reporting.Event{Type: reporting.OTPVerificationFailed, IdempotencyKey: "otp-failed:" + uuid.NewString(), VisitorSessionID: nil})
		return TokenPair{}, invalid
	}
	if e != nil {
		return TokenPair{}, e
	}
	if !security.EqualHash(hash, security.Hash(s.cfg.HashKey, code)) {
		_, _ = tx.Exec(ctx, `UPDATE email_verification_codes SET attempts=attempts+1 WHERE id=$1`, id)
		_, _ = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.OTPVerificationFailed, IdempotencyKey: fmt.Sprintf("otp-failed:%s:%d", id, attempts+1)})
		if e = tx.Commit(ctx); e != nil {
			return TokenPair{}, e
		}
		return TokenPair{}, invalid
	}
	if _, e = tx.Exec(ctx, `UPDATE email_verification_codes SET consumed_at=$2 WHERE id=$1`, id, s.now()); e != nil {
		return TokenPair{}, e
	}
	user, created, e := s.resolveIdentity(i18n.WithLocale(ctx, verificationLocale), tx, "email", norm, norm, true)
	if e != nil {
		return TokenPair{}, e
	}
	pair, e := s.createSession(ctx, tx, user, client)
	if e != nil {
		return TokenPair{}, e
	}
	if created {
		if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.UserRegistered, IdempotencyKey: "user-registered:" + user.String(), UserID: &user}); e != nil {
			return TokenPair{}, e
		}
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.UserLoginSucceeded, IdempotencyKey: "login:" + pair.SessionID.String(), UserID: &user}); e != nil {
		return TokenPair{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return TokenPair{}, e
	}
	return pair, nil
}

func (s *Service) Google(ctx context.Context, idToken string, client Client) (TokenPair, error) {
	g, e := s.google.Verify(ctx, idToken)
	if e != nil {
		_, _ = s.report.Record(ctx, reporting.Event{Type: reporting.UserLoginFailed, IdempotencyKey: "google-login-failed:" + uuid.NewString()})
		return TokenPair{}, httpapi.E(401, "INVALID_GOOGLE_TOKEN", "Invalid Google credential")
	}
	norm, e := NormalizeEmail(g.Email)
	if e != nil {
		return TokenPair{}, e
	}
	for attempt := 0; attempt < 3; attempt++ {
		pair, err := s.googleIdentity(ctx, g, norm, client)
		if err == nil {
			return pair, nil
		}
		if !identityRace(err) || attempt == 2 {
			return TokenPair{}, err
		}
	}
	return TokenPair{}, e
}

func (s *Service) googleIdentity(ctx context.Context, g GoogleIdentity, norm string, client Client) (TokenPair, error) {
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if e != nil {
		return TokenPair{}, e
	}
	defer tx.Rollback(ctx)
	user, created, e := s.resolveIdentity(ctx, tx, "google", g.Subject, norm, g.EmailVerified)
	if e != nil {
		return TokenPair{}, e
	}
	pair, e := s.createSession(ctx, tx, user, client)
	if e != nil {
		return TokenPair{}, e
	}
	if created {
		if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.UserRegistered, IdempotencyKey: "user-registered:" + user.String(), UserID: &user}); e != nil {
			return TokenPair{}, e
		}
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.UserLoginSucceeded, IdempotencyKey: "login:" + pair.SessionID.String(), UserID: &user}); e != nil {
		return TokenPair{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return TokenPair{}, e
	}
	return pair, nil
}

func identityRace(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01" || pgErr.Code == "23505")
}

func (s *Service) resolveIdentity(ctx context.Context, tx pgx.Tx, provider, subject, email string, verified bool) (uuid.UUID, bool, error) {
	var user uuid.UUID
	var status string
	e := tx.QueryRow(ctx, `SELECT i.user_id,u.status FROM auth_identities i JOIN users u ON u.id=i.user_id WHERE i.provider=$1 AND i.provider_subject=$2 FOR UPDATE OF u`, provider, subject).Scan(&user, &status)
	if e == nil {
		if status == "inactive" {
			if e = reactivateUser(ctx, tx, user, email); e != nil {
				return uuid.Nil, false, e
			}
		} else if status != "active" {
			return uuid.Nil, false, httpapi.E(403, "ACCOUNT_UNAVAILABLE", "This account is unavailable")
		}
		return user, false, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return uuid.Nil, false, e
	}
	if !verified {
		return uuid.Nil, false, httpapi.E(401, "EMAIL_NOT_VERIFIED", "A verified email is required")
	}
	// One transaction-scoped lock per normalized email serializes cross-provider signup.
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, email); e != nil {
		return uuid.Nil, false, e
	}
	var existingStatus string
	e = tx.QueryRow(ctx, `SELECT id,status FROM users WHERE primary_email=$1 AND status IN ('active','inactive') FOR UPDATE`, email).Scan(&user, &existingStatus)
	created := false
	if errors.Is(e, pgx.ErrNoRows) {
		user = uuid.New()
		locale := i18n.FromContext(ctx)
		if _, e = tx.Exec(ctx, `INSERT INTO users(id,primary_email,preferred_locale) VALUES($1,$2,$3)`, user, email, locale); e != nil {
			return uuid.Nil, false, e
		}
		_, e = tx.Exec(ctx, `INSERT INTO user_profiles(user_id,username,display_name) VALUES($1,$2,$3)`, user, "user_"+strings.ReplaceAll(user.String()[:8], "-", ""), strings.Split(email, "@")[0])
		if e != nil {
			return uuid.Nil, false, e
		}
		// Enqueued in the same transaction that creates the account, so a rolled back
		// signup can never leave a welcome mail behind and the login response never waits
		// on the mail provider. The user id keys the row, which makes the retried
		// serializable transaction idempotent.
		if _, e = tx.Exec(ctx, `INSERT INTO email_outbox(idempotency_key,template,recipient,payload,locale) VALUES($1,'welcome',$2,'{}'::jsonb,$3) ON CONFLICT(idempotency_key) DO NOTHING`, "welcome:"+user.String(), email, locale); e != nil {
			return uuid.Nil, false, e
		}
		created = true
	} else if e != nil {
		return uuid.Nil, false, e
	} else if existingStatus == "inactive" {
		if e = reactivateUser(ctx, tx, user, email); e != nil {
			return uuid.Nil, false, e
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO auth_identities(user_id,provider,provider_subject,normalized_email,email_verified) VALUES($1,$2,$3,$4,true)`, user, provider, subject, email)
	return user, created, e
}

func reactivateUser(ctx context.Context, tx pgx.Tx, user uuid.UUID, email string) error {
	username := "user_" + strings.ReplaceAll(user.String()[:8], "-", "")
	displayName := strings.Split(email, "@")[0]
	if _, err := tx.Exec(ctx, `UPDATE users SET primary_email=$2,status='active',preferred_locale=$3,deleted_at=NULL,updated_at=now() WHERE id=$1 AND status='inactive'`, user, email, i18n.FromContext(ctx)); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO user_profiles(user_id,username,display_name) VALUES($1,$2,$3) ON CONFLICT(user_id) DO UPDATE SET username=excluded.username,display_name=excluded.display_name,avatar_url=NULL,bio=NULL,bio_language=NULL,city=NULL,updated_at=now()`, user, username, displayName)
	return err
}

// LinkVisitor associates anonymous product activity after a successful login.
// The identifier is analytics-only: it never grants access to either account.
func (s *Service) LinkVisitor(ctx context.Context, user, visitor uuid.UUID) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var linked uuid.UUID
	e = tx.QueryRow(ctx, `UPDATE visitor_sessions SET linked_user_id=$1,last_seen_at=now() WHERE id=$2 AND (linked_user_id IS NULL OR linked_user_id=$1) RETURNING id`, user, visitor).Scan(&linked)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE searches SET user_id=$1 WHERE visitor_session_id=$2 AND user_id IS NULL`, user, visitor); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func nilBytes(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func (s *Service) createSession(ctx context.Context, tx pgx.Tx, user uuid.UUID, client Client) (TokenPair, error) {
	raw, e := security.RandomToken(32)
	if e != nil {
		return TokenPair{}, e
	}
	now := s.now()
	sid, family := uuid.New(), uuid.New()
	meta, _ := json.Marshal(client.Metadata)
	exp := now.Add(s.cfg.RefreshTTL)
	_, e = tx.Exec(ctx, `INSERT INTO auth_sessions(id,user_id,family_id,refresh_token_hash,client_type,device_metadata,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, sid, user, family, security.Hash(s.cfg.HashKey, raw), client.Type, meta, exp)
	if e != nil {
		return TokenPair{}, e
	}
	access, aexp, e := s.tokens.Access(user, sid, now)
	return TokenPair{AccessToken: access, RefreshToken: raw, TokenType: "Bearer", AccessExpiresAt: aexp, RefreshExpiresAt: exp, UserID: user, SessionID: sid}, e
}

func (s *Service) Refresh(ctx context.Context, raw string, client Client) (TokenPair, error) {
	if len(raw) < 32 {
		return TokenPair{}, httpapi.E(401, "INVALID_REFRESH_TOKEN", "Invalid refresh token")
	}
	hash := security.Hash(s.cfg.HashKey, raw)
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return TokenPair{}, e
	}
	defer tx.Rollback(ctx)
	var old, user, family uuid.UUID
	var expires time.Time
	var revoked *time.Time
	var replaced *uuid.UUID
	e = tx.QueryRow(ctx, `SELECT id,user_id,family_id,expires_at,revoked_at,replaced_by FROM auth_sessions WHERE refresh_token_hash=$1 FOR UPDATE`, hash).Scan(&old, &user, &family, &expires, &revoked, &replaced)
	invalid := httpapi.E(401, "INVALID_REFRESH_TOKEN", "Invalid refresh token")
	if errors.Is(e, pgx.ErrNoRows) || s.now().After(expires) {
		return TokenPair{}, invalid
	}
	if e != nil {
		return TokenPair{}, e
	}
	if revoked != nil || replaced != nil {
		_, _ = tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at=coalesce(revoked_at,now()),revoke_reason='reuse_detected' WHERE family_id=$1 AND revoked_at IS NULL`, family)
		_ = tx.Commit(ctx)
		return TokenPair{}, invalid
	}
	newRaw, e := security.RandomToken(32)
	if e != nil {
		return TokenPair{}, e
	}
	sid := uuid.New()
	now := s.now()
	newExp := now.Add(s.cfg.RefreshTTL)
	meta, _ := json.Marshal(client.Metadata)
	_, e = tx.Exec(ctx, `INSERT INTO auth_sessions(id,user_id,family_id,refresh_token_hash,client_type,device_metadata,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, sid, user, family, security.Hash(s.cfg.HashKey, newRaw), client.Type, meta, newExp)
	if e != nil {
		return TokenPair{}, e
	}
	_, e = tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at=$2,revoke_reason='rotated',replaced_by=$3,last_used_at=$2 WHERE id=$1`, old, now, sid)
	if e != nil {
		return TokenPair{}, e
	}
	access, aexp, e := s.tokens.Access(user, sid, now)
	if e != nil {
		return TokenPair{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return TokenPair{}, e
	}
	return TokenPair{AccessToken: access, RefreshToken: newRaw, TokenType: "Bearer", AccessExpiresAt: aexp, RefreshExpiresAt: newExp, UserID: user, SessionID: sid}, nil
}

func (s *Service) Logout(ctx context.Context, user, session uuid.UUID, all bool) error {
	q := `UPDATE auth_sessions SET revoked_at=now(),revoke_reason='logout' WHERE user_id=$1 AND revoked_at IS NULL`
	args := []any{user}
	if !all {
		q += ` AND family_id=(SELECT family_id FROM auth_sessions WHERE id=$2 AND user_id=$1)`
		args = append(args, session)
	}
	_, e := s.db.Exec(ctx, q, args...)
	return e
}

func IPHash(key []byte, ip string) []byte { return security.Hash(key, ip) }
func Unexpected(err error) error          { return fmt.Errorf("auth: %w", err) }
