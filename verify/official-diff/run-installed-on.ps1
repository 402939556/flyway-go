#requires -Version 7.0
<#
.SYNOPSIS
  Compare Flyway OSS and flywaygo installed_on rendering on one shared history row.

.DESCRIPTION
  For each database engine and migration owner, this script:
    1. resets the database;
    2. runs the owner's migrate command;
    3. rewrites version 1 installed_on to 2024-01-02 03:04:05;
    4. runs Java info and Go info without resetting or migrating again.

  Both languages run with Asia/Shanghai. The script compares the exact
  InstalledOn and InstalledOnUTC columns from both info commands with the
  official Flyway 13.13.0 rendering.
#>
param(
    [ValidateSet('postgres', 'mysql')]
    [string[]]$Engines = @('postgres', 'mysql'),
    [switch]$NoBuild,
    [switch]$KeepWork
)

$ErrorActionPreference = 'Stop'
$Root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$Official = $PSScriptRoot
$Go = 'C:\PartBackup\app\gosdk\bin\go.exe'
$Java = 'C:\PartBackup\app\jdk21\bin\java.exe'
$Javac = 'C:\PartBackup\app\jdk21\bin\javac.exe'
$FlywayExe = Join-Path $Root 'bin\flywaygo.exe'
$DbProbe = Join-Path $Root 'bin\dbprobe.exe'
$Harness = Join-Path $Official 'harness'
$HarnessOut = Join-Path $Harness 'out'
$ResultsRoot = Join-Path $Official 'installed-on-results'
$WorkRoot = Join-Path $ResultsRoot '_work'
$Fixture = Join-Path $Official 'cases\installed-on\migrations'
$ExpectedInstalledOn = '2024-01-02 03:04:05.0'
$ExpectedInstalledOnUTC = '2024-01-01T19:04:05Z'

$Profiles = @{
    postgres = @{
        Driver = 'postgres'
        Schema = 'public'
        Dsn = $(if ($env:FLYWAYGO_PG_DSN) { $env:FLYWAYGO_PG_DSN } else { 'postgres://postgres:postgres@127.0.0.1:55432/flywaygo?sslmode=disable' })
        Jdbc = 'jdbc:postgresql://127.0.0.1:55432/flywaygo'
        User = 'postgres'
        Password = 'postgres'
        Url = 'postgres://postgres:postgres@127.0.0.1:55432/flywaygo?sslmode=disable'
        UpdateSql = "UPDATE flyway_schema_history SET installed_on = TIMESTAMP '2024-01-02 03:04:05' WHERE version = '1'"
    }
    mysql = @{
        Driver = 'mysql'
        Schema = 'flywaygo'
        Dsn = $(if ($env:FLYWAYGO_MYSQL_DSN) { $env:FLYWAYGO_MYSQL_DSN } else { 'root:root@tcp(127.0.0.1:53306)/flywaygo?parseTime=true' })
        Jdbc = 'jdbc:mysql://127.0.0.1:53306/flywaygo?allowPublicKeyRetrieval=true&useSSL=false'
        User = 'root'
        Password = 'root'
        Url = 'mysql://root:root@tcp(127.0.0.1:53306)/flywaygo?parseTime=true'
        UpdateSql = "UPDATE flyway_schema_history SET installed_on = '2024-01-02 03:04:05' WHERE version = '1'"
    }
}

function Invoke-External {
    param(
        [string]$File,
        [string[]]$Arguments,
        [string]$WorkingDirectory = $Root,
        [hashtable]$Environment = @{},
        [string[]]$ClearEnvironment = @()
    )

    $psi = [System.Diagnostics.ProcessStartInfo]::new()
    $psi.FileName = $File
    $psi.WorkingDirectory = $WorkingDirectory
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    $psi.StandardOutputEncoding = [System.Text.UTF8Encoding]::new($false)
    $psi.StandardErrorEncoding = [System.Text.UTF8Encoding]::new($false)

    foreach ($argument in @($Arguments)) {
        [void]$psi.ArgumentList.Add([string]$argument)
    }

    # Isolate the child process from host-level FLYWAY_* settings and only
    # apply the environment explicitly supplied for this invocation.
    $clearNames = @($psi.Environment.Keys | Where-Object { [string]$_ -like 'FLYWAY_*' })
    $clearNames += @($ClearEnvironment)
    $clearNames += @($Environment.Keys)
    $clearNames = @($clearNames | Where-Object { $_ } | Select-Object -Unique)
    foreach ($name in $clearNames) {
        [void]$psi.Environment.Remove([string]$name)
    }
    foreach ($entry in $Environment.GetEnumerator()) {
        if ($null -eq $entry.Value) {
            [void]$psi.Environment.Remove([string]$entry.Key)
        }
        else {
            $psi.Environment[[string]$entry.Key] = [string]$entry.Value
        }
    }

    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $psi
    try {
        if (-not $process.Start()) {
            throw "failed to start $File"
        }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $process.WaitForExit()
        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        $code = $process.ExitCode
    }
    finally {
        $process.Dispose()
    }

    $lines = [System.Collections.Generic.List[string]]::new()
    foreach ($streamText in @($stdout, $stderr)) {
        if ([string]::IsNullOrEmpty($streamText)) { continue }
        $parts = $streamText -split "\r?\n"
        for ($i = 0; $i -lt $parts.Length; $i++) {
            if ($i -eq ($parts.Length - 1) -and $parts[$i] -eq '') { continue }
            $lines.Add($parts[$i]) | Out-Null
        }
    }
    [pscustomobject]@{ ExitCode = $code; Lines = @($lines) }
}
function Invoke-DbProbe {
    param([string]$Engine, [string]$Action, [string]$Sql = '')
    $p = $Profiles[$Engine]
    $args = @('-driver', $p.Driver, '-dsn', $p.Dsn, '-action', $Action, '-schema', $p.Schema)
    if ($Sql) { $args += @('-sql', $Sql) }
    $r = Invoke-External $DbProbe $args
    if ($r.ExitCode -ne 0) {
        throw "dbprobe $Engine $Action failed ($($r.ExitCode)): $($r.Lines -join ' | ')"
    }
    return @($r.Lines)
}

