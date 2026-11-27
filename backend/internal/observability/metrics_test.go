package observability

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsAndDBPoolCollector(t *testing.T) {
	m := NewMetrics()
	err := m.RegisterDBPoolCollector(func() []PoolStatsSnapshot {
		return []PoolStatsSnapshot{
			{
				PoolType: "platform",
				Database: "platform",
				Stats: sql.DBStats{
					OpenConnections: 5,
					InUse:           2,
					Idle:            3,
					WaitCount:       4,
					WaitDuration:    250 * time.Millisecond,
				},
			},
			{
				PoolType: "target",
				Database: "bioskop",
				Stats: sql.DBStats{
					OpenConnections: 3,
					InUse:           1,
					Idle:            2,
					WaitCount:       1,
					WaitDuration:    50 * time.Millisecond,
				},
			},
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	m.SyncJobsByStatus([]JobStatusCount{
		{Database: "bioskop", Status: "pending", Count: 2},
		{Database: "bioskop", Status: "running", Count: 1},
	})
	m.IncJobCompleted("bioskop", "success", "batch", "execute")
	m.IncJobRetry("bioskop", "batch", "execute")
	m.ObserveQueueWait("bioskop", "batch", 120*time.Millisecond)
	m.ObserveLockWait("bioskop", 15*time.Millisecond)
	m.ObserveExecutionDuration("bioskop", "batch", "execute", "success", 2*time.Second)
	m.AddBatchesProcessed("bioskop", "up", 5)
	m.AddRowsProcessed("bioskop", "up", 5000)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	body := rec.Body.String()

	required := []string{
		`datadock_migration_jobs_by_status{database="bioskop",status="pending"} 2`,
		`datadock_migration_jobs_by_status{database="bioskop",status="running"} 1`,
		`datadock_migration_jobs_completed_total{action="execute",database="bioskop",mode="batch",status="success"} 1`,
		`datadock_migration_job_retries_total{action="execute",database="bioskop",mode="batch"} 1`,
		`datadock_migration_queue_wait_seconds_count{database="bioskop",mode="batch"} 1`,
		`datadock_migration_lock_wait_seconds_count{database="bioskop"} 1`,
		`datadock_migration_execution_duration_seconds_count{action="execute",database="bioskop",mode="batch",status="success"} 1`,
		`datadock_migration_batches_processed_total{database="bioskop",direction="up"} 5`,
		`datadock_migration_rows_processed_total{database="bioskop",direction="up"} 5000`,
		`datadock_db_pool_connections_in_use{database="platform",pool_type="platform"} 2`,
		`datadock_db_pool_connections_idle{database="platform",pool_type="platform"} 3`,
		`datadock_db_pool_wait_duration_seconds_total{database="platform",pool_type="platform"} 0.25`,
		`datadock_db_pool_connections_in_use{database="bioskop",pool_type="target"} 1`,
		`datadock_db_pool_connections_idle{database="bioskop",pool_type="target"} 2`,
	}
	for _, needle := range required {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected metric line %q in output:\n%s", needle, body)
		}
	}

	forbiddenLabels := []string{"user_id=", "actor=", "job_id=", "execution_id=", "sql="}
	for _, forbidden := range forbiddenLabels {
		if strings.Contains(body, forbidden) {
			t.Fatalf("forbidden high-cardinality label %q found in metrics output", forbidden)
		}
	}
}
