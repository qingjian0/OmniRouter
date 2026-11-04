# Database migrations

The canonical SQL migration files are stored in `internal/storage/migrations/`.
They are embedded into the OmniRouter binary with Go's `embed` package, so a
normal deployment does not need to ship a separate migrations directory.

`storage.SQLStore.MigrateEmbedded` applies the embedded migrations. The lower-
level `storage.SQLStore.Migrate(fs.FS, directory)` entry point remains available
for controlled custom filesystems and migration tests.

Migration filenames use a positive numeric version prefix, for example
`001_initial_schema.sql`. Versions are applied in numeric order, and each
migration plus its schema-version marker is committed atomically.
