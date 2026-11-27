param(
    [string]$BaseUrl = "http://127.0.0.1:8080",
    [string]$PublicOrigin = "http://localhost",
    [string]$AdminUsername = "admin",
    [string]$AdminPassword = "admin-local-change-me",
    [string]$RunnerUsername = "k6_runner",
    [string]$GroupName = "k6-stress-group",
    [string]$PlatformDbContainer = "",
    [string]$PlatformDbUser = "hello_world",
    [string]$PlatformDbName = "hello_world",
    [string]$ExampleDbContainer = "",
    [string]$ExampleDbUser = "example_user",
    [string]$FixturePath = "k6/fixtures/stress-fixtures.json"
)

$ErrorActionPreference = "Stop"

if ($PlatformDbContainer -eq "") {
    $PlatformDbContainer = (docker compose ps -q db | Select-Object -First 1).Trim()
}
if ($ExampleDbContainer -eq "") {
    $ExampleDbContainer = (docker compose ps -q db_example | Select-Object -First 1).Trim()
}

$targetNames = @("k6-target-1", "k6-target-2", "k6-target-3")
$targetDbs = @("k6_stress_db_1", "k6_stress_db_2", "k6_stress_db_3")

try {
    $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    $headers = @{
        "Origin"         = $PublicOrigin
        "Sec-Fetch-Site" = "same-origin"
    }
    $body = "username=$([uri]::EscapeDataString($AdminUsername))&password=$([uri]::EscapeDataString($AdminPassword))"
    $null = Invoke-WebRequest -Uri "$BaseUrl/auth/login" -Method POST -Headers $headers -ContentType "application/x-www-form-urlencoded" -Body $body -WebSession $session -UseBasicParsing
    $connsResp = Invoke-WebRequest -Uri "$BaseUrl/api/v1/admin/connections" -Method GET -WebSession $session -UseBasicParsing
    $conns = $connsResp.Content | ConvertFrom-Json
    foreach ($conn in $conns) {
        if ($targetNames -contains $conn.name) {
            $null = Invoke-WebRequest -Uri "$BaseUrl/api/v1/connections/$($conn.id)" -Method DELETE -Headers $headers -WebSession $session -UseBasicParsing
            Write-Host "Deleted managed connection $($conn.name) ($($conn.id))"
        }
    }
} catch {
    Write-Warning "API connection cleanup skipped: $($_.Exception.Message)"
}

if ($PlatformDbContainer -ne "") {
    $cleanupSql = @"
DELETE FROM migration_request_approvals
WHERE request_id IN (
    SELECT r.id FROM migration_requests r
    LEFT JOIN managed_db_connections c ON c.id = r.database_id
    WHERE c.name IN ('k6-target-1', 'k6-target-2', 'k6-target-3')
       OR r.requested_by IN (SELECT id FROM platform_users WHERE username = '$RunnerUsername')
);
DELETE FROM migration_requests
WHERE database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'))
   OR requested_by IN (SELECT id FROM platform_users WHERE username = '$RunnerUsername');
DELETE FROM database_locks
WHERE database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'))
   OR job_id IN (SELECT job_id FROM migration_jobs WHERE actor_username = '$RunnerUsername');
DELETE FROM migration_runs
WHERE database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'))
   OR executed_by IN (SELECT id FROM platform_users WHERE username = '$RunnerUsername');
DELETE FROM migration_jobs
WHERE database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'))
   OR actor_username = '$RunnerUsername';
DELETE FROM migrations
WHERE database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'))
   OR created_by IN (SELECT id FROM platform_users WHERE username = '$RunnerUsername');
DELETE FROM connection_grants
WHERE group_id IN (SELECT id FROM access_groups WHERE name = '$GroupName')
   OR database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'));
DELETE FROM managed_db_connections
WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3');
DELETE FROM managed_sessions
WHERE user_id IN (SELECT id FROM platform_users WHERE username = '$RunnerUsername');
UPDATE access_groups SET leader_user_id = NULL WHERE name = '$GroupName';
DELETE FROM user_group_memberships
WHERE group_id IN (SELECT id FROM access_groups WHERE name = '$GroupName')
   OR user_id IN (SELECT id FROM platform_users WHERE username = '$RunnerUsername');
DELETE FROM access_groups WHERE name = '$GroupName';
DELETE FROM platform_users WHERE username = '$RunnerUsername';
"@
    $cleanupSql | docker exec -i $PlatformDbContainer psql -U $PlatformDbUser -d $PlatformDbName -v ON_ERROR_STOP=1 | Out-Null
    Write-Host "Cleaned platform database stress test records"
}

if ($ExampleDbContainer -ne "") {
    foreach ($dbName in $targetDbs) {
        $null = docker exec $ExampleDbContainer psql -U $ExampleDbUser -d postgres -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS $dbName WITH (FORCE);"
        Write-Host "Dropped target database $dbName"
    }
}

$fixtureFull = Join-Path (Get-Location) $FixturePath
if (Test-Path $fixtureFull) {
    Remove-Item -Force $fixtureFull
    Write-Host "Removed $FixturePath"
}