function ConvertTo-FlywayPath([string]$Path) {
    return ([IO.Path]::GetFullPath($Path)).Replace('\', '/')
}

function Get-JavaClasspath {
    $jars = @(Get-ChildItem $Official -Filter '*.jar' | ForEach-Object { $_.FullName })
    return ((@($HarnessOut) + $jars) -join ';')
}

function Ensure-Tools {
    if (-not $NoBuild) {
        New-Item -ItemType Directory -Force (Join-Path $Root 'bin') | Out-Null
        $r = Invoke-External $Go @('build', '-o', $FlywayExe, '.\cmd\flywaygo')
        if ($r.ExitCode -ne 0) { throw "go build flywaygo failed: $($r.Lines -join "`n")" }
        $r = Invoke-External $Go @('build', '-o', $DbProbe, '.\verify\cmd\dbprobe')
        if ($r.ExitCode -ne 0) { throw "go build dbprobe failed: $($r.Lines -join "`n")" }
        New-Item -ItemType Directory -Force $HarnessOut | Out-Null
        $jars = @(Get-ChildItem $Official -Filter '*.jar' | ForEach-Object { $_.FullName })
        $cp = (@($HarnessOut) + $jars) -join ';'
        $r = Invoke-External $Javac @('-encoding', 'UTF-8', '-cp', $cp, '-d', $HarnessOut, (Join-Path $Harness 'ParityRunner.java'))
        if ($r.ExitCode -ne 0) { throw "javac ParityRunner failed: $($r.Lines -join "`n")" }
    }
    foreach ($path in @($FlywayExe, $DbProbe, (Join-Path $HarnessOut 'ParityRunner.class'))) {
        if (-not (Test-Path $path)) { throw "missing $path" }
    }
}

function Invoke-Migrate {
    param([string]$Engine, [string]$Owner)
    $p = $Profiles[$Engine]
    $location = ConvertTo-FlywayPath $Fixture
    if ($Owner -eq 'java') {
        $javaArgs = @('-Duser.language=en', '-Duser.country=US', '-Duser.timezone=Asia/Shanghai',
            '-cp', (Get-JavaClasspath), 'ParityRunner', 'migrate', $p.Jdbc, $p.User, $p.Password, $location)
        return Invoke-External $Java $javaArgs -Environment @{ TZ = 'Asia/Shanghai' }
    }
    $goArgs = @('migrate', '-url', $p.Url, '-locations', $location)
    return Invoke-External $FlywayExe $goArgs -Environment @{ TZ = 'Asia/Shanghai'; FLYWAYGO_INTERNAL_HARNESS = '1' }
}

function Invoke-Info {
    param([string]$Engine, [string]$Implementation)
    $p = $Profiles[$Engine]
    $location = ConvertTo-FlywayPath $Fixture
    if ($Implementation -eq 'java') {
        $javaArgs = @('-Duser.language=en', '-Duser.country=US', '-Duser.timezone=Asia/Shanghai',
            '-cp', (Get-JavaClasspath), 'ParityRunner', 'info', $p.Jdbc, $p.User, $p.Password, $location)
        return Invoke-External $Java $javaArgs -Environment @{ TZ = 'Asia/Shanghai' }
    }
    $goArgs = @('info', '-url', $p.Url, '-locations', $location)
    return Invoke-External $FlywayExe $goArgs -Environment @{ TZ = 'Asia/Shanghai'; FLYWAYGO_INTERNAL_HARNESS = '1' }
}

function Get-VersionOneInfo {
    param([string]$Engine, [string]$Implementation)
    $r = Invoke-Info -Engine $Engine -Implementation $Implementation
    if ($r.ExitCode -ne 0) {
        throw "$Implementation info failed ($($r.ExitCode)): $($r.Lines -join ' | ')"
    }
    $versionOneLines = @($r.Lines | Where-Object {
        $parts = $_.Split("`t")
        $parts.Length -eq 12 -and $parts[0] -eq 'INFO' -and $parts[2] -eq '1'
    })
    if ($versionOneLines.Count -ne 1) {
        throw "$Implementation info returned $($versionOneLines.Count) rows for version 1, want 1: $($r.Lines -join ' | ')"
    }
    $parts = $versionOneLines[0].Split("`t")
    return [pscustomobject]@{
        InstalledOn = $parts[10]
        InstalledOnUTC = $parts[11]
        Raw = $versionOneLines[0]
    }
}

Ensure-Tools
New-Item -ItemType Directory -Force $ResultsRoot | Out-Null
if (Test-Path $WorkRoot) { Remove-Item -Recurse -Force $WorkRoot }
New-Item -ItemType Directory -Force $WorkRoot | Out-Null

$summary = [System.Collections.Generic.List[object]]::new()
foreach ($engine in $Engines) {
    foreach ($owner in @('java', 'go')) {
        $runDir = Join-Path $WorkRoot ($engine + '-' + $owner)
        New-Item -ItemType Directory -Force $runDir | Out-Null
        $failures = [System.Collections.Generic.List[string]]::new()
        $javaInfo = $null
        $goInfo = $null
        try {
            Invoke-DbProbe -Engine $engine -Action 'reset' | Out-Null
            $m = Invoke-Migrate -Engine $engine -Owner $owner
            Set-Content -Path (Join-Path $runDir 'migrate.txt') -Value @($m.Lines) -Encoding utf8NoBOM
            if ($m.ExitCode -ne 0) { throw "owner $owner migrate failed ($($m.ExitCode)): $($m.Lines -join ' | ')" }

            Invoke-DbProbe -Engine $engine -Action 'exec' -Sql ([string]$Profiles[$engine].UpdateSql) | Out-Null
            $javaInfo = Get-VersionOneInfo -Engine $engine -Implementation 'java'
            $goInfo = Get-VersionOneInfo -Engine $engine -Implementation 'go'
            Set-Content -Path (Join-Path $runDir 'java-info.txt') -Value $javaInfo.Raw -Encoding utf8NoBOM
            Set-Content -Path (Join-Path $runDir 'go-info.txt') -Value $goInfo.Raw -Encoding utf8NoBOM

            foreach ($implementation in @('java', 'go')) {
                $actual = if ($implementation -eq 'java') { $javaInfo } else { $goInfo }
                if ($actual.InstalledOn -cne $ExpectedInstalledOn) {
                    $failures.Add("$implementation InstalledOn=$($actual.InstalledOn), want $ExpectedInstalledOn") | Out-Null
                }
                if ($actual.InstalledOnUTC -cne $ExpectedInstalledOnUTC) {
                    $failures.Add("$implementation InstalledOnUTC=$($actual.InstalledOnUTC), want $ExpectedInstalledOnUTC") | Out-Null
                }
            }
            if ($javaInfo.InstalledOn -cne $goInfo.InstalledOn) {
                $failures.Add("InstalledOn java=$($javaInfo.InstalledOn), go=$($goInfo.InstalledOn)") | Out-Null
            }
            if ($javaInfo.InstalledOnUTC -cne $goInfo.InstalledOnUTC) {
                $failures.Add("InstalledOnUTC java=$($javaInfo.InstalledOnUTC), go=$($goInfo.InstalledOnUTC)") | Out-Null
            }
        }
        catch {
            $failures.Add($_.Exception.Message + "`n" + ($_.ScriptStackTrace)) | Out-Null
        }
        $status = if ($failures.Count -eq 0) { 'PASS' } else { 'FAIL' }
        $summary.Add([pscustomobject]@{
            Status = $status
            Engine = $engine
            Owner = $owner
            InstalledOn = if ($null -ne $goInfo) { $goInfo.InstalledOn } else { '' }
            InstalledOnUTC = if ($null -ne $goInfo) { $goInfo.InstalledOnUTC } else { '' }
            Failures = @($failures)
        }) | Out-Null
        Write-Host ('[{0}] {1}/{2}' -f $status, $engine, $owner)
        foreach ($failure in $failures) { Write-Host ('       ' + $failure) }
    }
}

$summaryPath = Join-Path $ResultsRoot 'summary.tsv'
$summaryLines = foreach ($row in $summary) {
    '{0}`t{1}`t{2}`t{3}`t{4}`t{5}' -f $row.Status, $row.Engine, $row.Owner, $row.InstalledOn, $row.InstalledOnUTC, ($row.Failures -join ' | ')
}
[IO.File]::WriteAllLines($summaryPath, [string[]]$summaryLines, [Text.UTF8Encoding]::new($false))
$failed = @($summary | Where-Object { $_.Status -ne 'PASS' })
Write-Host ''
Write-Host ('installed_on parity: {0} passed, {1} failed' -f ($summary.Count - $failed.Count), $failed.Count)
Write-Host ('Results: ' + $summaryPath)
if (-not $KeepWork) { Write-Host ('Raw work: ' + $WorkRoot) }
if ($failed.Count -gt 0) { exit 1 }
