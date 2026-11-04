package storage

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// EmbeddedMigrations returns a read-only filesystem containing the migrations
// compiled into the OmniRouter binary.
func EmbeddedMigrations() (fs.FS, error) {
	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	return migrations, nil
}

// MigrateEmbedded applies the SQL migrations shipped inside the binary.
func (s *SQLStore) MigrateEmbedded(ctx context.Context) error {
	migrations, err := EmbeddedMigrations()
	if err != nil {
		return err
	}
	return s.Migrate(ctx, migrations, ".")
}
