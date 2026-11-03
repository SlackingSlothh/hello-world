package migration

import (
	"context"
	"errors"
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
	mu          sync.Mutex
	nextMigID   int64
	nextRunID   int64
	migrations  map[int64]*Migration
	runsByMigID map[int64][]MigrationRun
	maxVerByDB  map[string]int64
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

func (s *inMemoryStore) UpdateDraftSQL(_ context.Context, migrationID int64, title, description, sqlContent, updatedBy string, updatedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.migrations[migrationID]
	if !ok {
		return ErrNotFound
	}
	if m.Status == StatusApplied {
		return ErrAlreadyApplied
	}
	m.Title = title
	m.Description = description
	m.SQLContent = sqlContent
	m.UpdatedBy = updatedBy
	m.UpdatedAt = updatedAt
	return nil
}

func (s *inMemoryStore) CreateRun(_ context.Context, run *MigrationRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	run.ID = s.nextRunID
	s.nextRunID++
	cp := *run
	s.runsByMigID[run.MigrationID] = append(s.runsByMigID[run.MigrationID], cp)
	return nil
}

func (s *inMemoryStore) CompleteRunFailed(_ context.Context, runID, migrationID int64, errMsg string, execMs int64, finishedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if m, ok := s.migrations[migrationID]; ok && m.Status != StatusApplied {
		m.Status = StatusFailed
		m.UpdatedAt = finishedAt
	}
	runs := s.runsByMigID[migrationID]
	for i := range runs {
		if runs[i].ID == runID {
			runs[i].Status = RunStatusFailed
			msg := errMsg
			runs[i].ErrorMessage = &msg
			runs[i].ExecutionMs = execMs
			t := finishedAt
			runs[i].FinishedAt = &t
			break
		}
	}
	return nil
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

type mockTargetDB struct {
	mu             sync.Mutex
	shouldFailSQL  bool
	failErrMsg     string
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
	return nil
}

func (tx *mockTargetTx) ExecSQL(_ context.Context, sqlQuery string) error {
	tx.parent.mu.Lock()
	defer tx.parent.mu.Unlock()

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
	return nil
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

func TestMigrationLifecycle_FailRollbackThenFixAndCommit(t *testing.T) {
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
		SQLContent:  "CREATE TABEL products (id SERIAL PRIMARY KEY);",
	})
	if err != nil {
		t.Fatalf("CreateDraft gagal: %v", err)
	}
	if draft.Status != StatusDraft || draft.Version != nil {
		t.Fatalf("status awal harus DRAFT dengan Version nil")
	}

	targetDB.shouldFailSQL = true
	targetDB.failErrMsg = `ERROR: syntax error at or near "TABEL" (SQLSTATE 42601)`

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

	fixedSQL := "CREATE TABLE products (id SERIAL PRIMARY KEY, name TEXT NOT NULL);"
	_, err = svc.UpdateDraft(ctx, devUser, UpdateDraftInput{
		DatabaseID:  dbID,
		MigrationID: draft.ID,
		Title:       "Buat tabel produk",
		Description: "Sudah diperbaiki typo TABEL -> TABLE",
		SQLContent:  fixedSQL,
	})
	if err != nil {
		t.Fatalf("UpdateDraft setelah gagal seharusnya diizinkan: %v", err)
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
		DatabaseID: "mysql-db",
		Title:      "Create table",
		SQLContent: "CREATE TABLE products (id INT);",
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
