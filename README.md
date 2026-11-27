# DataDock

## Menjalankan aplikasi

Siapkan Docker dan Docker Compose. Jalankan dari folder utama proyek:

```sh
docker compose up -d --build
```

Buka http://localhost, lalu login dengan akun admin default:

- Username: `admin`
- Password: `admin-local-change-me`

Layanan yang berjalan melalui Compose:

| Layanan | Alamat lokal | Fungsi |
| --- | --- | --- |
| Frontend (SPA) | `http://localhost` | Antarmuka pengguna DataDock |
| Backend API | `http://localhost:8080` | REST API & Prometheus `/metrics` (`datadock-api`) |
| Migration Worker | `http://localhost:9091` | Multi-worker async migration, `/healthz`, & `/metrics` (`datadock-worker`) |
| Platform PostgreSQL | `127.0.0.1:5433` | Penyimpanan metadata, antrean job, dan lock |
| Example PostgreSQL | `127.0.0.1:5434` | Database contoh (`example_db`) |
| Prometheus | `http://localhost:9090` | Scraping metrik API & Worker setiap 5 detik |
| Grafana | `http://localhost:3001` | Dashboard ter-provision otomatis + korelasi Prometheus, Tempo, dan Loki |
| Loki / Tempo / OTel Collector | `3100` / `3200` / `4317, 4318` | Penyimpanan log terstruktur, distributed trace, dan receiver OTLP |

Untuk mencoba secara lokal, tidak perlu membuat `.env`. Jika ingin mengubah konfigurasi, salin `.env.example` menjadi `.env` sebelum menjalankan aplikasi.

## Menambahkan database contoh

Database contoh bioskop sudah disiapkan oleh Compose beserta datanya.
Untuk mengaksesnya, buka **Manage connections → New connection** dan isi:

- Connection name: `Bioskop`
- Database engine: `PostgreSQL`
- Maximum open connections: `5`
- Maximum idle connections: `2`

Isi DSN dengan:

```text
postgres://example_user:example_local_password@db_example:5432/example_db?sslmode=disable
```

DSN adalah string berisi alamat database, nama database, username, dan password.
Contoh di atas berlaku untuk konfigurasi Compose default.

Simpan, lalu buka **Databases** dan klik kartu Bioskop untuk melihat tabel dan data.
Koneksi yang sudah disimpan tetap tersedia setelah aplikasi dijalankan ulang.

## Asynchronous Migration, Batch Execution, dan Lock Recovery

### Arsitektur Antrean & Multi-Worker

- Endpoint eksekusi (`POST /api/v1/connections/{id}/migrations/{migrationID}/execute`, `/rollback`, `/forward`) dan pemulihan batch (`POST /api/v1/connections/{id}/jobs/{jobID}/resume`, `/revert`) menyimpan job secara persisten ke tabel `migration_jobs` di database platform dan segera mengembalikan `202 Accepted` berisi `job_id` dan `execution_id`.
- Binary terpisah `/app/worker` menjalankan `WorkerPool` dengan konkurensi sesuai `WORKER_CONCURRENCY` (default `2`).
- Worker mengklaim job secara aman menggunakan `SELECT ... FOR UPDATE SKIP LOCKED` sembari mengakuisisi lock per database di tabel `database_locks`:
  - Migrasi untuk **database target berbeda** berjalan **paralel** pada worker yang berbeda.
  - Migrasi untuk **database target yang sama** berjalan **sekuensial** (job lain menunggu hingga lock dilepas).
- Setelah lock diperoleh, worker melakukan *post-lock revalidation* (status migrasi, urutan versi, otorisasi, serta pemeriksaan `ApprovalChecker`).

### Mode Normal vs Mode Batch & Partial Failure Recovery

