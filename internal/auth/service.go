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
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	OTPTTL         time.Duration
	OTPMaxAttempts int
	RefreshTTL     time.Duration
	HashKey        []byte
}
type Service struct {
	db     *pgxpool.Pool
	cfg    Config
	tokens *security.TokenManager
	google GoogleVerifier
	now    func() time.Time
}

func NewService(db *pgxpool.Pool, c Config, t *security.TokenManager, g GoogleVerifier) *Service {
	return &Service{db: db, cfg: c, tokens: t, google: g, now: time.Now}
}

type TokenPair struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	TokenType        string    `json:"token_type"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	UserID           uuid.UUID `json:"user_id"`
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
	code, e := security.NumericCode(6)
	if e != nil {
		return e
	}
	cipher, e := security.Seal(s.cfg.HashKey, code)
	if e != nil {
		return e
	}
	now := s.now()
	id := uuid.New()
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `UPDATE email_verification_codes SET invalidated_at=$2 WHERE normalized_email=$1 AND consumed_at IS NULL AND invalidated_at IS NULL`, norm, now)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO email_verification_codes(id,normalized_email,code_hash,visitor_session_id,request_ip_hash,max_attempts,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, norm, security.Hash(s.cfg.HashKey, code), visitor, ipHash, s.cfg.OTPMaxAttempts, now.Add(s.cfg.OTPTTL))
	if e != nil {
		return e
	}
	payload, _ := json.Marshal(map[string]any{"encrypted_code": cipher, "expires_minutes": int(s.cfg.OTPTTL.Minutes())})
	_, e = tx.Exec(ctx, `INSERT INTO email_outbox(idempotency_key,template,recipient,payload) VALUES($1,'login_code',$2,$3)`, "otp:"+id.String(), norm, payload)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Service) VerifyCode(ctx context.Context, email, code string, client Client) (TokenPair, error) {
	norm, e := NormalizeEmail(email)
	if e != nil {
		return TokenPair{}, e
	}
	if len(code) != 6 {
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
	e = tx.QueryRow(ctx, `SELECT id,code_hash,attempts,max_attempts,expires_at FROM email_verification_codes WHERE normalized_email=$1 AND consumed_at IS NULL AND invalidated_at IS NULL ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, norm).Scan(&id, &hash, &attempts, &max, &expires)
	invalid := httpapi.E(401, "INVALID_CODE", "The verification code is invalid or expired")
	if errors.Is(e, pgx.ErrNoRows) || s.now().After(expires) || attempts >= max {
		return TokenPair{}, invalid
	}
	if e != nil {
		return TokenPair{}, e
	}
	if !security.EqualHash(hash, security.Hash(s.cfg.HashKey, code)) {
		_, _ = tx.Exec(ctx, `UPDATE email_verification_codes SET attempts=attempts+1 WHERE id=$1`, id)
		if e = tx.Commit(ctx); e != nil {
			return TokenPair{}, e
		}
		return TokenPair{}, invalid
	}
	if _, e = tx.Exec(ctx, `UPDATE email_verification_codes SET consumed_at=$2 WHERE id=$1`, id, s.now()); e != nil {
		return TokenPair{}, e
	}
	user, e := s.resolveIdentity(ctx, tx, "email", norm, norm, true)
	if e != nil {
		return TokenPair{}, e
	}
	pair, e := s.createSession(ctx, tx, user, client)
	if e != nil {
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
		return TokenPair{}, httpapi.E(401, "INVALID_GOOGLE_TOKEN", "Invalid Google credential")
	}
	norm, e := NormalizeEmail(g.Email)
	if e != nil {
		return TokenPair{}, e
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if e != nil {
		return TokenPair{}, e
	}
	defer tx.Rollback(ctx)
	user, e := s.resolveIdentity(ctx, tx, "google", g.Subject, norm, g.EmailVerified)
	if e != nil {
		return TokenPair{}, e
	}
	pair, e := s.createSession(ctx, tx, user, client)
	if e != nil {
		return TokenPair{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return TokenPair{}, e
	}
	return pair, nil
}

func (s *Service) resolveIdentity(ctx context.Context, tx pgx.Tx, provider, subject, email string, verified bool) (uuid.UUID, error) {
	var user uuid.UUID
	e := tx.QueryRow(ctx, `SELECT user_id FROM auth_identities WHERE provider=$1 AND provider_subject=$2`, provider, subject).Scan(&user)
	if e == nil {
		return user, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return uuid.Nil, e
	}
	if !verified {
		return uuid.Nil, httpapi.E(401, "EMAIL_NOT_VERIFIED", "A verified email is required")
	}
	// One transaction-scoped lock per normalized email serializes cross-provider signup.
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, email); e != nil {
		return uuid.Nil, e
	}
	e = tx.QueryRow(ctx, `SELECT id FROM users WHERE primary_email=$1 AND deleted_at IS NULL FOR UPDATE`, email).Scan(&user)
	if errors.Is(e, pgx.ErrNoRows) {
		user = uuid.New()
		if _, e = tx.Exec(ctx, `INSERT INTO users(id,primary_email) VALUES($1,$2)`, user, email); e != nil {
			return uuid.Nil, e
		}
		_, e = tx.Exec(ctx, `INSERT INTO user_profiles(user_id,username,display_name) VALUES($1,$2,$3)`, user, "user_"+strings.ReplaceAll(user.String()[:8], "-", ""), strings.Split(email, "@")[0])
		if e != nil {
			return uuid.Nil, e
		}
	} else if e != nil {
		return uuid.Nil, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO auth_identities(user_id,provider,provider_subject,normalized_email,email_verified) VALUES($1,$2,$3,$4,true)`, user, provider, subject, email)
	return user, e
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
	return TokenPair{access, raw, "Bearer", aexp, exp, user}, e
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
	return TokenPair{access, newRaw, "Bearer", aexp, newExp, user}, nil
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
