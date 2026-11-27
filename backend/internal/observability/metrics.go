package observability

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type JobStatusCount struct {
	Database string
	Status   string
	Count    float64
}

type PoolStatsSnapshot struct {
	PoolType string
	Database string
	Stats    sql.DBStats
}

type PoolStatsProvider func() []PoolStatsSnapshot

type Metrics struct {
	registry          *prometheus.Registry
	jobsByStatus      *prometheus.GaugeVec
	jobsCompleted     *prometheus.CounterVec
	jobRetries        *prometheus.CounterVec
	queueWaitSeconds  *prometheus.HistogramVec
	lockWaitSeconds   *prometheus.HistogramVec
	executionSeconds  *prometheus.HistogramVec
	batchesProcessed  *prometheus.CounterVec
	rowsProcessed     *prometheus.CounterVec
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	jobsByStatus := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "datadock",
		Subsystem: "migration",
		Name:      "jobs_by_status",
		Help:      "Current number of migration jobs grouped by database and status.",
	}, []string{"database", "status"})

	jobsCompleted := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "datadock",
		Subsystem: "migration",
		Name:      "jobs_completed_total",
		Help:      "Total completed migration jobs by database, status, mode, and action.",
	}, []string{"database", "status", "mode", "action"})

	jobRetries := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "datadock",
		Subsystem: "migration",
		Name:      "job_retries_total",
		Help:      "Total migration job retries by database, mode, and action.",
	}, []string{"database", "mode", "action"})

	queueWaitSeconds := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "datadock",
		Subsystem: "migration",
		Name:      "queue_wait_seconds",
		Help:      "Time spent waiting in the migration job queue before worker claim.",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"database", "mode"})

	lockWaitSeconds := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "datadock",
		Subsystem: "migration",
		Name:      "lock_wait_seconds",
		Help:      "Time spent waiting to acquire a database migration lock.",
		Buckets:   []float64{0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"database"})

	executionSeconds := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "datadock",
		Subsystem: "migration",
		Name:      "execution_duration_seconds",
		Help:      "Duration of migration job executions.",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"database", "mode", "action", "status"})

	batchesProcessed := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "datadock",
		Subsystem: "migration",
		Name:      "batches_processed_total",
		Help:      "Total committed migration batches by database and direction.",
	}, []string{"database", "direction"})

	rowsProcessed := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "datadock",
		Subsystem: "migration",
		Name:      "rows_processed_total",
		Help:      "Total committed migration rows by database and direction.",
	}, []string{"database", "direction"})

	reg.MustRegister(
		jobsByStatus,
		jobsCompleted,
		jobRetries,
		queueWaitSeconds,
		lockWaitSeconds,
		executionSeconds,
		batchesProcessed,
		rowsProcessed,
	)

	return &Metrics{
		registry:         reg,
		jobsByStatus:     jobsByStatus,
		jobsCompleted:    jobsCompleted,
		jobRetries:       jobRetries,
		queueWaitSeconds: queueWaitSeconds,
		lockWaitSeconds:  lockWaitSeconds,
		executionSeconds: executionSeconds,
		batchesProcessed: batchesProcessed,
		rowsProcessed:    rowsProcessed,
	}
}

func (m *Metrics) RegisterDBPoolCollector(provider PoolStatsProvider) error {
	if m == nil || provider == nil {
		return nil
	}
	return m.registry.Register(newDBPoolCollector(provider))
}

func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) SetJobsByStatus(database, status string, count float64) {
	if m == nil {
		return
	}
	m.jobsByStatus.WithLabelValues(normalizeLabel(database), normalizeLabel(status)).Set(count)
}

func (m *Metrics) SyncJobsByStatus(counts []JobStatusCount) {
	if m == nil {
		return
	}
	m.jobsByStatus.Reset()
	for _, item := range counts {
		m.jobsByStatus.WithLabelValues(normalizeLabel(item.Database), normalizeLabel(item.Status)).Set(item.Count)
	}
}

func (m *Metrics) IncJobCompleted(database, status, mode, action string) {
	if m == nil {
		return
	}
	m.jobsCompleted.WithLabelValues(
		normalizeLabel(database),
		normalizeLabel(status),
		normalizeLabel(mode),
		normalizeLabel(action),
	).Inc()
}