- **Mode Normal (`execution_mode = "normal"`)**: seluruh SQL dijalankan dalam satu transaksi atomik di database target; bila gagal seluruh perubahan di-rollback dan lock dilepas.
- **Mode Batch (`execution_mode = "batch"`)**: eksekusi dipecah per `batch_size` berdasarkan kolom cursor (`batch_by_attribute`). Setiap batch mengeksekusi DML dan menulis baris checkpoint ke `_platform_internal.migration_checkpoints` di **dalam transaksi database target yang sama** (atomik).
- Schema `_platform_internal` diisolasi dari Database Viewer (tidak tampil di daftar schema/tabel) dan ditolak oleh validator SQL untuk kueri read-only maupun migrasi pengguna.
- Bila batch ke-N gagal (misalnya pelanggaran constraint atau error SQL non-transient):
  - Batch 1..N-1 yang sudah ter-commit tetap terjaga dengan bukti checkpoint.
  - Status job berubah menjadi `partial` dan `database_locks.lock_state` berpindah menjadi `held_partial` (mencegah migrasi lain mengubah database yang sedang dalam kondisi parsial).
  - Pengguna berhak `View + Edit` dapat memilih melalui UI tab **Async jobs** atau API:
    - **Resume (`resume_batch`)**: melanjutkan dari checkpoint terakhir yang ter-commit.
    - **Revert (`revert_batch`)**: menjalankan `sql_down` dalam urutan terbalik untuk seluruh batch yang telah ter-commit. Jika revert gagal di tengah jalan, status menjadi `partially_reverted` (lock tetap `held_partial`) dan dapat diulang hingga tuntas.

### Mekanisme Lock Recovery saat Worker Crash

1. Selama job berjalan, goroutine heartbeat di worker memperpanjang `database_locks.lease_expires_at` setiap `LOCK_HEARTBEAT_SECONDS` (default 5 detik) dengan durasi lease `LOCK_LEASE_SECONDS` (default 15 detik).
2. Jika proses worker mengalami crash atau terputus tanpa sempat melepas lock, *sweeper* berkala (`RunRecoverySweep`) mendeteksi lock dengan `lock_state = 'active'` yang `lease_expires_at < NOW()` dan memindahkannya ke **`held_uncertain`** (lock tidak pernah dilepas secara membabi buta).
3. Proses rekonsiliasi otomatis untuk lock `held_uncertain`:
   - **Mode Normal**:
     - Jika catatan `migration_runs` belum dibuat atau `sql_dispatch_started = false`, dipastikan SQL belum dikirim ke database target → run ditutup `FAILED`, lock dilepas, dan job diantrekan ulang (atau ditandai `failed` jika melewati `max_retries`).
     - Jika `sql_dispatch_started = true`, worker memverifikasi hasil transaksi PostgreSQL via bukti transaksi (`VerifyTransaction`):
       - `committed` → run & job disinkronkan menjadi `SUCCEEDED`/`success` dan lock dilepas.
       - `aborted` → run ditutup `FAILED`, lock dilepas, dan job diantrekan ulang/gagal sesuai kuota retry.
       - Tidak dapat diverifikasi → lock tetap ditahan pada `held_uncertain` agar dapat direkonsiliasi secara aman oleh admin.
   - **Mode Batch**:
     - Worker membaca tabel `_platform_internal.migration_checkpoints` di database target untuk `execution_id` terkait.
     - Progres `last_committed_batch`, `last_committed_cursor`, dan `committed_rows` pada `migration_jobs` disinkronkan dengan checkpoint yang benar-benar ter-commit di database target.
     - Lock dilepas dan job diantrekan ulang (`pending`) sehingga worker berikutnya melanjutkan secara idempoten dari batch berikutnya tanpa eksekusi ganda.

## Pengujian Large Dataset (> 1 Detik) & Benchmark Batch Migration

Tersedia dua script di folder `scripts/`:

- `scripts/seed_large_dataset.sql`: membuat tabel `public.benchmark_records` berisi **500.000 baris** lengkap dengan kolom payload dan 2 indeks (`status`, `account_code`) agar operasi *single full `UPDATE`* tanpa batch memakan waktu > 1 detik.
- `scripts/benchmark_batch_migration.ps1`: menjalankan perbandingan *end-to-end* pada kondisi awal yang identik:
  1. Seeding 500.000 baris (`status = 'pending'`).
  2. Mengukur baseline **single full `UPDATE`** dalam satu transaksi tanpa batching.
  3. Mereset tabel ke kondisi awal yang persis sama (500.000 baris identik).
  4. Menjalankan **batch migration** (`batch_size = 25000`, 20 batch) dengan commit atomik per batch ke `_platform_internal.migration_checkpoints`, lalu mencetak progres per batch dan ringkasan metrik.

Contoh eksekusi terhadap container `db_example` (PowerShell):

