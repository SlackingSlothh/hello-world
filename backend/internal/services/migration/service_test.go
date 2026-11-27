package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockAuthorizer struct {
	permissions map[string]map[string]string
}

func newMockAuthorizer() *mockAuthorizer {
	return &mockAuthorizer{
		permissions: make(map[string]map[string]string),
	}
}

func (a *mockAuthorizer) Grant(userID, databaseID, level string) {
	if a.permissions[userID] == nil {
		a.permissions[userID] = make(map[string]string)
	}
	a.permissions[userID][databaseID] = level
}

func (a *mockAuthorizer) RequireDatabase(_ context.Context, p Principal, databaseID string, action Action) error {
	dbPerms, ok := a.permissions[p.UserID]
	if !ok {
		return ErrForbidden
	}
	level := dbPerms[databaseID]
	switch action {
	case ActionReadMigration:
		if level == "VIEW_ONLY" || level == "VIEW_EDIT" {
			return nil
		}
	case ActionCreateMigration, ActionUpdateMigration, ActionExecuteMigration:
		if level == "VIEW_EDIT" {
			return nil
		}
	}
	return ErrForbidden
}

type inMemoryStore struct {
	RequestStore
	mu            sync.Mutex
	nextMigID     int64
	nextRunID     int64
	migrations    map[int64]*Migration
	runsByMigID   map[int64][]MigrationRun
	maxVerByDB    map[string]int64
	dispatchError error
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{
		nextMigID:   1,
		nextRunID:   1,
		migrations:  make(map[int64]*Migration),
		runsByMigID: make(map[int64][]MigrationRun),
		maxVerByDB:  make(map[string]int64),
	}
}

func (s *inMemoryStore) CreateMigration(_ context.Context, m *Migration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	m.ID = s.nextMigID
	s.nextMigID++
	cp := *m
	s.migrations[m.ID] = &cp
	return nil
}

