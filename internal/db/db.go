package db

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Connect creates a connection pool and verifies connectivity.
func Connect(databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return pool, nil
}

// RunMigrations applies pending SQL migrations from the embedded migrations directory.
// It tracks applied migrations in a schema_migrations table. Returns the list of
// newly applied migration filenames.
func RunMigrations(pool *pgxpool.Pool) ([]string, error) {
	ctx := context.Background()

	// Ensure the tracking table exists.
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename   TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return nil, fmt.Errorf("creating schema_migrations table: %w", err)
	}

	// List migration files.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("reading migrations dir: %w", err)
	}

	var filenames []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			filenames = append(filenames, e.Name())
		}
	}
	sort.Strings(filenames)

	// Check which are already applied.
	rows, err := pool.Query(ctx, "SELECT filename FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("querying applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning migration row: %w", err)
		}
		applied[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating migration rows: %w", err)
	}

	// Apply pending migrations in order.
	var newly []string
	for _, name := range filenames {
		if applied[name] {
			continue
		}

		sql, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return newly, fmt.Errorf("reading migration %s: %w", name, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return newly, fmt.Errorf("beginning tx for %s: %w", name, err)
		}

		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx)
			return newly, fmt.Errorf("applying migration %s: %w", name, err)
		}

		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (filename) VALUES ($1)", name); err != nil {
			tx.Rollback(ctx)
			return newly, fmt.Errorf("recording migration %s: %w", name, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return newly, fmt.Errorf("committing migration %s: %w", name, err)
		}

		newly = append(newly, name)
	}

	return newly, nil
}