```powershell
.\scripts\benchmark_batch_migration.ps1 `
  -ContainerName hello-world-db_example-1 `
  -Dsn "postgres://example_user:example_local_password@localhost:5432/example_db?sslmode=disable" `
  -RowCount 500000 `
  -BatchSize 25000
```

Atau langsung terhadap PostgreSQL lokal yang memiliki `psql`:

```powershell
.\scripts\benchmark_batch_migration.ps1 `
  -Dsn "postgres://example_user:example_local_password@127.0.0.1:5434/example_db?sslmode=disable" `
  -RowCount 500000 `
  -BatchSize 25000
```

Metrik yang dilaporkan oleh script mencakup:

- `rows_total` & `rows_migrated` (verifikasi 100% baris ter-update, mis. `500000`)
- `single_update_ms` (durasi transaksi tunggal > 1000 ms)
- `batch_size`, `batches_total`, dan `checkpoints_stored` (mis. `25000`, `20`, `20`)
- Progres per batch (`cursor`, `rows`, `cumulative`, `step_ms`) dan `batch_total_ms`

Untuk menjalankan integration test PostgreSQL *end-to-end* (termasuk normal mode, batch partial failure, `revert_batch`, `resume_batch`, paralel 2 DB, dan RBAC View Only):

```powershell
$env:TEST_DATABASE_DSN = "postgres://example_user:example_local_password@127.0.0.1:5434/example_db?sslmode=disable"
cd backend
go test -v ./internal/services/migration -run TestAsyncMigrationEndToEndPostgres
```

## Activity Logging (JSON + Redaction) & OpenTelemetry Tracing

Konfigurasi telemetry melalui environment variable:

- `OTEL_EXPORTER`: `otlp` (default di Compose), `console` (cetak span ke stdout untuk inspeksi lokal), atau `none`.
- `OTEL_EXPORTER_OTLP_ENDPOINT`: alamat gRPC OpenTelemetry Collector (default `otel-collector:4317`).
- `OTEL_SERVICE_NAME`: `datadock-api` untuk server HTTP dan `datadock-worker` untuk worker.

Seluruh log dipancarkan dalam format JSON melalui `log/slog` (fan-out ke `stdout` dan `otelslog` OTLP) dengan penyaringan data sensitif terpusat (`"[REDACTED]"` untuk password, token, cookie, header Authorization, DSN dengan kredensial, dan literal rahasia di SQL).

### Contoh Output Structured JSON Log

```json
{
  "time": "2026-11-27T10:15:22.418Z",
  "level": "INFO",
  "msg": "migration.batch_step_committed",
  "event": "migration.batch_step_committed",
  "actor": "user-admin-1",
  "database": "bioskop-db",
  "status": "SUCCEEDED",
  "migration_id": 12,
  "job_id": "a7f2c1b0-4d3e-4c9a-9e81-2201f9a0d112",
  "execution_id": "5c1b8a2e-7b9d-4c8f-8a31-912c77d20e44",
  "attempt": 1,
  "batch": 3,
  "operation": "APPLY",
  "duration_ms": 142,
  "rows_affected": 25000,
  "query": "WITH batch AS (SELECT id FROM benchmark_records WHERE id > $1 AND id <= $2 ORDER BY id LIMIT $3) UPDATE benchmark_records SET status = 'migrated' WHERE id IN (SELECT id FROM batch);",
  "dsn": "[REDACTED]",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7"
}
```

### Contoh Output OpenTelemetry Trace & Span Hierarchy

W3C `TraceContext` diserialisasi ke kolom `migration_jobs.trace_context` saat API meng-enqueue job dan diekstrak ulang oleh worker sehingga satu `trace_id` menyambungkan seluruh siklus hidup end-to-end:

```json
{
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "spans": [
    {
      "service": "datadock-api",
      "name": "POST /api/v1/connections/{id}/migrations/{migrationID}/execute",
      "span_id": "b7ad6b7169203331",
      "attributes": { "http.method": "POST", "http.status_code": 202 }
    },
    {
      "service": "datadock-api",
      "name": "migration.enqueue",
      "parent_span_id": "b7ad6b7169203331",
      "span_id": "9f12c4e8a1b23045",
      "attributes": { "migration.database_id": "bioskop-db", "migration.mode": "batch", "migration.action": "execute" }
    },
    {
      "service": "datadock-worker",
      "name": "worker.process_job",
      "parent_span_id": "9f12c4e8a1b23045",
      "span_id": "00f067aa0ba902b7",
      "attributes": { "migration.job_id": "a7f2c1b0-4d3e-4c9a-9e81-2201f9a0d112", "worker.id": "worker-1" }
    },
    {
      "service": "datadock-worker",
      "name": "batch.execute_step",
      "parent_span_id": "00f067aa0ba902b7",
      "span_id": "1c8a92d34e5f6071",
      "attributes": { "migration.batch_number": 3, "migration.rows_affected": 25000 }
    }
  ]
}
```

## Bonus Observability: Prometheus, Grafana, & 3-Pillar Correlation

### Metrik Prometheus (`/metrics`)

Kedua service (`http://localhost:8080/metrics` dan `http://localhost:9091/metrics`) mengekspos metrik ber-kardinalitas rendah (tanpa label `job_id`, `user_id`, atau SQL):

