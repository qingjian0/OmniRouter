package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// This test intentionally uses the registered modernc.org/sqlite driver. It
// exercises the production startup path rather than a SQL mock or stub.
func TestSQLiteRuntimeMigratesAndPersistsRequestUsage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "omnirouter.db")

	runtime, err := OpenSQLiteRuntime(ctx, path, SQLiteOptions{BusyTimeout: time.Second, WAL: true})
	if err != nil {
		t.Fatalf("open SQLite runtime: %v", err)
	}
	store := runtime.Store()
	if store == nil {
		t.Fatal("runtime returned nil store")
	}

	var migrationCount int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("read migration count: %v", err)
	}
	if migrationCount != 6 {
		t.Fatalf("got %d migrations, want 6", migrationCount)
	}

	repo, err := NewRequestLogRepository(store.DB())
	if err != nil {
		t.Fatalf("create request log repository: %v", err)
	}
	input, output := int64(23), int64(11)
	if err := repo.Append(ctx, RequestLog{
		ID: "request-complete", RequestID: "request-complete",
		ProviderID: "provider-a", ModelID: "model-a", StatusCode: 200,
		Latency: 125 * time.Millisecond, InputTokens: &input, OutputTokens: &output,
		LifecycleStatus: "completed", UsageSource: "provider", UsageFinal: true, UsageEligible: true,
		CreatedAt: time.Date(2026, 11, 4, 7, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("append completed request: %v", err)
	}
	if err := repo.Append(ctx, RequestLog{
		ID: "request-interrupted", RequestID: "request-interrupted",
		ProviderID: "provider-a", ModelID: "model-a", StatusCode: 200,
		Latency: 80 * time.Millisecond, InputTokens: &input,
		LifecycleStatus: "interrupted", UsageSource: "provider", UsageFinal: false, UsageEligible: true,
		CreatedAt: time.Date(2026, 11, 4, 7, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("append interrupted request: %v", err)
	}

	summary, err := repo.Summary(ctx)
	if err != nil {
		t.Fatalf("summarize usage: %v", err)
	}
	if summary.Requests != 2 || summary.Errors != 1 || summary.InputTokens != 46 || summary.OutputTokens != 11 || summary.KnownTokenRequests != 2 || summary.FinalTokenRequests != 1 {
		t.Fatalf("unexpected usage summary: %+v", summary)
	}

	breakdown, err := repo.Breakdown(ctx)
	if err != nil {
		t.Fatalf("read provider/model breakdown: %v", err)
	}
	if len(breakdown) != 1 || breakdown[0].ProviderID != "provider-a" || breakdown[0].ModelID != "model-a" || breakdown[0].Requests != 2 || breakdown[0].InputTokens != 46 || breakdown[0].OutputTokens != 11 || breakdown[0].FinalTokenRequests != 1 {
		t.Fatalf("unexpected provider/model breakdown: %+v", breakdown)
	}

	if err := runtime.Close(); err != nil {
		t.Fatalf("close SQLite runtime: %v", err)
	}

	// Reopening the same database proves migrations are idempotently skipped
	// and persisted request usage remains queryable after process restart.
	reopened, err := OpenSQLiteRuntime(ctx, path, SQLiteOptions{BusyTimeout: time.Second})
	if err != nil {
		t.Fatalf("reopen SQLite runtime: %v", err)
	}
	defer reopened.Close()
	reopenedRepo, err := NewRequestLogRepository(reopened.Store().DB())
	if err != nil {
		t.Fatalf("create reopened request log repository: %v", err)
	}
	recent, err := reopenedRepo.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("read persisted request logs: %v", err)
	}
	if len(recent) != 2 || recent[0].LifecycleStatus != "interrupted" || recent[1].LifecycleStatus != "completed" {
		t.Fatalf("unexpected persisted request logs after reopen: %+v", recent)
	}
}

func TestRecentRequestLogsCapsLimitAndUsesStableOrdering(t *testing.T) {
	ctx := context.Background()
	runtime, err := OpenSQLiteRuntime(ctx, filepath.Join(t.TempDir(), "request-limits.db"), SQLiteOptions{BusyTimeout: time.Second})
	if err != nil {
		t.Fatalf("open SQLite runtime: %v", err)
	}
	defer runtime.Close()
	repo, err := NewRequestLogRepository(runtime.Store().DB())
	if err != nil {
		t.Fatalf("create request log repository: %v", err)
	}
	createdAt := time.Date(2026, 11, 4, 7, 30, 0, 0, time.UTC)
	for i := 0; i <= MaxRecentRequestLogs; i++ {
		id := fmt.Sprintf("request-%04d", i)
		if err := repo.Append(ctx, RequestLog{ID: id, RequestID: id, StatusCode: 200, LifecycleStatus: "completed", CreatedAt: createdAt}); err != nil {
			t.Fatalf("append request %d: %v", i, err)
		}
	}

	items, err := repo.Recent(ctx, MaxRecentRequestLogs+1)
	if err != nil {
		t.Fatalf("read capped recent request logs: %v", err)
	}
	if len(items) != MaxRecentRequestLogs {
		t.Fatalf("got %d records, want cap %d", len(items), MaxRecentRequestLogs)
	}
	if items[0].ID != "request-1000" || items[1].ID != "request-0999" {
		t.Fatalf("records with equal timestamps are not deterministically ordered: first=%q second=%q", items[0].ID, items[1].ID)
	}
}

func TestRequestLogWritesRecoverAfterSQLiteLockIsReleased(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "request-recovery.db")
	runtime, err := OpenSQLiteRuntime(ctx, path, SQLiteOptions{BusyTimeout: 75 * time.Millisecond})
	if err != nil {
		t.Fatalf("open SQLite runtime: %v", err)
	}
	defer runtime.Close()
	repo, err := NewRequestLogRepository(runtime.Store().DB())
	if err != nil {
		t.Fatalf("create request log repository: %v", err)
	}

	// Hold a write reservation from a second connection to simulate a
	// transient external writer/lock. The production connection must fail
	// within its configured busy timeout rather than hang indefinitely.
	blocker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open lock-holder connection: %v", err)
	}
	defer blocker.Close()
	lockConn, err := blocker.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire lock-holder connection: %v", err)
	}
	defer lockConn.Close()
	if _, err := lockConn.ExecContext(ctx, "PRAGMA busy_timeout = 50"); err != nil {
		t.Fatalf("configure lock-holder busy timeout: %v", err)
	}
	if _, err := lockConn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("acquire external write lock: %v", err)
	}

	firstErr := repo.Append(ctx, RequestLog{ID: "blocked", RequestID: "blocked", StatusCode: 200, LifecycleStatus: "completed"})
	if firstErr == nil {
		_, _ = lockConn.ExecContext(ctx, "ROLLBACK")
		t.Fatal("append unexpectedly succeeded while another connection held the write lock")
	}
	if _, err := lockConn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("release external write lock: %v", err)
	}

	if err := repo.Append(ctx, RequestLog{ID: "recovered", RequestID: "recovered", StatusCode: 200, LifecycleStatus: "completed"}); err != nil {
		t.Fatalf("append after lock release should recover: %v", err)
	}
	items, err := repo.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("read recovered request log: %v", err)
	}
	if len(items) != 1 || items[0].ID != "recovered" {
		t.Fatalf("unexpected records after recovery: %+v", items)
	}
}

