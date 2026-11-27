param(
    [string]$BaseUrl = "http://127.0.0.1:8080",
    [string]$PublicOrigin = "http://localhost",
    [string]$AdminUsername = "admin",
    [string]$AdminPassword = "admin-local-change-me",
    [string]$RunnerUsername = "k6_runner",
    [string]$RunnerPassword = "k6-stress-runner-password",
    [string]$GroupName = "k6-stress-group",
    [string]$ExampleDbContainer = "",
    [string]$ExampleDbUser = "example_user",
    [string]$ExampleDbPassword = "example_local_password",
    [string]$ExampleDbHost = "db_example",
    [int]$SameDbCount = 240,
    [int]$DiffDb1Count = 80,
    [int]$DiffDb2Count = 80,
    [string]$OutputPath = "k6/fixtures/stress-fixtures.json"
)

$ErrorActionPreference = "Stop"

if ($ExampleDbContainer -eq "") {
    $ExampleDbContainer = (docker compose ps -q db_example | Select-Object -First 1).Trim()
    if ($ExampleDbContainer -eq "") {
        throw "db_example container is not running; start compose stack first"
    }
}

$initSqlPath = Join-Path $PSScriptRoot "sql\init_target_db.sql"
$initSql = Get-Content -Raw $initSqlPath

$targetSpecs = @(
    @{ Name = "k6-target-1"; DbName = "k6_stress_db_1"; Count = $SameDbCount; Group = "same_db" },
    @{ Name = "k6-target-2"; DbName = "k6_stress_db_2"; Count = $DiffDb1Count; Group = "diff_db" },
    @{ Name = "k6-target-3"; DbName = "k6_stress_db_3"; Count = $DiffDb2Count; Group = "diff_db" }
)

foreach ($spec in $targetSpecs) {
    $dbName = $spec.DbName
    $exists = (docker exec $ExampleDbContainer psql -U $ExampleDbUser -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$dbName';").Trim()
    if ($exists -ne "1") {
        $null = docker exec $ExampleDbContainer psql -U $ExampleDbUser -d postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE $dbName;"
        if ($LASTEXITCODE -ne 0) { throw "failed to create database $dbName" }
    }
    $initSql | docker exec -i $ExampleDbContainer psql -U $ExampleDbUser -d $dbName -v ON_ERROR_STOP=1 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "failed to initialize schema in $dbName" }
    Write-Host "Initialized target database $dbName (10000 rows)"
}

function New-ApiSession {
    param([string]$Username, [string]$Password)
    $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    $headers = @{
        "Origin"         = $PublicOrigin
        "Sec-Fetch-Site" = "same-origin"
    }
    $body = "username=$([uri]::EscapeDataString($Username))&password=$([uri]::EscapeDataString($Password))"
    $resp = Invoke-WebRequest -Uri "$BaseUrl/auth/login" -Method POST -Headers $headers -ContentType "application/x-www-form-urlencoded" -Body $body -WebSession $session -UseBasicParsing
    if ($resp.StatusCode -ne 204) {
        throw "login failed for $Username with status $($resp.StatusCode)"
    }
    return $session
}

function Invoke-ApiJson {
    param(
        [Microsoft.PowerShell.Commands.WebRequestSession]$Session,
        [string]$Method,
        [string]$Path,
        [object]$Body = $null
    )
    $headers = @{
        "Origin"         = $PublicOrigin
        "Sec-Fetch-Site" = "same-origin"
    }
    $params = @{
        Uri             = "$BaseUrl$Path"
        Method          = $Method
        Headers         = $headers
        WebSession      = $Session
        UseBasicParsing = $true
    }
    if ($null -ne $Body) {
        $params["ContentType"] = "application/json"
        $params["Body"] = ($Body | ConvertTo-Json -Depth 8 -Compress)
    }
    $resp = Invoke-WebRequest @params
    if ([string]::IsNullOrWhiteSpace($resp.Content)) {
        return $null
    }
    return ($resp.Content | ConvertFrom-Json)
}