- **Migration Metrics**:
  - `datadock_migration_jobs_by_status{database, status}`
  - `datadock_migration_jobs_completed_total{database, status, mode, action}`
  - `datadock_migration_job_retries_total{database, mode, action}`
  - `datadock_migration_queue_wait_seconds{database, mode}`
  - `datadock_migration_lock_wait_seconds{database}`
  - `datadock_migration_execution_duration_seconds{database, mode, action, status}`
  - `datadock_migration_batches_processed_total{database, direction}`
  - `datadock_migration_rows_processed_total{database, direction}`
- **Database Connection Pool Metrics** (`db.Stats()`):
  - `datadock_db_pool_open_connections{pool_type, database}`
  - `datadock_db_pool_connections_in_use{pool_type, database}`
  - `datadock_db_pool_connections_idle{pool_type, database}`
  - `datadock_db_pool_wait_count_total{pool_type, database}`
  - `datadock_db_pool_wait_duration_seconds_total{pool_type, database}`

### Panduan Korelasi 3 Pilar di Grafana

1. Buka Grafana di `http://localhost:3001` (login otomatis lokal atau `admin` / `admin`).
2. **Pilar 1 — Metrics (Prometheus)**: Buka folder **Dashboards → DataDock → DataDock Async Migrations & DB Pool Observability**. Pantau status job, latensi antrean p50/p95, durasi lock & eksekusi, throughput batch/rows, dan penggunaan connection pool.
3. **Pilar 2 — Logs (Loki → Tempo)**: Pada panel **Recent Structured Activity Logs** di bagian bawah dashboard (atau menu **Explore → Loki** dengan query `{service_name=~"datadock-api|datadock-worker"}`), buka detail baris log dan klik tombol **TraceID** (*derived field*) untuk langsung melompat ke trace terkait di Tempo.
4. **Pilar 3 — Traces (Tempo → Loki)**: Di menu **Explore → Tempo**, pilih salah satu trace migrasi untuk melihat *waterfall span* lintas `datadock-api` dan `datadock-worker`, lalu klik **Logs for this span** (`tracesToLogsV2`) untuk memfilter seluruh log terstruktur di Loki dengan `trace_id` yang sama.

## Stress Testing dengan k6

Pengujian beban dan stres (*stress testing*) dilakukan menggunakan **Grafana k6** (`grafana/k6:0.56.0`) secara paralel terhadap jalur Read API dan jalur eksekusi asynchronous migration worker pada lingkungan Docker Compose penuh (`app`, `worker`, `db`, `db_example`, `prometheus`, `grafana`, `loki`, `tempo`, `otel-collector`).

### Arsitektur Skrip & Isolasi Data Uji

