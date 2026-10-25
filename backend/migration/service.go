package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"
)

var (
	// sesi pengguna tidak valid
	ErrUnauthorized = errors.New("migration: sesi pengguna tidak sah")
	// pengguna tidak punya hak akses pada database target
	ErrForbidden = errors.New("migration: akses ditolak untuk operasi ini pada database target")
	// data migrasi tidak ditemukan
	ErrNotFound = errors.New("migration: data migrasi tidak ditemukan")
	// id migrasi bukan milik database yang diminta
	ErrDatabaseMismatch = errors.New("migration: ID migrasi tidak terdaftar pada database target tersebut")
	// migrasi yang sudah diterapkan tidak bisa diubah atau dijalankan ulang
	ErrAlreadyApplied = errors.New("migration: migrasi sudah diterapkan dan tidak dapat diubah atau dijalankan ulang")
	// sedang ada migrasi lain yang berjalan di database target yang sama
	ErrMigrationLocked = errors.New("migration: sedang ada eksekusi migrasi lain yang berjalan pada database target ini")
	// kueri sql kosong atau mengandung perintah transaksi manual
	ErrInvalidSQL = errors.New("migration: kueri SQL tidak valid atau melanggar aturan transaksi")
	// eksekusi sql gagal di database target dan sudah di-rollback
	ErrExecutionFailed = errors.New("migration: eksekusi SQL gagal pada database target dan perubahan telah di-rollback")
)

// status rancangan migrasi pada tabel migrations
type Status string

const (
	StatusDraft   Status = "DRAFT"   // baru dibuat atau diedit dan belum pernah berhasil dieksekusi
	StatusFailed  Status = "FAILED"  // percobaan eksekusi terakhir gagal dan masih bisa diperbaiki
	StatusApplied Status = "APPLIED" // sudah berhasil di-commit ke database target dan terkunci
)

// status setiap percobaan eksekusi pada tabel migration_runs
type RunStatus string

const (
	RunStatusRunning   RunStatus = "RUNNING"
	RunStatusSucceeded RunStatus = "SUCCEEDED"
	RunStatusFailed    RunStatus = "FAILED"
)

// jenis aksi untuk pemeriksaan otorisasi
type Action string

const (
	ActionReadMigration    Action = "migration.read"    // boleh untuk view only dan view edit
	ActionCreateMigration  Action = "migration.create"  // khusus view edit
	ActionUpdateMigration  Action = "migration.update"  // khusus view edit
	ActionExecuteMigration Action = "migration.execute" // khusus view edit
)

// identitas pengguna dari sesi yang sudah terverifikasi
type Principal struct {
	UserID   string
	Username string
}