func (s *inMemoryStore) GetMigrationByID(_ context.Context, migrationID int64) (*Migration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.migrations[migrationID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *m
	if m.Version != nil {
		v := *m.Version
		cp.Version = &v
	}
	return &cp, nil
}

func (s *inMemoryStore) ListMigrationsByDatabase(_ context.Context, databaseID string) ([]Migration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Migration
	for _, m := range s.migrations {
		if m.DatabaseID == databaseID {
			cp := *m
			out = append(out, cp)
		}
	}
	return out, nil
}

func (s *inMemoryStore) UpdateDraftSQL(_ context.Context, updated *Migration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.migrations[updated.ID]
	if !ok {
		return ErrNotFound
	}
	if m.FrozenAt != nil || m.Revision != updated.Revision {
		return ErrRevisionChanged
	}
	updated.Revision++
	*m = *updated
	return nil
}

func (s *inMemoryStore) CreateRun(_ context.Context, run *MigrationRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	m := s.migrations[run.MigrationID]
	if m.Revision != run.Revision || m.SQLContent != run.SQLSnapshot {
		return ErrRevisionChanged
	}
	if m.FrozenAt == nil {
		m.FrozenAt = &run.StartedAt
	}
	run.AttemptNumber = len(s.runsByMigID[run.MigrationID]) + 1
	run.ID = s.nextRunID
	s.nextRunID++
	run.SQLDispatchStarted = new(false)
	cp := *run
	s.runsByMigID[run.MigrationID] = append(s.runsByMigID[run.MigrationID], cp)
	return nil
}

func (s *inMemoryStore) MarkSQLDispatchStarted(_ context.Context, runID int64, _ TransactionEvidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dispatchError != nil {
		return s.dispatchError
	}
	for _, runs := range s.runsByMigID {
		for i := range runs {
			if runs[i].ID == runID && runs[i].Status == RunStatusRunning {
				runs[i].SQLDispatchStarted = new(true)
				return nil
			}
		}
	}
	return ErrNotFound
}

func (s *inMemoryStore) GetRunForReconciliation(_ context.Context, databaseID string, runID int64) (MigrationRun, TransactionEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, runs := range s.runsByMigID {
		for _, run := range runs {
			if run.ID == runID && run.DatabaseID == databaseID {
				return run, TransactionEvidence{}, nil
			}
		}
	}
	return MigrationRun{}, TransactionEvidence{}, ErrNotFound
}

func (s *inMemoryStore) CompleteRunFailed(_ context.Context, runID, migrationID int64, errMsg string, execMs int64, finishedAt time.Time) error {
	if !s.failRun(runID, migrationID, errMsg, execMs, finishedAt, false) {
		return ErrNotFound
	}
	return nil
}

func (s *inMemoryStore) FailRunBeforeDispatch(_ context.Context, runID, migrationID int64, errMsg string, finishedAt time.Time) (bool, error) {
	return s.failRun(runID, migrationID, errMsg, 0, finishedAt, true), nil
}

func (s *inMemoryStore) failRun(runID, migrationID int64, errMsg string, execMs int64, finishedAt time.Time, requireUndispatched bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	runs := s.runsByMigID[migrationID]
	for i := range runs {
		run := &runs[i]
		if run.ID != runID || run.Status != RunStatusRunning ||
			(requireUndispatched && (run.SQLDispatchStarted == nil || *run.SQLDispatchStarted)) {
			continue
		}
		run.Status = RunStatusFailed
		run.ErrorMessage = &errMsg
		run.ExecutionMs = execMs
		run.FinishedAt = &finishedAt
		if m := s.migrations[migrationID]; m.Status != StatusApplied {
			m.Status = StatusFailed
			m.UpdatedAt = finishedAt
		}
		return true
	}
	return false
}

func (s *inMemoryStore) CompleteRunSucceeded(_ context.Context, runID, migrationID int64, databaseID string, execMs int64, finishedAt time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nextVer := s.maxVerByDB[databaseID] + 1
	s.maxVerByDB[databaseID] = nextVer

	if m, ok := s.migrations[migrationID]; ok {
		m.Status = StatusApplied
		v := nextVer
		m.Version = &v
		t := finishedAt
		m.AppliedAt = &t
		m.UpdatedAt = finishedAt
	}

	runs := s.runsByMigID[migrationID]
	for i := range runs {
		if runs[i].ID == runID {
			runs[i].Status = RunStatusSucceeded
			runs[i].ErrorMessage = nil
			runs[i].ExecutionMs = execMs
			t := finishedAt
			runs[i].FinishedAt = &t
			break
		}
	}
	return nextVer, nil
}

func (s *inMemoryStore) ListRunsByMigration(_ context.Context, migrationID int64) ([]MigrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runs := s.runsByMigID[migrationID]
	out := make([]MigrationRun, len(runs))
	copy(out, runs)
	return out, nil
}

func (s *inMemoryStore) ListMigrationHistory(context.Context, string) ([]HistoryEntry, error) {
	return []HistoryEntry{}, nil
}

type mockTargetDB struct {
	mu             sync.Mutex
	lockError      error
	execCalls      int
	shouldFailSQL  bool
	failErrMsg     string
	commitError    error
	committedSQLs  []string
	rolledBackRuns int
	currentSchema  []TableSchema
	dialect        TargetDialect
}

func (db *mockTargetDB) BeginTargetTx(_ context.Context, _ string) (TargetTx, error) {
	return &mockTargetTx{parent: db}, nil
}

type mockTargetTx struct {
	parent     *mockTargetDB
	stagedSQLs []string
	done       bool
}

func (tx *mockTargetTx) Dialect() TargetDialect {
	if tx.parent.dialect == "" {
		return DialectPostgres
	}
	return tx.parent.dialect
}

func (tx *mockTargetTx) AcquireTargetLock(_ context.Context, _ string) error {
	return tx.parent.lockError
}

func (tx *mockTargetTx) ExecSQL(_ context.Context, sqlQuery string) error {
	tx.parent.mu.Lock()
	defer tx.parent.mu.Unlock()

	tx.parent.execCalls++
	if tx.parent.shouldFailSQL {
		return errors.New(tx.parent.failErrMsg)
	}
	tx.stagedSQLs = append(tx.stagedSQLs, sqlQuery)
	return nil
}

func (tx *mockTargetTx) ExplainSQL(_ context.Context, sqlQuery string, analyze bool) (string, error) {
	tx.stagedSQLs = append(tx.stagedSQLs, "EXPLAIN:"+sqlQuery)
	if analyze {
		return `[{"Plan":{"Node Type":"Seq Scan","Relation Name":"products","Startup Cost":0.0,"Total Cost":12.5,"Plan Rows":250,"Actual Total Time":0.42,"Actual Rows":250},"Planning Time":0.11,"Execution Time":0.48}]`, nil
	}
	return `[{"Plan":{"Node Type":"Index Scan","Relation Name":"products","Index Name":"idx_products_sku","Startup Cost":0.15,"Total Cost":8.17,"Plan Rows":1}}]`, nil
}

func (tx *mockTargetTx) InspectSchema(_ context.Context, _ string) ([]TableSchema, error) {
	tx.parent.mu.Lock()
	defer tx.parent.mu.Unlock()
	return tx.parent.currentSchema, nil
}

func (tx *mockTargetTx) Commit(_ context.Context) error {
	tx.parent.mu.Lock()
	defer tx.parent.mu.Unlock()

	if tx.done {
		return nil
	}
	tx.done = true
	tx.parent.committedSQLs = append(tx.parent.committedSQLs, tx.stagedSQLs...)
	return tx.parent.commitError
}

func (tx *mockTargetTx) Rollback(_ context.Context) error {
	tx.parent.mu.Lock()
	defer tx.parent.mu.Unlock()

	if tx.done {
		return nil
	}
	tx.done = true
	tx.stagedSQLs = nil
	tx.parent.rolledBackRuns++
	return nil
}

// ============================================================================
// UNIT & INTEGRATION TESTS
// ============================================================================

func TestValidateTransactionalSQL(t *testing.T) {
	validCases := []string{
		"CREATE TABLE users (id SERIAL PRIMARY KEY, username TEXT NOT NULL);",
		"ALTER TABLE users ADD COLUMN created_at TIMESTAMPTZ DEFAULT NOW();",
		"-- Komentar tentang COMMIT dan ROLLBACK tidak boleh memicu error\nINSERT INTO audit_log (note) VALUES ('tidak melakukan COMMIT manual');",
		"/* Blok komentar CREATE DATABASE */ UPDATE products SET price = 10000 WHERE id = 1;",
	}
	for _, sqlText := range validCases {
		if err := ValidateTransactionalSQL(sqlText); err != nil {
			t.Errorf("seharusnya SQL valid, namun ditolak: %v (sql: %s)", err, sqlText)
		}
	}

	invalidCases := []struct {
		name string
		sql  string
	}{
		{"Kosong", "   "},
		{"Hanya Komentar", "-- hanya komentar saja\n/* kosong */"},
		{"Manual COMMIT", "CREATE TABLE a (id INT); COMMIT;"},
		{"Manual ROLLBACK", "UPDATE a SET id = 2; ROLLBACK;"},
		{"Manual BEGIN", "BEGIN; CREATE TABLE a (id INT);"},
		{"CREATE DATABASE", "CREATE DATABASE baru;"},
		{"CREATE INDEX CONCURRENTLY", "CREATE INDEX CONCURRENTLY idx_a ON a(id);"},
		{"VACUUM", "VACUUM FULL;"},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTransactionalSQL(tc.sql)
			if !errors.Is(err, ErrInvalidSQL) {
				t.Fatalf("kasus %s seharusnya mengembalikan ErrInvalidSQL, dapat: %v", tc.name, err)
			}
		})
	}
}

