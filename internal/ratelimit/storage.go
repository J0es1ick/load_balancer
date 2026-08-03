package ratelimit

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/J0es1ick/test-assignment/internal/config"
	_ "github.com/lib/pq"
)

type Storage interface {
	Get(ctx context.Context, key string) (*TokenBucket, bool, error)
	Set(ctx context.Context, key string, bucket *TokenBucket) error
	Update(ctx context.Context, key string, updateFunc func(bucket *TokenBucket) (*TokenBucket, error)) error
}

type ReconfigurableStorage interface {
	Reconfigure(ctx context.Context, capacity int, rate time.Duration) error
}

type Database struct {
	DB *sql.DB
}

func NewDatabase(cfg *config.Config) (*Database, error) {
	connStr := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s&connect_timeout=%d",
		cfg.Database.User,
		cfg.Database.Password,
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.Name,
		cfg.Database.SSLMode,
		int(cfg.Database.ConnectTimeout.Seconds()),
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Database{DB: db}, nil
}

func (s *Database) Close() error {
	return s.DB.Close()
}

func (s *Database) Init(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ratelimit (
			key TEXT PRIMARY KEY,
			capacity INTEGER NOT NULL,
			tokens INTEGER NOT NULL,
			rate TEXT NOT NULL,
			last_refill TIMESTAMP NOT NULL,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		);
	`)
	return err
}

func (s *Database) Get(ctx context.Context, key string) (*TokenBucket, bool, error) {
	return scanBucket(s.DB.QueryRowContext(
		ctx,
		"SELECT capacity, tokens, rate, last_refill FROM ratelimit WHERE key = $1",
		key,
	))
}

func (s *Database) Set(ctx context.Context, key string, bucket *TokenBucket) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockBucketKey(ctx, tx, key); err != nil {
		return err
	}
	if err := setBucket(ctx, tx, key, bucket); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Database) Update(ctx context.Context, key string, updateFunc func(bucket *TokenBucket) (*TokenBucket, error)) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockBucketKey(ctx, tx, key); err != nil {
		return err
	}

	bucket, exists, err := scanBucket(tx.QueryRowContext(
		ctx,
		"SELECT capacity, tokens, rate, last_refill FROM ratelimit WHERE key = $1 FOR UPDATE",
		key,
	))
	if err != nil {
		return err
	}
	if !exists {
		bucket = nil
	}

	newBucket, err := updateFunc(bucket)
	if err != nil {
		return err
	}
	if newBucket == nil {
		return fmt.Errorf("bucket update returned nil")
	}
	if err := setBucket(ctx, tx, key, newBucket); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Database) Reconfigure(ctx context.Context, capacity int, rate time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE ratelimit
		SET capacity = $1,
			tokens = LEAST(tokens, $1),
			rate = $2,
			last_refill = NOW(),
			updated_at = NOW()
	`, capacity, rate.String())
	return err
}

func lockBucketKey(ctx context.Context, tx *sql.Tx, key string) error {
	_, err := tx.ExecContext(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
		key,
	)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBucket(row rowScanner) (*TokenBucket, bool, error) {
	var (
		capacity   int
		tokens     int
		rateString string
		lastRefill time.Time
	)

	if err := row.Scan(&capacity, &tokens, &rateString, &lastRefill); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}

	rate, err := time.ParseDuration(rateString)
	if err != nil {
		return nil, false, fmt.Errorf("invalid rate format: %w", err)
	}
	return &TokenBucket{
		capacity:   capacity,
		tokens:     tokens,
		rate:       rate,
		lastRefill: lastRefill,
	}, true, nil
}

type statementExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func setBucket(ctx context.Context, executor statementExecutor, key string, bucket *TokenBucket) error {
	_, err := executor.ExecContext(ctx, `
		INSERT INTO ratelimit (key, capacity, tokens, rate, last_refill)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (key) DO UPDATE SET
			capacity = EXCLUDED.capacity,
			tokens = EXCLUDED.tokens,
			rate = EXCLUDED.rate,
			last_refill = EXCLUDED.last_refill,
			updated_at = NOW();
	`, key, bucket.capacity, bucket.tokens, bucket.rate.String(), bucket.lastRefill)
	return err
}

func (s *Database) StartCleanupWorker(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := s.cleanupOldBuckets(ctx, 24*time.Hour); err != nil {
					log.Printf("cleanup error: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Database) cleanupOldBuckets(ctx context.Context, olderThan time.Duration) error {
	_, err := s.DB.ExecContext(
		ctx,
		`DELETE FROM ratelimit WHERE last_refill < $1`,
		time.Now().Add(-olderThan),
	)
	return err
}