// representasi satu baris pada tabel migrations di database platform
type Migration struct {
	ID          int64      `json:"id"`
	DatabaseID  string     `json:"database_id"`
	Version     *int64     `json:"version,omitempty"` // bernilai nil saat draft atau failed, berisi nomor urut saat applied
	Title       string     `json:"title"`
	Description string     `json:"description"`
	SQLContent  string     `json:"sql_content"`
	Status      Status     `json:"status"`
	CreatedBy   string     `json:"created_by"`
	UpdatedBy   string     `json:"updated_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	AppliedAt   *time.Time `json:"applied_at,omitempty"`
}

// representasi satu baris riwayat percobaan pada tabel migration_runs
type MigrationRun struct {
	ID            int64      `json:"id"`
	MigrationID   int64      `json:"migration_id"`
	DatabaseID    string     `json:"database_id"`
	AttemptNumber int        `json:"attempt_number"`
	SQLSnapshot   string     `json:"sql_snapshot"` // salinan sql saat percobaan dijalankan
	Status        RunStatus  `json:"status"`
	ExecutedBy    string     `json:"executed_by"`
	ErrorMessage  *string    `json:"error_message,omitempty"`
	ExecutionMs   int64      `json:"execution_ms"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// gabungan data migrasi beserta daftar riwayat percobaannya
type MigrationDetail struct {
	Migration Migration      `json:"migration"`
	Runs      []MigrationRun `json:"runs"`
}

// kontrak pemeriksaan hak akses ke modul access control
type Authorizer interface {
	RequireDatabase(ctx context.Context, principal Principal, databaseID string, action Action) error
}

// adaptor agar fungsi biasa bisa langsung memenuhi interface authorizer
type AuthorizerFunc func(ctx context.Context, principal Principal, databaseID string, action Action) error

func (fn AuthorizerFunc) RequireDatabase(ctx context.Context, principal Principal, databaseID string, action Action) error {
	return fn(ctx, principal, databaseID, action)
}

// kontrak penyimpanan data migrasi di database platform
type Store interface {
	CreateMigration(ctx context.Context, m *Migration) error
	GetMigrationByID(ctx context.Context, migrationID int64) (*Migration, error)
	ListMigrationsByDatabase(ctx context.Context, databaseID string) ([]Migration, error)
	UpdateDraftSQL(ctx context.Context, migrationID int64, title, description, sqlContent, updatedBy string, updatedAt time.Time) error

	CreateRun(ctx context.Context, run *MigrationRun) error
	CompleteRunFailed(ctx context.Context, runID, migrationID int64, errMsg string, execMs int64, finishedAt time.Time) error
	CompleteRunSucceeded(ctx context.Context, runID, migrationID int64, databaseID string, execMs int64, finishedAt time.Time) (assignedVersion int64, err error)
	ListRunsByMigration(ctx context.Context, migrationID int64) ([]MigrationRun, error)
}

// kontrak untuk membuka transaksi ke database target
type TargetConnector interface {
	BeginTargetTx(ctx context.Context, databaseID string) (TargetTx, error)
}

// kontrak operasi transaksi di database target
type TargetTx interface {
	AcquireTargetLock(ctx context.Context, databaseID string) error
	ExecSQL(ctx context.Context, sqlQuery string) error
	ExplainSQL(ctx context.Context, sqlQuery string, analyze bool) (string, error)
	InspectSchema(ctx context.Context, schemaName string) ([]TableSchema, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// struktur data catatan aktivitas audit
type AuditEvent struct {
	ActorUserID string
	Action      string
	ObjectType  string
	ObjectID    int64
	DatabaseID  string
	Status      string
	Message     string
	CreatedAt   time.Time
}

// kontrak opsional untuk mencatat aktivitas ke log audit
type AuditLogger interface {
	Log(ctx context.Context, event AuditEvent)
}

// mengubah id database bertipe string menjadi angka int64 untuk advisory lock postgresql
func AdvisoryLockKey(databaseID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("migration-lock:" + databaseID))
	return int64(h.Sum64())
}

// memvalidasi isi sql agar tidak kosong dan tidak memakai perintah transaksi manual
func ValidateTransactionalSQL(rawSQL string) error {
	trimmed := strings.TrimSpace(rawSQL)
	if trimmed == "" {
		return fmt.Errorf("%w: kueri SQL tidak boleh kosong", ErrInvalidSQL)
	}

	// bersihkan komentar dan isi string sebelum mengecek kata kunci sql
	cleaned := stripSQLCommentsAndStrings(trimmed)
	statements := strings.Split(cleaned, ";")

	hasExecutableStatement := false
	for _, stmt := range statements {
		tokens := strings.Fields(strings.ToUpper(stmt))
		if len(tokens) == 0 {
			continue
		}
		hasExecutableStatement = true
		first := tokens[0]

		// tolak perintah yang mengganggu transaksi otomatis atau tidak bisa berjalan di dalam transaksi
		switch first {
		case "BEGIN", "COMMIT", "END", "ROLLBACK", "ABORT", "SAVEPOINT", "RELEASE":
			return fmt.Errorf("%w: perintah transaksi manual (%s) tidak diizinkan karena transaksi dikelola otomatis oleh sistem", ErrInvalidSQL, first)
		case "START":
			if len(tokens) >= 2 && tokens[1] == "TRANSACTION" {
				return fmt.Errorf("%w: perintah START TRANSACTION tidak diizinkan di dalam migrasi", ErrInvalidSQL)
			}
		case "VACUUM", "CLUSTER":
			return fmt.Errorf("%w: perintah %s tidak dapat dijalankan di dalam transaksi migrasi", ErrInvalidSQL, first)
		case "CREATE", "DROP":
			if len(tokens) >= 2 && (tokens[1] == "DATABASE" || tokens[1] == "TABLESPACE") {
				return fmt.Errorf("%w: perintah %s %s tidak dapat dijalankan di dalam blok transaksi PostgreSQL", ErrInvalidSQL, first, tokens[1])
			}
			for _, tok := range tokens {
				if tok == "CONCURRENTLY" {
					return fmt.Errorf("%w: opsi CONCURRENTLY tidak didukung di dalam migrasi transaksional", ErrInvalidSQL)
				}
			}
		case "REINDEX":
			for _, tok := range tokens {
				if tok == "CONCURRENTLY" {
					return fmt.Errorf("%w: REINDEX CONCURRENTLY tidak didukung di dalam migrasi transaksional", ErrInvalidSQL)
				}
			}
		}
	}

	if !hasExecutableStatement {
		return fmt.Errorf("%w: kueri SQL hanya berisi komentar tanpa perintah yang dapat dijalankan", ErrInvalidSQL)
	}
	return nil
}

// menghapus komentar dan isi literal string supaya kata kunci di dalamnya tidak ikut terdeteksi
func stripSQLCommentsAndStrings(sqlText string) string {
	var out strings.Builder
	out.Grow(len(sqlText))

	inSingleQuote := false
	inDoubleQuote := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(sqlText); i++ {
		ch := sqlText[i]
		var next byte
		if i+1 < len(sqlText) {
			next = sqlText[i+1]
		}

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				out.WriteByte('\n')
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inSingleQuote {
			if ch == '\'' {
				if next == '\'' {
					i++
					continue
				}
				inSingleQuote = false
				out.WriteByte('\'')
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if next == '"' {
					i++
					continue
				}
				inDoubleQuote = false
				out.WriteByte('"')
			}
			continue
		}

		if ch == '-' && next == '-' {
			inLineComment = true
			i++
			continue
		}
		if ch == '/' && next == '*' {
			inBlockComment = true
			i++
			continue
		}
		if ch == '\'' {
			inSingleQuote = true
			out.WriteByte('\'')
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			out.WriteByte('"')
			continue
		}

		out.WriteByte(ch)
	}
	return out.String()
}

// layanan utama pengelola logika bisnis migrasi
type Service struct {
	authz     Authorizer
	store     Store
	connector TargetConnector
	audit     AuditLogger
	nowFn     func() time.Time

	mu      sync.Mutex
	dbLocks map[string]bool
}

// membuat instance service migrasi baru
func NewService(authz Authorizer, store Store, connector TargetConnector, audit AuditLogger) *Service {
	return &Service{
		authz:     authz,
		store:     store,
		connector: connector,
		audit:     audit,
		nowFn:     func() time.Time { return time.Now().UTC() },
		dbLocks:   make(map[string]bool),
	}
}

// mengganti fungsi waktu saat pengujian
func (s *Service) SetClock(nowFn func() time.Time) {
	if nowFn != nil {
		s.nowFn = nowFn
	}
}

type CreateDraftInput struct {
	DatabaseID  string
	Title       string
	Description string
	SQLContent  string
}

// membuat dan menyimpan rancangan migrasi baru dengan status draft
func (s *Service) CreateDraft(ctx context.Context, principal Principal, in CreateDraftInput) (*Migration, error) {
	if err := s.authorize(ctx, principal, in.DatabaseID, ActionCreateMigration); err != nil {
		return nil, err
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, fmt.Errorf("%w: judul migrasi wajib diisi", ErrInvalidSQL)
	}
	if err := ValidateTransactionalSQL(in.SQLContent); err != nil {
		return nil, err
	}

	now := s.nowFn()
	m := &Migration{
		DatabaseID:  in.DatabaseID,
		Title:       title,
		Description: strings.TrimSpace(in.Description),
		SQLContent:  strings.TrimSpace(in.SQLContent),
		Status:      StatusDraft,
		CreatedBy:   principal.UserID,
		UpdatedBy:   principal.UserID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.store.CreateMigration(ctx, m); err != nil {
		return nil, err
	}

	s.logAudit(ctx, AuditEvent{
		ActorUserID: principal.UserID,
		Action:      string(ActionCreateMigration),
		ObjectType:  "migration",
		ObjectID:    m.ID,
		DatabaseID:  m.DatabaseID,
		Status:      "SUCCESS",
		Message:     fmt.Sprintf("Membuat rancangan migrasi %q", m.Title),
		CreatedAt:   now,
	})
	return m, nil
}

type UpdateDraftInput struct {
	DatabaseID  string
	MigrationID int64
	Title       string
	Description string
	SQLContent  string
}

// memperbarui rancangan sql selama statusnya belum applied
func (s *Service) UpdateDraft(ctx context.Context, principal Principal, in UpdateDraftInput) (*Migration, error) {
	if err := s.authorize(ctx, principal, in.DatabaseID, ActionUpdateMigration); err != nil {
		return nil, err
	}

	existing, err := s.getVerifiedMigration(ctx, in.DatabaseID, in.MigrationID)
	if err != nil {
		return nil, err
	}
	if existing.Status == StatusApplied {
		return nil, ErrAlreadyApplied
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = existing.Title
	}
	if err := ValidateTransactionalSQL(in.SQLContent); err != nil {
		return nil, err
	}

	now := s.nowFn()
	cleanedSQL := strings.TrimSpace(in.SQLContent)
	cleanedDesc := strings.TrimSpace(in.Description)

	if err := s.store.UpdateDraftSQL(ctx, existing.ID, title, cleanedDesc, cleanedSQL, principal.UserID, now); err != nil {
		return nil, err
	}

	existing.Title = title
	existing.Description = cleanedDesc
	existing.SQLContent = cleanedSQL
	existing.UpdatedBy = principal.UserID
	existing.UpdatedAt = now

	s.logAudit(ctx, AuditEvent{
		ActorUserID: principal.UserID,
		Action:      string(ActionUpdateMigration),
		ObjectType:  "migration",
		ObjectID:    existing.ID,
		DatabaseID:  existing.DatabaseID,
		Status:      "SUCCESS",
		Message:     fmt.Sprintf("Memperbarui rancangan migrasi %q", existing.Title),
		CreatedAt:   now,
	})
	return existing, nil
}

// mengambil daftar migrasi untuk satu database target
func (s *Service) ListMigrations(ctx context.Context, principal Principal, databaseID string) ([]Migration, error) {
	if err := s.authorize(ctx, principal, databaseID, ActionReadMigration); err != nil {
		return nil, err
	}
	return s.store.ListMigrationsByDatabase(ctx, databaseID)
}

// mengambil detail migrasi beserta riwayat percobaan eksekusinya
func (s *Service) GetMigrationDetail(ctx context.Context, principal Principal, databaseID string, migrationID int64) (*MigrationDetail, error) {
	if err := s.authorize(ctx, principal, databaseID, ActionReadMigration); err != nil {
		return nil, err
	}

	m, err := s.getVerifiedMigration(ctx, databaseID, migrationID)
	if err != nil {
		return nil, err
	}

	runs, err := s.store.ListRunsByMigration(ctx, m.ID)
	if err != nil {
		return nil, err
	}

	return &MigrationDetail{
		Migration: *m,
		Runs:      runs,
	}, nil
}

type ExecuteResult struct {
	Migration Migration    `json:"migration"`
	Run       MigrationRun `json:"run"`
}

// menjalankan sql migrasi pada database target di dalam transaksi otomatis
func (s *Service) ExecuteMigration(ctx context.Context, principal Principal, databaseID string, migrationID int64) (*ExecuteResult, error) {
	if err := s.authorize(ctx, principal, databaseID, ActionExecuteMigration); err != nil {
		return nil, err
	}

	m, err := s.getVerifiedMigration(ctx, databaseID, migrationID)
	if err != nil {
		return nil, err
	}
	if m.Status == StatusApplied {
		return nil, ErrAlreadyApplied
	}
	if err := ValidateTransactionalSQL(m.SQLContent); err != nil {
		return nil, err
	}

	// cegah dua migrasi berjalan bersamaan pada database target yang sama
	if !s.tryLockDatabase(databaseID) {
		return nil, ErrMigrationLocked
	}
	defer s.unlockDatabase(databaseID)

	prevRuns, err := s.store.ListRunsByMigration(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	attemptNum := len(prevRuns) + 1

	// catat awal percobaan eksekusi di database platform
	startTime := s.nowFn()
	run := &MigrationRun{
		MigrationID:   m.ID,
		DatabaseID:    databaseID,
		AttemptNumber: attemptNum,
		SQLSnapshot:   m.SQLContent,
		Status:        RunStatusRunning,
		ExecutedBy:    principal.UserID,
		StartedAt:     startTime,
	}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return nil, err
	}

	// buka transaksi ke database target dan pastikan rollback jika belum commit
	tx, err := s.connector.BeginTargetTx(ctx, databaseID)
	if err != nil {
		return s.failRun(ctx, principal, m, run, startTime, fmt.Sprintf("gagal membuka transaksi ke database target: %v", err))
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if err := tx.AcquireTargetLock(ctx, databaseID); err != nil {
		return s.failRun(ctx, principal, m, run, startTime, fmt.Sprintf("gagal mengunci urutan migrasi pada database target: %v", err))
	}

	// jalankan kueri sql pada database target, langsung rollback jika gagal
	if err := tx.ExecSQL(ctx, m.SQLContent); err != nil {
		_ = tx.Rollback(ctx)
		committed = true
		return s.failRun(ctx, principal, m, run, startTime, err.Error())
	}

	// simpan perubahan secara permanen di database target
	if err := tx.Commit(ctx); err != nil {
		return s.failRun(ctx, principal, m, run, startTime, fmt.Sprintf("gagal melakukan commit transaksi pada database target: %v", err))
	}
	committed = true

	finishTime := s.nowFn()
	execMs := finishTime.Sub(startTime).Milliseconds()
	if execMs < 0 {
		execMs = 0
	}

	// tetapkan nomor versi linear dan tandai migrasi sebagai applied
	assignedVersion, err := s.store.CompleteRunSucceeded(ctx, run.ID, m.ID, databaseID, execMs, finishTime)
	if err != nil {
		return nil, fmt.Errorf("perubahan telah di-commit ke database target namun gagal memperbarui riwayat platform: %w", err)
	}

	m.Status = StatusApplied
	m.Version = &assignedVersion
	m.AppliedAt = &finishTime
	m.UpdatedAt = finishTime

	run.Status = RunStatusSucceeded
	run.ExecutionMs = execMs
	run.FinishedAt = &finishTime

	s.logAudit(ctx, AuditEvent{
		ActorUserID: principal.UserID,
		Action:      string(ActionExecuteMigration),
		ObjectType:  "migration",
		ObjectID:    m.ID,
		DatabaseID:  databaseID,
		Status:      "SUCCESS",
		Message:     fmt.Sprintf("Migrasi #%d (%s) berhasil diterapkan sebagai versi v%d", m.ID, m.Title, assignedVersion),
		CreatedAt:   finishTime,
	})

	return &ExecuteResult{
		Migration: *m,
		Run:       *run,
	}, nil
}

// mencatat kegagalan eksekusi migrasi beserta pesan errornya ke database platform
func (s *Service) failRun(ctx context.Context, principal Principal, m *Migration, run *MigrationRun, startTime time.Time, errMsg string) (*ExecuteResult, error) {
	finishTime := s.nowFn()
	execMs := finishTime.Sub(startTime).Milliseconds()
	if execMs < 0 {
		execMs = 0
	}

	_ = s.store.CompleteRunFailed(ctx, run.ID, m.ID, errMsg, execMs, finishTime)

	m.Status = StatusFailed
	m.UpdatedAt = finishTime

	run.Status = RunStatusFailed
	run.ErrorMessage = &errMsg
	run.ExecutionMs = execMs
	run.FinishedAt = &finishTime

	s.logAudit(ctx, AuditEvent{
		ActorUserID: principal.UserID,
		Action:      string(ActionExecuteMigration),
		ObjectType:  "migration",
		ObjectID:    m.ID,
		DatabaseID:  m.DatabaseID,
		Status:      "FAILED",
		Message:     fmt.Sprintf("Migrasi #%d gagal dan di-rollback: %s", m.ID, errMsg),
		CreatedAt:   finishTime,
	})

	return &ExecuteResult{
		Migration: *m,
		Run:       *run,
	}, fmt.Errorf("%w: %s", ErrExecutionFailed, errMsg)
}

// ringkasan rencana eksekusi kueri dari hasil explain postgresql
type ExplainPlanSummary struct {
	NodeType          string               `json:"node_type"`
	RelationName      string               `json:"relation_name,omitempty"`
	IndexName         string               `json:"index_name,omitempty"`
	JoinType          string               `json:"join_type,omitempty"`
	StartupCost       float64              `json:"startup_cost"`
	TotalCost         float64              `json:"total_cost"`
	PlanRows          int64                `json:"plan_rows"`
	ActualTotalTimeMs *float64             `json:"actual_total_time_ms,omitempty"`
	ActualRows        *int64               `json:"actual_rows,omitempty"`
	PlanningTimeMs    *float64             `json:"planning_time_ms,omitempty"`
	ExecutionTimeMs   *float64             `json:"execution_time_ms,omitempty"`
	Children          []ExplainPlanSummary `json:"children,omitempty"`
	RawJSON           string               `json:"raw_json"`
}

// menjalankan explain atau explain analyze lalu selalu melakukan rollback agar data aman
func (s *Service) ExplainQuery(ctx context.Context, principal Principal, databaseID, sqlQuery string, analyze bool) (*ExplainPlanSummary, error) {
	if err := s.authorize(ctx, principal, databaseID, ActionExecuteMigration); err != nil {
		return nil, err
	}
	if err := ValidateTransactionalSQL(sqlQuery); err != nil {
		return nil, err
	}

	tx, err := s.connector.BeginTargetTx(ctx, databaseID)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka koneksi ke database target untuk EXPLAIN: %w", err)
	}
	// selalu batalkan transaksi setelah explain selesai
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	rawJSON, err := tx.ExplainSQL(ctx, sqlQuery, analyze)
	if err != nil {
		return nil, fmt.Errorf("gagal menjalankan EXPLAIN pada database target: %w", err)
	}

	summary, err := parsePostgresExplainJSON(rawJSON)
	if err != nil {
		return &ExplainPlanSummary{
			NodeType: "CustomPlan",
			RawJSON:  rawJSON,
		}, nil
	}
	return summary, nil
}

type pgExplainRoot struct {
	Plan          pgExplainNode `json:"Plan"`
	PlanningTime  *float64      `json:"Planning Time,omitempty"`
	ExecutionTime *float64      `json:"Execution Time,omitempty"`
}

type pgExplainNode struct {
	NodeType        string          `json:"Node Type"`
	RelationName    string          `json:"Relation Name,omitempty"`
	IndexName       string          `json:"Index Name,omitempty"`
	JoinType        string          `json:"Join Type,omitempty"`
	StartupCost     float64         `json:"Startup Cost"`
	TotalCost       float64         `json:"Total Cost"`
	PlanRows        int64           `json:"Plan Rows"`
	ActualTotalTime *float64        `json:"Actual Total Time,omitempty"`
	ActualRows      *int64          `json:"Actual Rows,omitempty"`
	Plans           []pgExplainNode `json:"Plans,omitempty"`
}

// mengubah json explain dari postgresql menjadi struct ringkasan
func parsePostgresExplainJSON(rawJSON string) (*ExplainPlanSummary, error) {
	var roots []pgExplainRoot
	if err := json.Unmarshal([]byte(rawJSON), &roots); err != nil || len(roots) == 0 {
		return nil, fmt.Errorf("format JSON EXPLAIN tidak dikenali: %w", err)
	}
	root := roots[0]
	summary := convertExplainNode(root.Plan)
	summary.PlanningTimeMs = root.PlanningTime
	summary.ExecutionTimeMs = root.ExecutionTime
	summary.RawJSON = rawJSON
	return &summary, nil
}

// menyalin pohon node explain secara rekursif
func convertExplainNode(n pgExplainNode) ExplainPlanSummary {
	out := ExplainPlanSummary{
		NodeType:          n.NodeType,
		RelationName:      n.RelationName,
		IndexName:         n.IndexName,
		JoinType:          n.JoinType,
		StartupCost:       n.StartupCost,
		TotalCost:         n.TotalCost,
		PlanRows:          n.PlanRows,
		ActualTotalTimeMs: n.ActualTotalTime,
		ActualRows:        n.ActualRows,
	}
	for _, child := range n.Plans {
		out.Children = append(out.Children, convertExplainNode(child))
	}
	return out
}

type ColumnSchema struct {
	Name       string  `json:"name"`
	DataType   string  `json:"data_type"`
	IsNullable bool    `json:"is_nullable"`
	DefaultVal *string `json:"default_val,omitempty"`
	IsPrimary  bool    `json:"is_primary,omitempty"`
}

type TableSchema struct {
	TableName string         `json:"table_name"`
	Columns   []ColumnSchema `json:"columns"`
}

// hasil perbandingan schema beserta kandidat sql migrasinya
type SchemaDiffResult struct {
	AddedTables     []string `json:"added_tables"`
	DroppedTables   []string `json:"dropped_tables"`
	AddedColumns    []string `json:"added_columns"`
	ModifiedColumns []string `json:"modified_columns"`
	DroppedColumns  []string `json:"dropped_columns"`
	CandidateSQL    string   `json:"candidate_sql"`
}

// membaca struktur tabel di database target lalu membandingkannya dengan struktur tujuan
func (s *Service) GenerateSchemaDiff(ctx context.Context, principal Principal, databaseID, schemaName string, desiredTables []TableSchema) (*SchemaDiffResult, error) {
	if err := s.authorize(ctx, principal, databaseID, ActionReadMigration); err != nil {
		return nil, err
	}
	if strings.TrimSpace(schemaName) == "" {
		schemaName = "public"
	}

	tx, err := s.connector.BeginTargetTx(ctx, databaseID)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka koneksi ke database target untuk Schema Diff: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	currentTables, err := tx.InspectSchema(ctx, schemaName)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca struktur schema dari database target: %w", err)
	}

	return CompareSchemas(currentTables, desiredTables), nil
}

// mencari perbedaan tabel dan kolom untuk menghasilkan kandidat kueri sql migrasi
func CompareSchemas(current, desired []TableSchema) *SchemaDiffResult {
	res := &SchemaDiffResult{}
	var sqlLines []string

	currentMap := make(map[string]TableSchema, len(current))
	for _, t := range current {
		currentMap[strings.ToLower(t.TableName)] = t
	}

	desiredMap := make(map[string]TableSchema, len(desired))
	for _, t := range desired {
		desiredMap[strings.ToLower(t.TableName)] = t
	}

	// cek tabel baru dan perubahan kolom pada tabel yang sudah ada
	for _, wantTable := range desired {
		tKey := strings.ToLower(wantTable.TableName)
		currTable, exists := currentMap[tKey]
		if !exists {
			res.AddedTables = append(res.AddedTables, wantTable.TableName)
			sqlLines = append(sqlLines, buildCreateTableSQL(wantTable))
			continue
		}

		currCols := make(map[string]ColumnSchema, len(currTable.Columns))
		for _, c := range currTable.Columns {
			currCols[strings.ToLower(c.Name)] = c
		}
		wantCols := make(map[string]ColumnSchema, len(wantTable.Columns))
		for _, c := range wantTable.Columns {
			wantCols[strings.ToLower(c.Name)] = c
		}

		for _, wantCol := range wantTable.Columns {
			cKey := strings.ToLower(wantCol.Name)
			currCol, colExists := currCols[cKey]
			if !colExists {
				res.AddedColumns = append(res.AddedColumns, fmt.Sprintf("%s.%s", wantTable.TableName, wantCol.Name))
				sqlLines = append(sqlLines, buildAddColumnSQL(wantTable.TableName, wantCol))
				continue
			}

			if !strings.EqualFold(strings.TrimSpace(currCol.DataType), strings.TrimSpace(wantCol.DataType)) {
				res.ModifiedColumns = append(res.ModifiedColumns, fmt.Sprintf("%s.%s (type: %s -> %s)", wantTable.TableName, wantCol.Name, currCol.DataType, wantCol.DataType))
				sqlLines = append(sqlLines, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;",
					QuoteIdent(wantTable.TableName), QuoteIdent(wantCol.Name), wantCol.DataType))
			}
			if currCol.IsNullable != wantCol.IsNullable {
				nullAction := "SET NOT NULL"
				if wantCol.IsNullable {
					nullAction = "DROP NOT NULL"
				}
				res.ModifiedColumns = append(res.ModifiedColumns, fmt.Sprintf("%s.%s (%s)", wantTable.TableName, wantCol.Name, nullAction))
				sqlLines = append(sqlLines, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s %s;",
					QuoteIdent(wantTable.TableName), QuoteIdent(wantCol.Name), nullAction))
			}
		}

		for _, currCol := range currTable.Columns {
			cKey := strings.ToLower(currCol.Name)
			if _, stillWanted := wantCols[cKey]; !stillWanted {
				res.DroppedColumns = append(res.DroppedColumns, fmt.Sprintf("%s.%s", wantTable.TableName, currCol.Name))
				sqlLines = append(sqlLines, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;",
					QuoteIdent(wantTable.TableName), QuoteIdent(currCol.Name)))
			}
		}
	}

	// cek tabel yang dihapus
	for _, currTable := range current {
		tKey := strings.ToLower(currTable.TableName)
		if _, stillWanted := desiredMap[tKey]; !stillWanted {
			res.DroppedTables = append(res.DroppedTables, currTable.TableName)
			sqlLines = append(sqlLines, fmt.Sprintf("DROP TABLE %s;", QuoteIdent(currTable.TableName)))
		}
	}

	if len(sqlLines) == 0 {
		res.CandidateSQL = "-- Tidak ada perbedaan schema (struktur database sudah sesuai)."
	} else {
		res.CandidateSQL = strings.Join(sqlLines, "\n\n")
	}
	return res
}