func TestRequestLogPreservesInt64TokenCounts(t *testing.T) {
	ctx := context.Background()
	runtime, err := OpenSQLiteRuntime(ctx, filepath.Join(t.TempDir(), "int64-usage.db"), SQLiteOptions{BusyTimeout: time.Second})
	if err != nil {
		t.Fatalf("open SQLite runtime: %v", err)
	}
	defer runtime.Close()
	repo, err := NewRequestLogRepository(runtime.Store().DB())
	if err != nil {
		t.Fatalf("create request log repository: %v", err)
	}
	// Values above signed 32-bit range must survive ingestion and retrieval on
	// every supported Go architecture; token accounting is int64 end-to-end.
	inputTokens := int64(5_000_000_123)
	outputTokens := int64(3_000_000_456)
	if err := repo.Append(ctx, RequestLog{
		ID: "large-usage", RequestID: "large-usage", StatusCode: 200,
		LifecycleStatus: "completed", UsageFinal: true, UsageEligible: true,
		InputTokens: &inputTokens, OutputTokens: &outputTokens,
	}); err != nil {
		t.Fatalf("append large token counts: %v", err)
	}
	items, err := repo.Recent(ctx, 1)
	if err != nil {
		t.Fatalf("read large token counts: %v", err)
	}
	if len(items) != 1 || items[0].InputTokens == nil || *items[0].InputTokens != inputTokens || items[0].OutputTokens == nil || *items[0].OutputTokens != outputTokens {
		t.Fatalf("int64 token counts did not round-trip: %+v", items)
	}
}

func TestRequestLogAppendRejectsNegativeTokenCountsBeforeDatabaseAccess(t *testing.T) {
	negative := int64(-1)
	repo := &RequestLogRepository{}
	cases := []struct {
		name string
		log  RequestLog
	}{
		{name: "negative input", log: RequestLog{ID: "negative-input", RequestID: "negative-input", InputTokens: &negative}},
		{name: "negative output", log: RequestLog{ID: "negative-output", RequestID: "negative-output", OutputTokens: &negative}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := repo.Append(context.Background(), tc.log); err == nil {
				t.Fatal("Append accepted a negative token count")
			}
		})
	}
}

