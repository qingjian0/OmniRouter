package storage

// Register the pure-Go SQLite database/sql driver.
// modernc.org/sqlite does not require CGO.
import _ "modernc.org/sqlite"