// menyusun perintah sql create table dari definisi tabel
func buildCreateTableSQL(t TableSchema) string {
	var colDefs []string
	for _, c := range t.Columns {
		def := fmt.Sprintf("    %s %s", QuoteIdent(c.Name), c.DataType)
		if c.IsPrimary {
			def += " PRIMARY KEY"
		} else if !c.IsNullable {
			def += " NOT NULL"
		}
		if c.DefaultVal != nil && strings.TrimSpace(*c.DefaultVal) != "" {
			def += " DEFAULT " + *c.DefaultVal
		}
		colDefs = append(colDefs, def)
	}
	return fmt.Sprintf("CREATE TABLE %s (\n%s\n);", QuoteIdent(t.TableName), strings.Join(colDefs, ",\n"))
}

// menyusun perintah sql alter table add column
func buildAddColumnSQL(tableName string, c ColumnSchema) string {
	def := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", QuoteIdent(tableName), QuoteIdent(c.Name), c.DataType)
	if !c.IsNullable {
		def += " NOT NULL"
	}
	if c.DefaultVal != nil && strings.TrimSpace(*c.DefaultVal) != "" {
		def += " DEFAULT " + *c.DefaultVal
	}
	return def + ";"
}

// mengutip nama tabel atau kolom dengan tanda petik ganda agar aman digunakan di sql
func QuoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