func TestSplitSQLStatementsIgnoresSemicolonsInLiteralsAndComments(t *testing.T) {
	got := splitSQLStatements("INSERT INTO notes(text) VALUES ('one;two'); -- keep ; here\nUPDATE notes SET text = `value;name` WHERE id = 1;")
	if len(got) != 2 {
		t.Fatalf("expected two statements, got %d: %#v", len(got), got)
	}
}

func TestValidateTransactionalSQLForDialect(t *testing.T) {
	for _, query := range []string{
		"SELECT id FROM products;",
		"INSERT INTO products(id) VALUES (1);",
		"UPDATE products SET name = 'CREATE TABLE' WHERE id = 1;",
		"DELETE FROM products WHERE id = 1;",
		"# CREATE TABLE is only a comment\nINSERT INTO products(id) VALUES (1);",
	} {
		if err := ValidateTransactionalSQLForDialect(query, DialectMySQL); err != nil {
			t.Errorf("transactional SQL was rejected: %s: %v", query, err)
		}
	}
	for _, query := range []string{
		"CREATE TABLE products (id INT);",
		"ALTER TABLE products ADD COLUMN name TEXT;",
		"SET autocommit = 1;",
		"CALL update_products();",
		"SELECT id FROM products INTO OUTFILE '/tmp/products.csv';",
	} {
		if err := ValidateTransactionalSQLForDialect(query, DialectMariaDB); !errors.Is(err, ErrInvalidSQL) {
			t.Errorf("non-transactional SQL should be rejected: %s: %v", query, err)
		}
	}
}