$adminSession = New-ApiSession -Username $AdminUsername -Password $AdminPassword

$users = Invoke-ApiJson -Session $adminSession -Method GET -Path "/api/v1/access/users"
$runnerUser = $users | Where-Object { $_.username -eq $RunnerUsername } | Select-Object -First 1
if ($null -eq $runnerUser) {
    $formBody = "username=$([uri]::EscapeDataString($RunnerUsername))&password=$([uri]::EscapeDataString($RunnerPassword))"
    $createResp = Invoke-WebRequest -Uri "$BaseUrl/api/v1/access/users" -Method POST -Headers @{
        "Origin"         = $PublicOrigin
        "Sec-Fetch-Site" = "same-origin"
    } -ContentType "application/x-www-form-urlencoded" -Body $formBody -WebSession $adminSession -UseBasicParsing
    $runnerUser = $createResp.Content | ConvertFrom-Json
    Write-Host "Created runner user $RunnerUsername ($($runnerUser.id))"
} else {
    Write-Host "Reusing runner user $RunnerUsername ($($runnerUser.id))"
}

$accessInfo = Invoke-ApiJson -Session $adminSession -Method GET -Path "/api/v1/access/groups"
$groupObj = $accessInfo.groups | Where-Object { $_.name -eq $GroupName } | Select-Object -First 1
if ($null -eq $groupObj) {
    $groupObj = Invoke-ApiJson -Session $adminSession -Method POST -Path "/api/v1/access/groups" -Body @{
        name      = $GroupName
        memberIds = @($runnerUser.id)
        leaderId  = $runnerUser.id
    }
    Write-Host "Created group $GroupName ($($groupObj.id))"
} else {
    $null = Invoke-ApiJson -Session $adminSession -Method PUT -Path "/api/v1/access/groups/$($groupObj.id)/members/$($runnerUser.id)"
    $null = Invoke-ApiJson -Session $adminSession -Method PUT -Path "/api/v1/access/groups/$($groupObj.id)/leader" -Body @{ leaderId = $runnerUser.id }
    Write-Host "Reusing group $GroupName ($($groupObj.id))"
}

$existingConns = Invoke-ApiJson -Session $adminSession -Method GET -Path "/api/v1/admin/connections"
$connMap = @{}
foreach ($spec in $targetSpecs) {
    $existing = $existingConns | Where-Object { $_.name -eq $spec.Name } | Select-Object -First 1
    if ($null -ne $existing) {
        $connObj = $existing
        if (-not $connObj.connected) {
            $connObj = Invoke-ApiJson -Session $adminSession -Method POST -Path "/api/v1/connections/$($connObj.id)/connect"
        }
        Write-Host "Reusing connection $($spec.Name) ($($connObj.id))"
    } else {
        $dsn = "postgres://${ExampleDbUser}:${ExampleDbPassword}@${ExampleDbHost}:5432/$($spec.DbName)?sslmode=disable"
        $connObj = Invoke-ApiJson -Session $adminSession -Method POST -Path "/api/v1/connections" -Body @{
            name   = $spec.Name
            driver = "postgres"
            dsn    = $dsn
            pool   = @{
                maxOpen            = 10
                maxIdle            = 4
                maxLifetimeSeconds = 300
                maxIdleTimeSeconds = 60
            }
        }
        Write-Host "Created connection $($spec.Name) ($($connObj.id))"
    }
    $null = Invoke-ApiJson -Session $adminSession -Method PUT -Path "/api/v1/access/groups/$($groupObj.id)/connections/$($connObj.id)" -Body @{ access = "write" }
    $connMap[$spec.Name] = $connObj.id
}

$runnerSession = New-ApiSession -Username $RunnerUsername -Password $RunnerPassword

$sameDbRequests = New-Object System.Collections.Generic.List[object]
$diffDbRequests = New-Object System.Collections.Generic.List[object]
$totalTarget = $SameDbCount + $DiffDb1Count + $DiffDb2Count
$createdCount = 0

