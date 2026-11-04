// Package storage will contain persistence interfaces and implementations.
// SQLite is intentionally deferred until driver support and target builds are evaluated.
package storage

import "context"

type Store interface {
	Ping(context.Context) error
	Close() error
}