| File | Peran |
| --- | --- |
| `k6/sql/init_target_db.sql` | Menyiapkan tabel `public.k6_stress_target` (10.000 baris + indeks `bucket` & `counter`) di tiap DB uji agar 1 transaksi `UPDATE` berdurasi ~130–200 ms. |
| `k6/seed.ps1` / `k6/seed.sh` | Membuat 3 logical PostgreSQL DB terisolasi (`k6_stress_db_1..3` → koneksi `k6-target-1..3`, pool `maxOpen=10, maxIdle=4`), membuat akun `k6_runner` + leader grup `k6-stress-group` (`write`), serta menyiapkan 450 request `APPLY` berstatus `APPROVED` (60% `k6-target-1` sebagai `same_db`, 20% `k6-target-2` & 20% `k6-target-3` sebagai `diff_db`, masing-masing dengan `sql_up` dan `sql_down` valid). |
| `k6/stress-test.js` | Menjalankan 2 skenario paralel (`read_api_traffic` & `migration_jobs_traffic`), login 1x per VU (session terpisah di `managed_sessions`, header `Origin` & `Sec-Fetch-Site: same-origin`), indeks request deterministik via `exec.scenario.iterationInTest` (0 duplikasi), polling ketat hingga status terminal, tag `stage` & `db_group`, dan ekspor otomatis `k6/results/summary.json` serta `k6/results/summary.txt`. |
| `k6/cleanup.ps1` / `k6/cleanup.sh` | Menghapus koneksi uji `k6-target-1..3`, membersihkan seluruh entitas uji pada platform DB (`k6_runner`, `k6-stress-group`, migrations/requests/jobs/locks terkait), men-drop `k6_stress_db_1..3`, dan menghapus fixture lokal tanpa menyentuh `example_db` (Bioskop). |

### Cara Menjalankan Stress Test

1. Pastikan seluruh stack Compose berjalan:
   ```sh
   docker compose up -d --build
   ```
2. Jalankan seeder (Windows PowerShell atau Bash):
   ```powershell
   .\k6\seed.ps1
   ```
   ```sh
   ./k6/seed.sh
   ```
3. Jalankan k6 menggunakan profile `stress`:
   ```sh
   docker compose --profile stress run --rm k6
   ```
4. Verifikasi hasil pada `k6/results/summary.txt`, `k6/results/summary.json`, dan Grafana (`http://localhost:3001`), lalu bersihkan data uji:
   ```powershell
   .\k6\cleanup.ps1
   ```
   ```sh
   ./k6/cleanup.sh
   ```

### Baseline, Tahapan Beban (*Stages*), & *Thresholds*

- **Konfigurasi lingkungan baseline**: `worker` menjalankan `WORKER_CONCURRENCY=2` (`PollInterval=500ms`, `LockLeaseDuration=15s`, `LockHeartbeatInterval=5s`, `MaxRetries=3`); platform DB (`storeDB`) memiliki `MaxOpenConns=10` & `MaxIdleConns=4` pada `app` maupun `worker`; masing-masing target DB menggunakan `maxOpen=10, maxIdle=4, maxLifetimeSeconds=300, maxIdleTimeSeconds=60`.
- **Kalibrasi 1 job**: 1 transaksi `UPDATE` pada 10.000 baris `public.k6_stress_target` terukur ~165 ms (p50) – 276 ms (p95) di dalam worker (`277 ms` termasuk overhead `docker exec psql`).
- **4 Tahap sinkron (total 3m 40s / 220s)**:
  - `normal` (`0s–50s`): 8 VU Read API + 2 VU Migration Jobs
  - `mid` (`50s–100s`): 24 VU Read API + 4 VU Migration Jobs
  - `peak` (`100s–160s`): 64 VU Read API + 8 VU Migration Jobs
  - `recovery` (`160s–210s`): turun ke 8 VU Read API + 2 VU Migration Jobs, diikuti ramp-down `210s–220s` ke 0 VU.
- **Thresholds kelulusan**:
  - `http_req_duration{scenario:read_api}`: `p(95) < 500 ms` (**LULUS** — aktual `53,70 ms`)
  - `http_req_failed`: `rate < 5%` (**LULUS** — aktual `0,00%`)
  - `job_failed_rate`: `rate < 5%` (**LULUS** — aktual `0,00%`)

### Hasil Aktual per Tahap Beban

- **Rentang waktu pengujian (UTC)**: `2026-11-27T16:31:58.265Z` → `2026-11-27T16:35:38.907Z` (`220.641 ms`)
- **Ringkasan keseluruhan** (`k6/results/summary.json` & `k6/results/summary.txt`):
  - Total HTTP request: **22.396 req** (**101,50 RPS**), HTTP error rate **0,00%**, Read API p50 **11,55 ms**, p95 **53,70 ms**, max **907,08 ms**.
  - Total Migration Jobs: **450 / 450 sukses** (**0,00%** gagal), Queue Wait `same_db` p95 **558,10 ms** vs `diff_db` p95 **512,75 ms**, Exec p95 **276,00 ms**, E2E p95 **770,20 ms**.

