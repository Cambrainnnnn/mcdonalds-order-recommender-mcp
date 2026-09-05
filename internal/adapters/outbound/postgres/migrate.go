package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Migrate(ctx context.Context, db *sql.DB) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		statement, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		for index, part := range strings.Split(string(statement), "-- migrate:split") {
			if strings.TrimSpace(part) == "" {
				continue
			}
			if _, err := db.ExecContext(ctx, part); err != nil {
				return fmt.Errorf("apply migration %s part %d: %w", entry.Name(), index+1, err)
			}
		}
	}
	return nil
}