func TestUsageAggregatesFailClosedOnInt64Overflow(t *testing.T) {
	ctx := context.Background()
	runtime, err := OpenSQLiteRuntime(ctx, filepath.Join(t.TempDir(), "usage-overflow.db"), SQLiteOptions{BusyTimeout: time.Second})
	if err != nil {
		t.Fatalf("open SQLite runtime: %v", err)
	}
	defer runtime.Close()
	repo, err := NewRequestLogRepository(runtime.Store().DB())
	if err != nil {
		t.Fatalf("create request log repository: %v", err)
	}
	maxInt64 := int64(^uint64(0) >> 1)
	one := int64(1)
	for _, item := range []struct {
		id     string
		input  *int64
		output *int64
	}{
		{id: "max", input: &maxInt64},
		{id: "overflow", input: &one},
	} {
		if err := repo.Append(ctx, RequestLog{
			ID: item.id, RequestID: item.id, ProviderID: "provider-overflow", ModelID: "model-overflow",
			StatusCode: 200, LifecycleStatus: "completed", UsageEligible: true,
			InputTokens: item.input, OutputTokens: item.output,
		}); err != nil {
			t.Fatalf("append %s usage: %v", item.id, err)
		}
	}
	if _, _, err := repo.UsageReport(ctx); err == nil {
		t.Fatal("UsageReport silently returned a wrapped token total instead of failing on int64 overflow")
	}
	if _, err := repo.Summary(ctx); err == nil {
		t.Fatal("Summary silently returned a wrapped token total instead of failing on int64 overflow")
	}
	if _, err := repo.Breakdown(ctx); err == nil {
		t.Fatal("Breakdown silently returned a wrapped token total instead of failing on int64 overflow")
	}
}

func TestTransactionCallbacksPanicAndReleaseConnection(t *testing.T) {
	ctx := context.Background()
	runtime, err := OpenSQLiteRuntime(ctx, filepath.Join(t.TempDir(), "transaction-panic.db"), SQLiteOptions{BusyTimeout: time.Second})
	if err != nil {
		t.Fatalf("open SQLite runtime: %v", err)
	}
	defer runtime.Close()

	store := runtime.Store()
	if _, err := store.DB().ExecContext(ctx, `CREATE TABLE panic_rollback_probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	func() {
		defer func() {
			if recovered := recover(); recovered != "with-tx-panic" {
				t.Fatalf("WithTx panic = %v, want with-tx-panic", recovered)
			}
		}()
		_ = store.WithTx(ctx, nil, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO panic_rollback_probe(id) VALUES(1)`); err != nil {
				return err
			}
			panic("with-tx-panic")
		})
	}()

	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM panic_rollback_probe`).Scan(&count); err != nil {
		t.Fatalf("query after WithTx panic (connection may be stranded): %v", err)
	}
	if count != 0 {
		t.Fatalf("WithTx panic left %d uncommitted rows, want rollback", count)
	}

	func() {
		defer func() {
			if recovered := recover(); recovered != "in-transaction-panic" {
				t.Fatalf("InTransaction panic = %v, want in-transaction-panic", recovered)
			}
		}()
		_ = InTransaction(ctx, store.DB(), func(tx DBTX) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO panic_rollback_probe(id) VALUES(2)`); err != nil {
				return err
			}
			panic("in-transaction-panic")
		})
	}()

	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM panic_rollback_probe`).Scan(&count); err != nil {
		t.Fatalf("query after InTransaction panic (connection may be stranded): %v", err)
	}
	if count != 0 {
		t.Fatalf("InTransaction panic left %d uncommitted rows, want rollback", count)
	}
}

func TestSQLiteMigrationsSerializeConcurrentStartup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concurrent-migrations.db")
	const instances = 2
	stores := make([]*SQLStore, 0, instances)
	dbs := make([]*sql.DB, 0, instances)
	for i := 0; i < instances; i++ {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open SQLite connection %d: %v", i, err)
		}
		dbs = append(dbs, db)
		if err := ConfigureSQLite(ctx, db, SQLiteOptions{BusyTimeout: 5 * time.Second, WAL: true}); err != nil {
			t.Fatalf("configure SQLite connection %d: %v", i, err)
		}
		store, err := NewSQL(db)
		if err != nil {
			t.Fatalf("create SQL store %d: %v", i, err)
		}
		stores = append(stores, store)
	}
	defer func() {
		for _, db := range dbs {
			_ = db.Close()
		}
	}()

	start := make(chan struct{})
	errs := make(chan error, instances)
	var wg sync.WaitGroup
	for _, store := range stores {
		wg.Add(1)
		go func(store *SQLStore) {
			defer wg.Done()
			<-start
			errs <- store.MigrateEmbedded(ctx)
		}(store)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migration failed: %v", err)
		}
	}

	var count int
	if err := stores[0].DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("read migration count after concurrent startup: %v", err)
	}
	if count != 5 {
		t.Fatalf("got %d migration records after concurrent startup, want 5", count)
	}
	var lockRows int
	if err := stores[0].DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migration_lock WHERE id = 1`).Scan(&lockRows); err != nil {
		t.Fatalf("read migration lock row: %v", err)
	}
	if lockRows != 1 {
		t.Fatalf("got %d migration lock rows, want exactly one", lockRows)
	}
	var generation int64
	if err := stores[0].DB().QueryRowContext(ctx, `SELECT generation FROM schema_migration_lock WHERE id = 1`).Scan(&generation); err != nil {
		t.Fatalf("read migration lock generation: %v", err)
	}
	if generation < 5 {
		t.Fatalf("migration lock generation = %d, want at least one acquisition per migration", generation)
	}
}