func TestMigrationLifecycle_FailedAttemptFreezesDefinitionAndRetryKeepsHistory(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	store := newInMemoryStore()
	targetDB := &mockTargetDB{}

	const dbID = "db-101"
	devUser := Principal{UserID: "user-7", Username: "budi_dev"}
	authz.Grant(devUser.UserID, dbID, "VIEW_EDIT")

	svc := NewService(authz, store, targetDB, nil)

	draft, err := svc.CreateDraft(ctx, devUser, CreateDraftInput{
		DatabaseID:  dbID,
		Title:       "Buat tabel produk",
		Description: "Menambahkan tabel products awal",
		SQLContent:  "CREATE TABLE products (id SERIAL PRIMARY KEY);",
		SQLDown:     "DROP TABLE products;",
	})
	if err != nil {
		t.Fatalf("CreateDraft gagal: %v", err)
	}
	if draft.Status != StatusDraft || draft.Version != nil {
		t.Fatalf("status awal harus DRAFT dengan Version nil")
	}

	targetDB.shouldFailSQL = true
	targetDB.failErrMsg = "temporary target failure"

	execRes1, err := svc.ExecuteMigration(ctx, devUser, dbID, draft.ID)
	if !errors.Is(err, ErrExecutionFailed) {
		t.Fatalf("seharusnya gagal dengan ErrExecutionFailed, dapat: %v", err)
	}
	if execRes1 == nil || execRes1.Migration.Status != StatusFailed {
		t.Fatalf("status migrasi setelah gagal harus FAILED")
	}
	if targetDB.rolledBackRuns != 1 || len(targetDB.committedSQLs) != 0 {
		t.Fatalf("transaksi target DB seharusnya di-rollback")
	}

	_, err = svc.UpdateDraft(ctx, devUser, UpdateDraftInput{
		DatabaseID:  dbID,
		MigrationID: draft.ID,
		Title:       "Buat tabel produk",
		SQLContent:  "CREATE TABLE products (id SERIAL PRIMARY KEY, name TEXT NOT NULL);",
		SQLDown:     "DROP TABLE products;",
	})
	if !errors.Is(err, ErrDefinitionFrozen) {
		t.Fatalf("attempted definition should stay frozen: %v", err)
	}

	targetDB.shouldFailSQL = false
	execRes2, err := svc.ExecuteMigration(ctx, devUser, dbID, draft.ID)
	if err != nil {
		t.Fatalf("ExecuteMigration percobaan ke-2 seharusnya berhasil: %v", err)
	}
	if execRes2.Migration.Status != StatusApplied || execRes2.Migration.Version == nil || *execRes2.Migration.Version != 1 {
		t.Fatalf("migrasi seharusnya APPLIED dengan versi 1")
	}

	detail, err := svc.GetMigrationDetail(ctx, devUser, dbID, draft.ID)
	if err != nil || len(detail.Runs) != 2 {
		t.Fatalf("seharusnya terdapat 2 catatan di migration_runs")
	}
	first, second := detail.Runs[0], detail.Runs[1]
	if first.Status != RunStatusFailed || second.Status != RunStatusSucceeded || first.ID == second.ID ||
		first.Revision != draft.Revision || second.Revision != draft.Revision ||
		first.SQLSnapshot != draft.SQLContent || second.SQLSnapshot != draft.SQLContent {
		t.Fatalf("retry did not preserve the original attempt: %+v", detail.Runs)
	}

	_, err = svc.UpdateDraft(ctx, devUser, UpdateDraftInput{
		DatabaseID:  dbID,
		MigrationID: draft.ID,
		Title:       "Coba ubah setelah applied",
		SQLContent:  "DROP TABLE products;",
	})
	if !errors.Is(err, ErrAlreadyApplied) {
		t.Fatalf("mengubah migrasi yang sudah APPLIED harus ditolak dengan ErrAlreadyApplied")
	}
}

