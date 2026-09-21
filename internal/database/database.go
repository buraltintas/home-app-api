package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	cfg.MaxConns = 20
	// Nothing is held open while nobody is asking. The database suspends itself
	// after five minutes without a connection and is billed for the hours it is
	// awake, and a floor of two connections meant it never once qualified: it
	// stayed up every hour of every night for a service whose nights are a
	// crawler and nobody else. A warm connection is worth about a second on the
	// first request after a quiet spell, which is not worth a night's compute.
	cfg.MinConns = 0
	cfg.MaxConnLifetime = time.Hour
	// Long enough that a burst of requests shares one connection, short enough
	// that a single query at three in the morning does not hold the database up
	// for the next quarter of an hour.
	cfg.MaxConnIdleTime = 90 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
