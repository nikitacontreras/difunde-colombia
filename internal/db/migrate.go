// Package db ejecuta migraciones SQL embebidas al arrancar.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrator aplica las migraciones embebidas no aplicadas todavía.
type Migrator struct {
	// Exec ejecuta SQL sobre la conexión.
	Exec func(ctx context.Context, sql string, args ...any) error
	// QueryScalar devuelve las filas de una consulta como []any de valores.
	QueryScalar func(ctx context.Context, sql string, args ...any) (any, error)
}

func (m *Migrator) Apply(ctx context.Context) error {
	if err := m.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("crear schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := m.QueryScalar(ctx, `SELECT version FROM schema_migrations`)
	if err == nil {
		if list, ok := rows.([]any); ok {
			for _, v := range list {
				if s, ok := v.(string); ok {
					applied[s] = true
				}
			}
		}
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if applied[name] {
			continue
		}
		data, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		// Ejecutar cada statement por separado (pgx no permite multi-statement).
		for _, stmt := range splitStatements(string(data)) {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if err := m.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("migración %s: %w", name, err)
			}
		}
		if err := m.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			return fmt.Errorf("registrar migración %s: %w", name, err)
		}
	}
	return nil
}

func splitStatements(sql string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "--" || strings.HasPrefix(trimmed, "-- ") {
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}
