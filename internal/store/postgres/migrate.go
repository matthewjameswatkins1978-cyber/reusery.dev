package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// migrationsFS embeds the authored Goose SQL migrations.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending migrations (goose "up").
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	provider, db, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// MigrateDown rolls back the most recent migration (goose "down").
func MigrateDown(ctx context.Context, pool *pgxpool.Pool) error {
	provider, db, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if _, err := provider.Down(ctx); err != nil {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

func newProvider(pool *pgxpool.Pool) (*goose.Provider, *sql.DB, error) {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, nil, fmt.Errorf("migrations fs: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("goose provider: %w", err)
	}
	return provider, db, nil
}