func (m *Metrics) IncJobRetry(database, mode, action string) {
	if m == nil {
		return
	}
	m.jobRetries.WithLabelValues(
		normalizeLabel(database),
		normalizeLabel(mode),
		normalizeLabel(action),
	).Inc()
}

func (m *Metrics) ObserveQueueWait(database, mode string, d time.Duration) {
	if m == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	m.queueWaitSeconds.WithLabelValues(normalizeLabel(database), normalizeLabel(mode)).Observe(d.Seconds())
}

func (m *Metrics) ObserveLockWait(database string, d time.Duration) {
	if m == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	m.lockWaitSeconds.WithLabelValues(normalizeLabel(database)).Observe(d.Seconds())
}

func (m *Metrics) ObserveExecutionDuration(database, mode, action, status string, d time.Duration) {
	if m == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	m.executionSeconds.WithLabelValues(
		normalizeLabel(database),
		normalizeLabel(mode),
		normalizeLabel(action),
		normalizeLabel(status),
	).Observe(d.Seconds())
}

func (m *Metrics) AddBatchesProcessed(database, direction string, count float64) {
	if m == nil || count <= 0 {
		return
	}
	m.batchesProcessed.WithLabelValues(normalizeLabel(database), normalizeLabel(direction)).Add(count)
}

func (m *Metrics) AddRowsProcessed(database, direction string, count float64) {
	if m == nil || count <= 0 {
		return
	}
	m.rowsProcessed.WithLabelValues(normalizeLabel(database), normalizeLabel(direction)).Add(count)
}

func normalizeLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}

type dbPoolCollector struct {
	provider      PoolStatsProvider
	openDesc      *prometheus.Desc
	inUseDesc     *prometheus.Desc
	idleDesc      *prometheus.Desc
	waitCountDesc *prometheus.Desc
	waitDurDesc   *prometheus.Desc
}

func newDBPoolCollector(provider PoolStatsProvider) *dbPoolCollector {
	labels := []string{"pool_type", "database"}
	return &dbPoolCollector{
		provider: provider,
		openDesc: prometheus.NewDesc(
			"datadock_db_pool_open_connections",
			"Number of established connections both in use and idle.",
			labels, nil,
		),
		inUseDesc: prometheus.NewDesc(
			"datadock_db_pool_connections_in_use",
			"Number of connections currently in use.",
			labels, nil,
		),
		idleDesc: prometheus.NewDesc(
			"datadock_db_pool_connections_idle",
			"Number of idle connections in the pool.",
			labels, nil,
		),
		waitCountDesc: prometheus.NewDesc(
			"datadock_db_pool_wait_count_total",
			"Total number of connections waited for.",
			labels, nil,
		),
		waitDurDesc: prometheus.NewDesc(
			"datadock_db_pool_wait_duration_seconds_total",
			"Total time blocked waiting for a new connection in seconds.",
			labels, nil,
		),
	}
}

func (c *dbPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.openDesc
	ch <- c.inUseDesc
	ch <- c.idleDesc
	ch <- c.waitCountDesc
	ch <- c.waitDurDesc
}

func (c *dbPoolCollector) Collect(ch chan<- prometheus.Metric) {
	for _, snap := range c.provider() {
		poolType := normalizeLabel(snap.PoolType)
		database := normalizeLabel(snap.Database)
		ch <- prometheus.MustNewConstMetric(c.openDesc, prometheus.GaugeValue, float64(snap.Stats.OpenConnections), poolType, database)
		ch <- prometheus.MustNewConstMetric(c.inUseDesc, prometheus.GaugeValue, float64(snap.Stats.InUse), poolType, database)
		ch <- prometheus.MustNewConstMetric(c.idleDesc, prometheus.GaugeValue, float64(snap.Stats.Idle), poolType, database)
		ch <- prometheus.MustNewConstMetric(c.waitCountDesc, prometheus.CounterValue, float64(snap.Stats.WaitCount), poolType, database)
		ch <- prometheus.MustNewConstMetric(c.waitDurDesc, prometheus.CounterValue, snap.Stats.WaitDuration.Seconds(), poolType, database)
	}
}
