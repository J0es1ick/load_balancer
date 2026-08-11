package ratelimit

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type PostgresOptions struct {
	Host           string
	Port           string
	User           string
	Password       string
	Database       string
	SSLMode        string
	ConnectTimeout time.Duration
	MaxOpenConns   int
}

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(ctx context.Context, options PostgresOptions) (*PostgresStore, error) {
	dsn := &url.URL{Scheme: "postgres", Host: options.Host + ":" + options.Port, Path: options.Database, User: url.UserPassword(options.User, options.Password)}
	query := dsn.Query()
	query.Set("sslmode", options.SSLMode)
	query.Set("connect_timeout", strconv.Itoa(max(1, int(options.ConnectTimeout.Seconds()))))
	dsn.RawQuery = query.Encode()
	db, err := sql.Open("pgx", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(options.MaxOpenConns)
	db.SetMaxIdleConns(options.MaxOpenConns)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &PostgresStore{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (store *PostgresStore) Name() string { return "postgres" }

func (store *PostgresStore) Take(ctx context.Context, key string, policy Policy) (LimitDecision, error) {
	var allowed bool
	var tokens float64
	err := store.db.QueryRowContext(ctx, "SELECT allowed, remaining FROM ratelimit_take($1, $2, $3)", key, policy.Capacity, policy.RefillPerSecond).Scan(&allowed, &tokens)
	if err != nil {
		return LimitDecision{}, err
	}
	return LimitDecision{Allowed: allowed, Bucket: BucketState{Capacity: policy.Capacity, Tokens: tokens, RefillPerSecond: policy.RefillPerSecond, Storage: store.Name()}}, nil
}

func (store *PostgresStore) Peek(ctx context.Context, key string, policy Policy) (BucketState, error) {
	state := BucketState{Capacity: policy.Capacity, Tokens: float64(policy.Capacity), RefillPerSecond: policy.RefillPerSecond, Storage: store.Name()}
	err := store.db.QueryRowContext(ctx, `
		SELECT $2::integer,
			LEAST($2::double precision, tokens + GREATEST(0, EXTRACT(EPOCH FROM (clock_timestamp() - last_refill))) * $3),
			$3::double precision
		FROM ratelimit_buckets WHERE key = $1
	`, key, policy.Capacity, policy.RefillPerSecond).Scan(&state.Capacity, &state.Tokens, &state.RefillPerSecond)
	if err == sql.ErrNoRows {
		return state, nil
	}
	return state, err
}

func (store *PostgresStore) Reset(ctx context.Context, key string, policy Policy) (BucketState, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return BucketState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", key); err != nil {
		return BucketState{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ratelimit_buckets (key, capacity, tokens, refill_per_second, last_refill, last_seen_at)
		VALUES ($1, $2, $2, $3, clock_timestamp(), clock_timestamp())
		ON CONFLICT (key) DO UPDATE SET capacity = EXCLUDED.capacity, tokens = EXCLUDED.tokens,
			refill_per_second = EXCLUDED.refill_per_second, last_refill = EXCLUDED.last_refill,
			last_seen_at = EXCLUDED.last_seen_at
	`, key, policy.Capacity, policy.RefillPerSecond); err != nil {
		return BucketState{}, err
	}
	if err := tx.Commit(); err != nil {
		return BucketState{}, err
	}
	return BucketState{Capacity: policy.Capacity, Tokens: float64(policy.Capacity), RefillPerSecond: policy.RefillPerSecond, Storage: store.Name()}, nil
}

func (store *PostgresStore) Healthy(ctx context.Context) error { return store.db.PingContext(ctx) }
func (store *PostgresStore) Close() error                      { return store.db.Close() }

func (store *PostgresStore) Cleanup(ctx context.Context, olderThan time.Duration) error {
	_, err := store.db.ExecContext(ctx, "DELETE FROM ratelimit_buckets WHERE last_seen_at < clock_timestamp() - $1::interval", intervalLiteral(olderThan))
	return err
}

func (store *PostgresStore) migrate(ctx context.Context) error {
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended('go-load-balancer-schema-migrations', 0))`); err != nil {
		return err
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = connection.ExecContext(unlockContext, `SELECT pg_advisory_unlock(hashtextextended('go-load-balancer-schema-migrations', 0))`)
	}()
	if _, err := connection.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return err
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var applied bool
		if err := connection.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)", name).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := connection.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(contents)); err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES ($1)", name)
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func intervalLiteral(duration time.Duration) string {
	return fmt.Sprintf("%f seconds", duration.Seconds())
}
