param(
    [string]$Dsn = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
    [int]$RowCount = 500000,
    [int]$BatchSize = 25000,
    [string]$ContainerName = ""
)

$ErrorActionPreference = "Stop"

function Invoke-Psql {
    param([string]$Sql)
    if ($ContainerName -ne "") {
        $result = $Sql | docker exec -i $ContainerName psql $Dsn -v ON_ERROR_STOP=1 -At
    } else {
        $result = $Sql | psql $Dsn -v ON_ERROR_STOP=1 -At
    }
    if ($LASTEXITCODE -ne 0) {
        throw "psql command failed with exit code $LASTEXITCODE"
    }
    return $result
}

function Reset-Dataset {
    param([int]$Count)
    $seedSql = @"
DROP TABLE IF EXISTS public.benchmark_records;
CREATE TABLE public.benchmark_records (
    id BIGINT PRIMARY KEY,
    account_code TEXT NOT NULL,
    status TEXT NOT NULL,
    payload TEXT NOT NULL,
    checksum TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO public.benchmark_records (id, account_code, status, payload, checksum, updated_at)
SELECT
    g,
    'ACCT-' || lpad((g % 10000)::text, 5, '0'),
    'pending',
    repeat(md5(g::text), 6),
    '',
    NOW()
FROM generate_series(1, $Count) AS g;
CREATE INDEX idx_benchmark_records_status ON public.benchmark_records (status);
CREATE INDEX idx_benchmark_records_account ON public.benchmark_records (account_code);
ANALYZE public.benchmark_records;
"@
    $null = Invoke-Psql -Sql $seedSql
}

Write-Host "=== Seeding $RowCount rows for single full UPDATE baseline ==="
Reset-Dataset -Count $RowCount

$singleSw = [System.Diagnostics.Stopwatch]::StartNew()
$singleSql = @"
BEGIN;
UPDATE public.benchmark_records
SET status = 'migrated',
    checksum = md5(payload || id::text),
    updated_at = NOW();
COMMIT;
"@
$null = Invoke-Psql -Sql $singleSql
$singleSw.Stop()
$singleMs = $singleSw.ElapsedMilliseconds
Write-Host "Single full UPDATE completed: rows=$RowCount duration_ms=$singleMs"

Write-Host ""
Write-Host "=== Resetting identical $RowCount rows for batch migration (batch_size=$BatchSize) ==="
Reset-Dataset -Count $RowCount

$execId = [guid]::NewGuid().ToString()
$checkpointInitSql = @"
CREATE SCHEMA IF NOT EXISTS _platform_internal;
CREATE TABLE IF NOT EXISTS _platform_internal.migration_checkpoints (
    execution_id UUID NOT NULL,
    batch_number INT NOT NULL,
    direction TEXT NOT NULL DEFAULT 'up',
    cursor_start TEXT NOT NULL,
    cursor_end TEXT NOT NULL,
    rows_affected BIGINT NOT NULL,
    recovery_metadata JSONB,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (execution_id, direction, batch_number)
);
"@
$null = Invoke-Psql -Sql $checkpointInitSql

$scopeMax = [int64](Invoke-Psql -Sql "SELECT COALESCE(MAX(id), 0) FROM public.benchmark_records;" | Select-Object -Last 1)
$lastCursor = [int64]0
$batchNum = 0
$totalCommittedRows = [int64]0

$batchSw = [System.Diagnostics.Stopwatch]::StartNew()
while ($lastCursor -lt $scopeMax) {
    $batchNum++
    $nextStart = $lastCursor + 1
    $nextEnd = [Math]::Min($lastCursor + $BatchSize, $scopeMax)
    $stepSw = [System.Diagnostics.Stopwatch]::StartNew()
    $stepSql = @"
BEGIN;
WITH batch AS (
    SELECT id FROM public.benchmark_records
    WHERE id > $lastCursor AND id <= $scopeMax
    ORDER BY id
    LIMIT $BatchSize
),
updated AS (
    UPDATE public.benchmark_records
    SET status = 'migrated',
        checksum = md5(payload || id::text),
        updated_at = NOW()
    WHERE id IN (SELECT id FROM batch)
    RETURNING id
)
INSERT INTO _platform_internal.migration_checkpoints
    (execution_id, batch_number, direction, cursor_start, cursor_end, rows_affected)
SELECT
    '$execId'::uuid,
    $batchNum,
    'up',
    COALESCE(MIN(id)::text, '$nextStart'),
    COALESCE(MAX(id)::text, '$nextEnd'),
    COUNT(*)
FROM updated
RETURNING rows_affected, cursor_end;
COMMIT;
"@
    $outLine = (Invoke-Psql -Sql $stepSql | Where-Object { $_ -match '\|' } | Select-Object -Last 1)
    $stepSw.Stop()
    $parts = $outLine -split '\|'
    $rowsAffected = [int64]$parts[0]
    $cursorEnd = [int64]$parts[1]
    $totalCommittedRows += $rowsAffected
    $lastCursor = $cursorEnd
    Write-Host ("Batch {0}: cursor={1}..{2} rows={3} cumulative={4} step_ms={5}" -f $batchNum, $nextStart, $cursorEnd, $rowsAffected, $totalCommittedRows, $stepSw.ElapsedMilliseconds)
}
$batchSw.Stop()

$checkpointCount = (Invoke-Psql -Sql "SELECT COUNT(*) FROM _platform_internal.migration_checkpoints WHERE execution_id = '$execId'::uuid AND direction = 'up';" | Select-Object -Last 1)
$migratedCount = (Invoke-Psql -Sql "SELECT COUNT(*) FROM public.benchmark_records WHERE status = 'migrated';" | Select-Object -Last 1)

Write-Host ""
Write-Host "=== Benchmark Summary ==="
Write-Host "execution_id       : $execId"
Write-Host "rows_total         : $RowCount"
Write-Host "rows_migrated      : $migratedCount"
Write-Host "single_update_ms   : $singleMs"
Write-Host "batch_size         : $BatchSize"
Write-Host "batches_total      : $batchNum"
Write-Host "checkpoints_stored : $checkpointCount"
Write-Host "batch_total_ms     : $($batchSw.ElapsedMilliseconds)"
