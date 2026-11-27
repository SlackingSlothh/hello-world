package migrations

import (
	"io/fs"
	"strings"
	"testing"
)

func TestEmbeddedMigrationsPresentAndNonEmpty(t *testing.T) {
	expected := []string{
		"0023_migration_run_sql_dispatch.sql",
		"0024_migration_run_transaction_evidence.sql",
		"0025_migration_batch_config.sql",
		"0026_migration_jobs.sql",
		"0027_migration_jobs_queue_index.sql",
		"0028_migration_jobs_database_index.sql",
		"0029_database_locks.sql",
		"0030_database_locks_lease_index.sql",
		"0031_migration_runs_async_fields.sql",
	}
	for _, name := range expected {
		data, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("missing embedded migration %s: %v", name, err)
		}
		if strings.TrimSpace(string(data)) == "" {
			t.Fatalf("embedded migration %s is empty", name)
		}
	}
}