| Tahap | Beban VU (Read + Job) | Read RPS | Read p50 (ms) | Read p95 (ms) | Read Err% | Jobs Selesai | Throughput (Jobs/mnt) | Job Err% | Queue Wait `same_db` p95 (ms) | Queue Wait `diff_db` p95 (ms) | Exec p95 (ms) | E2E p95 (ms) |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `normal` | 8 + 2 | 27,20 | 9,43 | 16,63 | 0,00% | 29 | 34,80 | 0,00% | 458,65 | 336,00 | 194,20 | 563,40 |
| `mid` | 24 + 4 | 85,80 | 8,78 | 17,19 | 0,00% | 88 | 105,60 | 0,00% | 549,20 | 490,60 | 223,30 | 700,95 |
| `peak` | 64 + 8 | 208,70 | 20,13 | 66,47 | 0,00% | 333 | 333,00 | 0,00% | 562,10 | 575,50 | 323,60 | 801,60 |
| `recovery` | 8 + 2 | 55,38 | 10,01 | 36,52 | 0,00% | 0* | 0,00* | 0,00% | — | — | — | — |

*\*Seluruh 450 request uji yang disiapkan habis diproses tuntas pada akhir fase `peak` (`29 + 88 + 333 = 450`) dengan 0 job gagal dan 0 antrean tersisa saat memasuki fase `recovery`.*

### Korelasi Metrik Prometheus & Grafana

Snapshot time-series Prometheus pada rentang UTC uji tersimpan di `k6/results/prometheus-snapshot.json` (step `5s`, kedua target `datadock-api` & `datadock-worker` berstatus `UP`):

| Metrik Prometheus | Layanan / Label Utama | Nilai Min | Nilai Maks (Peak) | Nilai Akhir (Recovery) | Korelasi dengan Hasil k6 |
| --- | --- | --- | --- | --- | --- |
| `datadock_migration_jobs_completed_total` | `worker` (`k6-target-1` / `-2` / `-3`) | 0 | `270` / `90` / `90` | **450 total** (`success`) | Cocok tepat 100% dengan 450 job selesai di k6 (rasio 60% / 20% / 20%). |
| `datadock_migration_jobs_by_status` | `worker` (`k6-target-1`, `pending` / `running`) | 1 / 1 | `pending=3`, `running=1` | `0` pending, `0` running | Membuktikan serialisasi ketat (`running <= 1` per DB) dan kedalaman antrean lebih tinggi di `same_db` (`max 3`) dibanding `diff_db` (`max 2`). |
| `datadock_migration_queue_wait_seconds` (p95) | `worker` (`k6-target-1` vs `-2` / `-3`) | `0,425 s` | `0,890 s` (`same_db`) vs `0,717–0,870 s` (`diff_db`) | Turun seiring antrean kosong | Selaras dengan k6 queue wait p95 (`same_db` `558,10 ms` > `diff_db` `512,75 ms`) ditambah lantai interval polling worker `500 ms`. |
| `datadock_migration_lock_wait_seconds` (p95) | `worker` (3 target DB) | `0,0048 s` | `0,0048 s` | `0,0048 s` | Akuisisi lock non-blocking (`FOR UPDATE SKIP LOCKED` + `ON CONFLICT DO NOTHING`) tetap sangat cepat (<5 ms). |
| `datadock_migration_execution_duration_seconds` (p95) | `worker` (3 target DB) | `0,243 s` | `0,313–0,456 s` | `0,243 s` | Selaras dengan kenaikan k6 `exec_p95` dari `194,20 ms` (`normal`) ke `323,60 ms` (`peak`) akibat kompetisi I/O PostgreSQL pada container `db_example`. |
| `datadock_db_pool_connections_in_use` | `app` (`platform`) vs `worker` (`platform`) | `0` / `0` | **`10 / 10`** (`app`) vs `2 / 10` (`worker`) | `0` / `0` | **Bottleneck utama fase `peak`**: pool platform DB pada `app` mencapai batas `MaxOpenConns=10` saat 64 VU Read API aktif. |
| `datadock_db_pool_connections_idle` | `app` (`platform`) | **`0`** | `4` | `4` | Idle pool `app` sempat habis (`0`) saat `peak` lalu pulih utuh ke `4` pada `recovery`. |
| `rate(datadock_db_pool_wait_duration_seconds_total[1m])` | `app` (`platform`) vs target DBs | `0,00 s/s` | **`4,4937 s/s`** (`app` platform), `0,00 s/s` (target DB) | **`0,0000 s/s`** | Menjelaskan lonjakan Read API p95 (`16,63 ms` → `66,47 ms`, max `907,08 ms`) berasal dari antrean koneksi `storeDB` di `app`, bukan target DB. |

