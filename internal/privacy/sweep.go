// Package privacy enforces the retention periods the product publishes. The statements
// live here rather than in a command so that the API can run them on a schedule of its
// own: a retention policy nobody executes is not a policy, and the published pages state
// these periods as fact.
package privacy

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	SearchRetentionDays         int
	SearchLocationRetentionDays int
	VisitorRetentionDays        int
}

// advisoryLockKey keeps concurrent instances from sweeping at the same time. The work is
// idempotent, but two instances deleting the same rows is wasted load for no benefit.
const advisoryLockKey = 774_1990

// Sweep applies every retention period in one transaction. Each statement matches a period
// stated on the published privacy and KVKK pages; changing one here without changing the
// page there makes the page untrue.
func Sweep(ctx context.Context, db *pgxpool.Pool, c Config) error {
	tx, e := db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)

	var acquired bool
	if e = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, advisoryLockKey).Scan(&acquired); e != nil {
		return e
	}
	if !acquired {
		// Another instance is already sweeping. Nothing to do and nothing wrong.
		return nil
	}

	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM searches WHERE created_at < now()-($1::int*interval '1 day')`, []any{c.SearchRetentionDays}},
		{`DELETE FROM searches WHERE user_id IS NULL AND visitor_session_id IN (SELECT id FROM visitor_sessions WHERE expires_at < now() OR last_seen_at < now()-($1::int*interval '1 day'))`, []any{c.VisitorRetentionDays}},
		{`DELETE FROM visitor_sessions WHERE expires_at < now() OR last_seen_at < now()-($1::int*interval '1 day')`, []any{c.VisitorRetentionDays}},
		{`DELETE FROM email_verification_codes WHERE created_at < now()-interval '30 days'`, nil},
		{`DELETE FROM auth_sessions WHERE expires_at < now()-interval '30 days' OR revoked_at < now()-interval '30 days'`, nil},
		{`DELETE FROM store_visit_verifications WHERE expires_at < now()-interval '30 days' OR consumed_at < now()-interval '30 days'`, nil},
		{`DELETE FROM email_outbox WHERE created_at < now()-interval '90 days' AND status IN ('sent','failed')`, nil},
		{`UPDATE searches SET request_latitude=NULL,request_longitude=NULL WHERE created_at < now()-($1::int*interval '1 day')`, []any{c.SearchLocationRetentionDays}},
	}
	for _, statement := range statements {
		if _, e = tx.Exec(ctx, statement.query, statement.args...); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

// Run sweeps once at startup and then daily. It runs inside the API rather than waiting on
// an external scheduler, because the scheduler was never created and the retention periods
// were therefore never enforced -- while the privacy pages stated them as fact.
func Run(ctx context.Context, db *pgxpool.Pool, c Config, log *slog.Logger) {
	sweep := func() {
		started := time.Now()
		if e := Sweep(ctx, db, c); e != nil {
			log.Error("privacy retention sweep failed", "error", e)
			return
		}
		log.Info("privacy retention sweep completed", "duration_ms", time.Since(started).Milliseconds())
	}
	sweep()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}