func TestMigrationFailureBeforeSQLDispatch(t *testing.T) {
	for _, tc := range []struct {
		name          string
		lockError     error
		dispatchError error
	}{
		{name: "target lock fails", lockError: errors.New("lock unavailable")},
		{name: "dispatch marker fails", dispatchError: errors.New("marker unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			actor := Principal{UserID: "editor"}
			authz := newMockAuthorizer()
			authz.Grant(actor.UserID, "db", "VIEW_EDIT")
			store := newInMemoryStore()
			store.dispatchError = tc.dispatchError
			target := &mockTargetDB{lockError: tc.lockError}
			service := NewService(authz, store, target, nil)
			draft, err := service.CreateDraft(ctx, actor, CreateDraftInput{
				DatabaseID: "db", Title: "Create sample", SQLContent: "CREATE TABLE samples (id INT);",
				SQLDown: "DROP TABLE samples;",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.ExecuteMigration(ctx, actor, "db", draft.ID); !errors.Is(err, ErrExecutionFailed) {
				t.Fatalf("expected execution to fail before SQL dispatch: %v", err)
			}
			detail, err := service.GetMigrationDetail(ctx, actor, "db", draft.ID)
			if err != nil || len(detail.Runs) != 1 {
				t.Fatalf("expected one attempt in history: %+v, %v", detail, err)
			}
			run := detail.Runs[0]
			if run.Status != RunStatusFailed || run.SQLDispatchStarted == nil || *run.SQLDispatchStarted ||
				run.SQLSnapshot != draft.SQLContent || target.execCalls != 0 || target.rolledBackRuns != 1 {
				t.Fatalf("expected failure before SQL dispatch: %+v, SQL calls %d", run, target.execCalls)
			}
		})
	}
}

func TestMigrationCommitUnconfirmedKeepsHistoryOpen(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	store := newInMemoryStore()
	targetDB := &mockTargetDB{commitError: errors.New("connection lost after commit")}
	actor := Principal{UserID: "editor"}
	authz.Grant(actor.UserID, "db", "VIEW_EDIT")
	var output bytes.Buffer
	logger := SlogAuditLogger{Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	service := NewService(authz, store, targetDB, logger)
	draft, err := service.CreateDraft(ctx, actor, CreateDraftInput{
		DatabaseID: "db", Title: "Create sample", SQLContent: "CREATE TABLE samples (id INT);",
		SQLDown: "DROP TABLE samples;",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteMigration(ctx, actor, "db", draft.ID); !errors.Is(err, ErrOutcomeUnconfirmed) {
		t.Fatalf("expected an unconfirmed target commit: %v", err)
	}
	detail, err := service.GetMigrationDetail(ctx, actor, "db", draft.ID)
	if err != nil || len(detail.Runs) != 1 || detail.Runs[0].Status != RunStatusRunning || detail.Runs[0].FinishedAt != nil {
		t.Fatalf("the result must stay open: %+v, %v", detail, err)
	}
	if len(targetDB.committedSQLs) != 1 || targetDB.rolledBackRuns != 0 {
		t.Fatal("target SQL must not be retried or marked rolled back")
	}
	lastLine := strings.Split(strings.TrimSpace(output.String()), "\n")
	var event struct {
		Message     string `json:"msg"`
		Status      string `json:"status"`
		ExecutionID int64  `json:"execution_id"`
	}
	if err := json.Unmarshal([]byte(lastLine[len(lastLine)-1]), &event); err != nil ||
		event.Message != "migration_attempt_finished" || event.Status != "UNCONFIRMED" || event.ExecutionID != detail.Runs[0].ID {
		t.Fatalf("expected a structured outcome for the recorded attempt: %s", output.String())
	}
}

func TestMigrationAuditLogsDispatchedSQL(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	actor := Principal{UserID: "editor"}
	authz.Grant(actor.UserID, "db", "VIEW_EDIT")
	var output bytes.Buffer
	logger := SlogAuditLogger{Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	service := NewService(authz, newInMemoryStore(), &mockTargetDB{}, logger)
	const sqlUp = "INSERT INTO private_records VALUES (918273645, 'sample', $$private-note$$); -- first statement\nUPDATE private_records SET note = 'second';"
	draft, err := service.CreateDraft(ctx, actor, CreateDraftInput{
		DatabaseID: "db", Title: "Insert test row", SQLContent: sqlUp,
		IrreversibleReason: "No safe reversal",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ExecuteMigration(ctx, actor, "db", draft.ID)
	if err != nil || result.Run.SQLSnapshot != sqlUp {
		t.Fatalf("the recorded snapshot should retain the executed SQL: %+v, %v", result, err)
	}
	expected := map[string]string{
		"migration_sql_started":          "RUNNING",
		"migration_sql_finished":         "SUCCEEDED",
		"migration_transaction_finished": "COMMITTED",
	}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var event struct {
			Message string `json:"msg"`
			Status  string `json:"status"`
			SQL     string `json:"sql"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if status, ok := expected[event.Message]; ok {
			if event.Status != status {
				t.Fatalf("%s has status %q, want %q", event.Message, event.Status, status)
			}
			delete(expected, event.Message)
		}
		if (event.Message == "migration_sql_started" || event.Message == "migration_sql_finished") && event.SQL != sqlUp {
			t.Fatalf("%s logged SQL %q, want %q", event.Message, event.SQL, sqlUp)
		}
	}
	if len(expected) != 0 {
		t.Fatalf("missing audit events: %v", expected)
	}
}

func TestAccessControl_ViewOnlyCannotMutateOrExecute(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	store := newInMemoryStore()
	targetDB := &mockTargetDB{}

	const dbA = "db-toko"
	const dbB = "db-gudang"

	viewer := Principal{UserID: "user-2", Username: "siti_analyst"}
	editor := Principal{UserID: "user-3", Username: "andi_editor"}

	authz.Grant(viewer.UserID, dbA, "VIEW_ONLY")
	authz.Grant(editor.UserID, dbA, "VIEW_EDIT")
	authz.Grant(editor.UserID, dbB, "VIEW_EDIT")

	svc := NewService(authz, store, targetDB, nil)

	migA, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID: dbA,
		Title:      "Tambah kolom stok",
		SQLContent: "ALTER TABLE products ADD COLUMN stock INT NOT NULL DEFAULT 0;",
		SQLDown:    "ALTER TABLE products DROP COLUMN stock;",
	})
	if err != nil {
		t.Fatalf("editor gagal membuat draf: %v", err)
	}

	list, err := svc.ListMigrations(ctx, viewer, dbA)
	if err != nil || len(list) != 1 {
		t.Fatalf("View Only seharusnya bisa melihat riwayat migrasi")
	}

	if _, err := svc.CreateDraft(ctx, viewer, CreateDraftInput{
		DatabaseID: dbA,
		Title:      "Ilegal",
		SQLContent: "DROP TABLE products;",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("View Only seharusnya ditolak saat CreateDraft")
	}

	if _, err := svc.ExecuteMigration(ctx, viewer, dbA, migA.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("View Only seharusnya ditolak saat ExecuteMigration")
	}
	if _, err := svc.RollbackMigration(ctx, viewer, dbA, migA.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("View Only cannot roll back a migration")
	}
	if _, err := svc.ForwardMigration(ctx, viewer, dbA, migA.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("View Only cannot forward a migration")
	}

	if _, err := svc.ExecuteMigration(ctx, editor, dbB, migA.ID); !errors.Is(err, ErrDatabaseMismatch) {
		t.Fatalf("akses lintas database harus ditolak dengan ErrDatabaseMismatch")
	}
}

func TestBonus_ExplainAnalyzeAlwaysRollsBackAndSchemaDiff(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	store := newInMemoryStore()
	targetDB := &mockTargetDB{
		currentSchema: []TableSchema{
			{
				TableName: "users",
				Columns: []ColumnSchema{
					{Name: "id", DataType: "integer", IsNullable: false, IsPrimary: true},
					{Name: "legacy_code", DataType: "text", IsNullable: true},
				},
			},
		},
	}

	const dbID = "db-55"
	editor := Principal{UserID: "user-9", Username: "rina_lead"}
	authz.Grant(editor.UserID, dbID, "VIEW_EDIT")

	svc := NewService(authz, store, targetDB, nil)

	plan, err := svc.ExplainQuery(ctx, editor, dbID, "SELECT * FROM products;", true)
	if err != nil || plan.NodeType != "Seq Scan" {
		t.Fatalf("ExplainQuery gagal: %v", err)
	}
	if targetDB.rolledBackRuns != 1 || len(targetDB.committedSQLs) != 0 {
		t.Fatalf("ExplainQuery wajib melakukan Rollback!")
	}

	desired := []TableSchema{
		{
			TableName: "users",
			Columns: []ColumnSchema{
				{Name: "id", DataType: "integer", IsNullable: false, IsPrimary: true},
				{Name: "email", DataType: "varchar(255)", IsNullable: false},
			},
		},
		{
			TableName: "orders",
			Columns: []ColumnSchema{
				{Name: "id", DataType: "serial", IsNullable: false, IsPrimary: true},
				{Name: "user_id", DataType: "integer", IsNullable: false},
			},
		},
	}

	diff, err := svc.GenerateSchemaDiff(ctx, editor, dbID, "public", desired)
	if err != nil {
		t.Fatalf("GenerateSchemaDiff gagal: %v", err)
	}
	if len(diff.AddedTables) != 1 || len(diff.AddedColumns) != 1 || len(diff.DroppedColumns) != 1 {
		t.Fatalf("hasil diff tidak sesuai: %+v", diff)
	}
}

func TestCompareSchemas_MySQLUsesDialectSyntax(t *testing.T) {
	diff := CompareSchemasForDialect(nil, []TableSchema{{
		TableName: "order`items",
		Columns: []ColumnSchema{
			{Name: "item`id", DataType: "BIGINT", IsPrimary: true, IsNullable: false},
		},
	}}, DialectMySQL)
	if !strings.Contains(diff.CandidateSQL, "`order``items`") || !strings.Contains(diff.CandidateSQL, "`item``id`") {
		t.Fatalf("MySQL candidate SQL must escape identifiers with backticks: %s", diff.CandidateSQL)
	}
}

func TestExecuteMigration_RejectsImplicitCommitSQLForMySQL(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	store := newInMemoryStore()
	targetDB := &mockTargetDB{dialect: DialectMySQL}
	user := Principal{UserID: "user-1"}
	authz.Grant(user.UserID, "mysql-db", "VIEW_EDIT")
	service := NewService(authz, store, targetDB, nil)

	draft, err := service.CreateDraft(ctx, user, CreateDraftInput{
		DatabaseID:         "mysql-db",
		Title:              "Create table",
		SQLContent:         "CREATE TABLE products (id INT);",
		IrreversibleReason: "Test migration without SQL down",
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if _, err := service.ExecuteMigration(ctx, user, "mysql-db", draft.ID); !errors.Is(err, ErrInvalidSQL) {
		t.Fatalf("expected implicit-commit DDL to be rejected, got %v", err)
	}
	runs, err := store.ListRunsByMigration(ctx, draft.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("rejected SQL must not create an execution run: %v (%d runs)", err, len(runs))
	}
	if targetDB.rolledBackRuns != 1 || len(targetDB.committedSQLs) != 0 {
		t.Fatal("transaction opened for validation must be rolled back without executing SQL")
	}
}