### Analisis Teknis per Tahap & Bukti Pemulihan

1. **Tahap `normal` (8 + 2 VU)**: Sistem berjalan sangat ringan dengan Read API p95 `16,63 ms` (27,20 RPS) dan 0% error. Worker memproses 29 job (`34,80 jobs/mnt`) dengan durasi eksekusi p95 `194,20 ms`. Antrean `same_db` (`458,65 ms`) sudah mulai terlihat lebih tinggi dibanding `diff_db` (`336,00 ms`) karena serialisasi lock pada `k6-target-1`.
2. **Tahap `mid` (24 + 4 VU)**: Throughput Read API naik 3,15× menjadi `85,80 RPS` sementara p95 nyaris datar (`17,19 ms`), menandakan kapasitas CPU dan pool `storeDB` belum tersaturasi. Throughput migrasi naik 3× menjadi `105,60 jobs/mnt` (88 job), dengan selisih antrean `same_db` (`549,20 ms`) vs `diff_db` (`490,60 ms`) tetap terjaga di bawah 600 ms.
3. **Tahap `peak` (64 + 8 VU)**: Throughput Read API melonjak ke `208,70 RPS` (12.522 request) bersamaan dengan 333 job migrasi (`333 jobs/mnt`, mendekati batas teoretis 2 worker goroutine × ~3 job/s). Di titik ini Prometheus memperlihatkan bottleneck nyata: `datadock_db_pool_connections_in_use{service="datadock-api",database="platform"}` menyentuh **`10/10`**, `idle` turun ke `0`, dan `wait_duration` naik hingga **`4,49 s/s`**, mendorong Read API p95 ke `66,47 ms` (masih jauh di bawah ambang batas `500 ms`) dan max `907,08 ms`, sementara `exec_p95` naik ke `323,60 ms` akibat kompetisi disk I/O antar 3 database uji di satu instance `db_example`. Meski berada di bawah tekanan puncak, tidak terjadi satu pun timeout maupun kegagalan (`0,00%` error HTTP & job).
4. **Tahap `recovery` (8 + 2 VU) & Verifikasi Pasca-Tes**:
   - Begitu beban diturunkan kembali, Read API p50 langsung pulih dari `20,13 ms` → **`10,01 ms`** dan p95 turun dari `66,47 ms` → **`36,52 ms`** (`0,00%` error).
   - Pool `storeDB` pada `app` kembali ke `in_use = 0`, `idle = 4`, dan `wait_duration_rate = 0,0000 s/s`.
   - Pemeriksaan langsung pada database platform pasca-tes mengonfirmasi **`SELECT COUNT(*) FROM database_locks` = `0`** (tidak ada lock tersangkut) dan **`migration_jobs`: 450 `success`, 0 `failed`/`partial`/`uncertain`**.
5. **Temuan Tambahan & Rekomendasi Tuning**:
   - Selama eksekusi stress test juga terdeteksi dan diperbaiki bug agregasi label duplikat pada `dbPoolCollector` (`backend/internal/observability/metrics.go`) yang sebelumnya memicu HTTP 500 pada `worker:9091/metrics` ketika koneksi dengan nama sama dihapus dan dibuat ulang.
   - Untuk beban baca >60 VU di produksi, disarankan menaikkan `storeDB.SetMaxOpenConns` pada `app` (mis. dari `10` ke `25–30`) guna menghilangkan antrean pool platform DB, serta menaikkan `WORKER_CONCURRENCY` jika jumlah database target paralel bertambah.

## Setelah mengubah kode

Bangun ulang backend, worker, dan frontend:

```sh
docker compose up -d --build app worker frontend
```

## Menghentikan aplikasi

```sh
docker compose down
```

Perintah ini tidak menghapus data database dan observability yang tersimpan di volume Docker.