// memeriksa validitas principal dan izin akses terhadap database target
func (s *Service) authorize(ctx context.Context, principal Principal, databaseID string, action Action) error {
	if strings.TrimSpace(principal.UserID) == "" {
		return ErrUnauthorized
	}
	if strings.TrimSpace(databaseID) == "" {
		return ErrForbidden
	}
	if s.authz == nil {
		return ErrForbidden
	}
	if err := s.authz.RequireDatabase(ctx, principal, databaseID, action); err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrForbidden, err)
	}
	return nil
}

// mengambil data migrasi dan memastikan migrasi tersebut milik database yang sesuai
func (s *Service) getVerifiedMigration(ctx context.Context, databaseID string, migrationID int64) (*Migration, error) {
	m, err := s.store.GetMigrationByID(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrNotFound
	}
	if m.DatabaseID != databaseID {
		return nil, ErrDatabaseMismatch
	}
	return m, nil
}

// mengunci proses migrasi di memori untuk satu database target
func (s *Service) tryLockDatabase(databaseID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dbLocks[databaseID] {
		return false
	}
	s.dbLocks[databaseID] = true
	return true
}

// melepas kunci proses migrasi di memori
func (s *Service) unlockDatabase(databaseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.dbLocks, databaseID)
}

// mengirim catatan aktivitas ke audit logger jika tersedia
func (s *Service) logAudit(ctx context.Context, ev AuditEvent) {
	if s.audit != nil {
		s.audit.Log(ctx, ev)
	}
}