foreach ($spec in $targetSpecs) {
    $connId = $connMap[$spec.Name]
    for ($i = 1; $i -le $spec.Count; $i++) {
        $bucket = $i % 20
        $sqlUp = "UPDATE public.k6_stress_target SET counter = counter + 1, payload = md5(payload || id::text), updated_at = NOW();"
        $sqlDown = "UPDATE public.k6_stress_target SET counter = GREATEST(counter - 1, 0), updated_at = NOW() WHERE bucket = $bucket;"
        $draft = Invoke-ApiJson -Session $runnerSession -Method POST -Path "/api/v1/connections/$connId/migrations" -Body @{
            title          = "k6 stress $($spec.Name) #$i"
            description    = "k6 stress test apply request $i"
            sql_content    = $sqlUp
            sql_down       = $sqlDown
            execution_mode = "normal"
        }
        $req = Invoke-ApiJson -Session $runnerSession -Method POST -Path "/api/v1/connections/$connId/migrations/$($draft.id)/requests" -Body @{
            operation = "APPLY"
            revision  = $draft.revision
        }
        $approved = Invoke-ApiJson -Session $runnerSession -Method PUT -Path "/api/v1/connections/$connId/migration-requests/$($req.id)/approvals/$($groupObj.id)"
        if ($approved.status -ne "APPROVED") {
            throw "request $($req.id) did not become APPROVED (status=$($approved.status))"
        }
        $entry = [ordered]@{
            connection_id   = $connId
            connection_name = $spec.Name
            db_group        = $spec.Group
            migration_id    = $draft.id
            request_id      = $req.id
        }
        if ($spec.Group -eq "same_db") {
            $sameDbRequests.Add($entry)
        } else {
            $diffDbRequests.Add($entry)
        }
        $createdCount++
        if (($createdCount % 50) -eq 0 -or $createdCount -eq $totalTarget) {
            Write-Host "Prepared $createdCount / $totalTarget approved APPLY requests"
        }
    }
}

$interleaved = New-Object System.Collections.Generic.List[object]
$sameIdx = 0
$diffIdx = 0
while ($sameIdx -lt $sameDbRequests.Count -or $diffIdx -lt $diffDbRequests.Count) {
    for ($s = 0; $s -lt 3 -and $sameIdx -lt $sameDbRequests.Count; $s++) {
        $interleaved.Add($sameDbRequests[$sameIdx])
        $sameIdx++
    }
    for ($d = 0; $d -lt 2 -and $diffIdx -lt $diffDbRequests.Count; $d++) {
        $interleaved.Add($diffDbRequests[$diffIdx])
        $diffIdx++
    }
}

$fixture = [ordered]@{
    generated_at    = (Get-Date).ToUniversalTime().ToString("o")
    public_origin   = $PublicOrigin
    runner_username = $RunnerUsername
    runner_password = $RunnerPassword
    group_id        = $groupObj.id
    group_name      = $GroupName
    connections     = @(
        @{ id = $connMap["k6-target-1"]; name = "k6-target-1"; db_name = "k6_stress_db_1"; db_group = "same_db" },
        @{ id = $connMap["k6-target-2"]; name = "k6-target-2"; db_name = "k6_stress_db_2"; db_group = "diff_db" },
        @{ id = $connMap["k6-target-3"]; name = "k6-target-3"; db_name = "k6_stress_db_3"; db_group = "diff_db" }
    )
    requests        = $interleaved
}

$outFull = Join-Path (Get-Location) $OutputPath
$outDir = Split-Path -Parent $outFull
if (-not (Test-Path $outDir)) {
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
}
$fixture | ConvertTo-Json -Depth 8 | Set-Content -Path $outFull -Encoding UTF8
Write-Host "Wrote stress test fixtures ($($interleaved.Count) requests) to $OutputPath"
