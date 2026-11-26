#requires -Version 7.0
<#
.SYNOPSIS
  Compare flywaygo with the official Flyway OSS 13.13.0 jars.

.DESCRIPTION
  Every scenario is executed independently against both engines. The Java
  implementation runs first, then the Go implementation, with a fresh database
  before each implementation. All steps in a scenario share the same database
  state so callbacks, repair, and follow-up info/validate are meaningful.

  Comparison is structural: canonical command lines, exit status, schema
  history, and ordinary database objects. Text that Flyway localizes, stack
  traces and execution_time are intentionally excluded. Timestamp columns are
  masked in the main diff; run-installed-on.ps1 compares them precisely.
#>
param(
    [string[]]$Scenarios = @(),
    [ValidateSet('postgres', 'mysql')]
    [string[]]$Engines = @('postgres', 'mysql'),
    [switch]$NoBuild,
    [switch]$KeepWork,
    [switch]$FailFast
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
$ResultsRoot = Join-Path $Official 'parity-results'
$WorkRoot = Join-Path $ResultsRoot '_work'

$Profiles = @{
    postgres = @{
        Driver = 'postgres'
        Schema = 'public'
        Dsn = $(if ($env:FLYWAYGO_PG_DSN) { $env:FLYWAYGO_PG_DSN } else { 'postgres://postgres:postgres@127.0.0.1:55432/flywaygo?sslmode=disable' })
        Jdbc = 'jdbc:postgresql://127.0.0.1:55432/flywaygo'
        User = 'postgres'
        Password = 'postgres'
        Url = 'postgres://postgres:postgres@127.0.0.1:55432/flywaygo?sslmode=disable'
    }
    mysql = @{
        Driver = 'mysql'
        Schema = 'flywaygo'
        Dsn = $(if ($env:FLYWAYGO_MYSQL_DSN) { $env:FLYWAYGO_MYSQL_DSN } else { 'root:root@tcp(127.0.0.1:53306)/flywaygo?parseTime=true' })
        Jdbc = 'jdbc:mysql://127.0.0.1:53306/flywaygo?allowPublicKeyRetrieval=true&useSSL=false'
        User = 'root'
        Password = 'root'
        Url = 'mysql://root:root@tcp(127.0.0.1:53306)/flywaygo?parseTime=true'
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

    # Child processes must never inherit a stale FLYWAY_* value from the
    # PowerShell host or from a previous scenario. Only scenario-local values
    # are visible to the implementation being exercised.
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
    param(
        [string]$Engine,
        [string]$Action,
        [string]$Table = 'flyway_schema_history',
        [string]$Sql = '',
        [string]$Schemas = ''
    )
    $p = $Profiles[$Engine]
    $args = @('-driver', $p.Driver, '-dsn', $p.Dsn, '-action', $Action, '-table', $Table)
    if ($Schemas) {
        $args += @('-schemas', $Schemas)
    } else {
        $args += @('-schema', $p.Schema)
    }
    if ($Sql) { $args += @('-sql', $Sql) }
    $r = Invoke-External $DbProbe $args
    if ($r.ExitCode -ne 0) {
        throw "dbprobe $Engine $Action failed ($($r.ExitCode)): $($r.Lines -join ' | ')"
    }
    return @($r.Lines)
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
    if (-not (Test-Path $FlywayExe)) { throw "missing $FlywayExe" }
    if (-not (Test-Path $DbProbe)) { throw "missing $DbProbe" }
    if (-not (Test-Path (Join-Path $HarnessOut 'ParityRunner.class'))) { throw 'missing compiled ParityRunner.class' }
}

function Get-JavaClasspath {
    $jars = @(Get-ChildItem $Official -Filter '*.jar' | ForEach-Object { $_.FullName })
    return ((@($HarnessOut) + $jars) -join ';')
}

function ConvertTo-FlywayPath([string]$Path) {
    $full = [System.IO.Path]::GetFullPath($Path, $Root)
    return $full.Replace('\', '/')
}

function Get-ScenarioStepLocation {
    param([hashtable]$Scenario, [hashtable]$Step, [string]$FixtureDir, [string]$WorkDir)
    $location = $Step.Location
    if (-not $location) { $location = $Scenario.Location }
    if (-not $location) { $location = '.' }
    if ($env:PARITY_DEBUG) {
        Write-Host ("[DEBUG] resolve scenarioLocation={0} stepLocation={1} location={2} fixture={3}" -f $Scenario.Location, $Step.Location, $location, $FixtureDir)
    }
    if ($location -eq '$WORK') { return (ConvertTo-FlywayPath $WorkDir) }
    if ($location -eq 'case') { return (ConvertTo-FlywayPath $FixtureDir) }
    if ($location.StartsWith('case/')) {
        return (ConvertTo-FlywayPath (Join-Path $FixtureDir $location.Substring(5)))
    }
    if ([System.IO.Path]::IsPathRooted($location)) {
        return (ConvertTo-FlywayPath $location)
    }
    return (ConvertTo-FlywayPath (Join-Path $FixtureDir $location))
}

function New-ScenarioFixture {
    param([string]$ScenarioName, [string]$Implementation, [string]$RunDir, [string]$SourceCase)
    $caseName = if ($SourceCase) { $SourceCase } else { $ScenarioName }
    $source = Join-Path $Official ('cases\' + $caseName)
    $fixture = Join-Path $RunDir ($Implementation + '-fixture')
    New-Item -ItemType Directory -Force $fixture | Out-Null
    if (Test-Path $source) {
        Copy-Item -Path (Join-Path $source '*') -Destination $fixture -Recurse -Force
    }
    return $fixture
}

function Get-ImplementationTokens {
    param([string]$Implementation, [string]$Engine, [string]$FixtureDir)
    $p = $Profiles[$Engine]
    $url = if ($Implementation -eq 'java') { $p.Jdbc } else { $p.Url }
    $invalidUrl = if ($Implementation -eq 'java') { 'jdbc:postgresql://127.0.0.1:1/flywaygo' } else { 'postgres://postgres:postgres@127.0.0.1:1/flywaygo?sslmode=disable' }
    if ($Engine -eq 'mysql') {
        $invalidUrl = if ($Implementation -eq 'java') { 'jdbc:mysql://127.0.0.1:1/flywaygo?allowPublicKeyRetrieval=true&useSSL=false' } else { 'mysql://root:root@tcp(127.0.0.1:1)/flywaygo?parseTime=true' }
    }
    $native = [System.IO.Path]::GetFullPath($FixtureDir)
    $slash = $native.Replace('\', '/')
    return @{
        '@@PARITY_DB_URL@@' = $url
        '@@PARITY_INVALID_DB_URL@@' = $invalidUrl
        '@@PARITY_DB_USER@@' = $p.User
        '@@PARITY_DB_PASSWORD@@' = $p.Password
        '@@PARITY_FIXTURE@@' = $slash
        '@@PARITY_WORKDIR@@' = ($slash.TrimEnd('/') + '/work')
        '@@PARITY_INSTALL@@' = ($slash.TrimEnd('/') + '/install')
        '@@PARITY_HOME@@' = ($slash.TrimEnd('/') + '/home')
        '@@PARITY_MIGRATIONS@@' = ($slash.TrimEnd('/') + '/migrations')
    }
}

function Expand-FixtureTokens {
    param([string]$Text, [hashtable]$Tokens)
    $expanded = $Text
    foreach ($key in ($Tokens.Keys | Sort-Object { $_.Length } -Descending)) {
        $expanded = $expanded.Replace($key, [string]$Tokens[$key])
    }
    return $expanded
}

function Write-FixtureTokens {
    param([string]$FixtureDir, [hashtable]$Tokens)
    $extensions = @('.conf', '.sql', '.properties', '.txt', '.toml')
    foreach ($file in @(Get-ChildItem -LiteralPath $FixtureDir -Recurse -File)) {
        if ($extensions -notcontains $file.Extension.ToLowerInvariant()) { continue }
        $content = [System.IO.File]::ReadAllText($file.FullName)
        $expanded = Expand-FixtureTokens -Text $content -Tokens $Tokens
        if ($expanded -cne $content) {
            [System.IO.File]::WriteAllText($file.FullName, $expanded, [System.Text.UTF8Encoding]::new($false))
        }
    }
}

function Initialize-ScenarioFixture {
    param([hashtable]$Scenario, [string]$Implementation, [string]$Engine, [string]$FixtureDir)
    foreach ($dir in @('home', 'install', 'install\conf', 'work', 'migrations')) {
        New-Item -ItemType Directory -Force (Join-Path $FixtureDir $dir) | Out-Null
    }
    if ($Implementation -eq 'go') {
        Copy-Item -Force $FlywayExe (Join-Path $FixtureDir 'install\flywaygo.exe')
    }
    if ($Scenario.ContainsKey('FixtureFiles') -and $Scenario.FixtureFiles) {
        foreach ($entry in $Scenario.FixtureFiles.GetEnumerator()) {
            $target = Resolve-FixturePath -FixtureDir $FixtureDir -RelativePath ([string]$entry.Key)
            New-Item -ItemType Directory -Force (Split-Path -Parent $target) | Out-Null
            [System.IO.File]::WriteAllText($target, [string]$entry.Value, [System.Text.UTF8Encoding]::new($false))
        }
    }
    $tokens = Get-ImplementationTokens -Implementation $Implementation -Engine $Engine -FixtureDir $FixtureDir
    Write-FixtureTokens -FixtureDir $FixtureDir -Tokens $tokens
}

function Get-ImplementationEnvironment {
    param([hashtable]$Scenario, [string]$Implementation, [string]$Engine, [string]$FixtureDir)
    $tokens = Get-ImplementationTokens -Implementation $Implementation -Engine $Engine -FixtureDir $FixtureDir
    $result = @{
        USERPROFILE = [string]$tokens['@@PARITY_HOME@@']
        HOME = [string]$tokens['@@PARITY_HOME@@']
        TZ = 'Asia/Shanghai'
    }
    if ($Scenario.ContainsKey('Environment') -and $Scenario.Environment) {
        foreach ($entry in $Scenario.Environment.GetEnumerator()) {
            $value = Expand-FixtureTokens -Text ([string]$entry.Value) -Tokens $tokens
            $result[[string]$entry.Key] = $value
        }
    }
    return $result
}

function Get-ScenarioTable {
    param([hashtable]$Scenario)
    if ($Scenario.ContainsKey('HistoryTable') -and $Scenario.HistoryTable) { return [string]$Scenario.HistoryTable }
    if ($Scenario.ContainsKey('Options') -and $Scenario.Options -and $Scenario.Options.ContainsKey('table')) { return [string]$Scenario.Options['table'] }
    foreach ($step in @($Scenario.Steps)) {
        if ($step.ContainsKey('Options') -and $step.Options -and $step.Options.ContainsKey('table')) { return [string]$step.Options['table'] }
    }
    return 'flyway_schema_history'
}
function Resolve-FixturePath {
    param([string]$FixtureDir, [string]$RelativePath)
    if ([System.IO.Path]::IsPathRooted($RelativePath)) {
        return [System.IO.Path]::GetFullPath($RelativePath)
    }
    $fixtureRoot = [System.IO.Path]::GetFullPath($FixtureDir)
    $resolved = [System.IO.Path]::GetFullPath((Join-Path $fixtureRoot $RelativePath))
    if (-not $resolved.StartsWith($fixtureRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "fixture path escapes scenario root: $RelativePath"
    }
    return $resolved
}

function Invoke-StepFixtureMutation {
    param(
        [hashtable]$Step,
        [string]$ScenarioName,
        [string]$FixtureDir,
        [string]$RunDir,
        [hashtable]$Tokens
    )
    if (-not $Step.ContainsKey('FileAction')) { return }

    $fileAction = [string]$Step.FileAction
    $relativePath = [string]$Step.Path
    if (-not $relativePath) { throw "$ScenarioName step FileAction requires Path" }
    $target = Resolve-FixturePath -FixtureDir $FixtureDir -RelativePath $relativePath
    New-Item -ItemType Directory -Force (Split-Path -Parent $target) | Out-Null

    switch -Regex ($fileAction) {
        '^(?i)remove$' {
            if (Test-Path $target) { Remove-Item -Force $target }
        }
        '^(?i)write$' {
            $utf8 = [System.Text.UTF8Encoding]::new($false)
            $text = Expand-FixtureTokens -Text ([string]$Step.Content) -Tokens $Tokens
            [System.IO.File]::WriteAllText($target, $text, $utf8)
        }
        '^(?i)append$' {
            $utf8 = [System.Text.UTF8Encoding]::new($false)
            $existing = if (Test-Path $target) { [System.IO.File]::ReadAllText($target) } else { '' }
            $text = Expand-FixtureTokens -Text ([string]$Step.Content) -Tokens $Tokens
            [System.IO.File]::WriteAllText($target, $existing + $text, $utf8)
        }
        '^(?i)writebytes$' {
            $base64 = [string]$Step.ContentBase64
            if (-not $base64) { throw "$ScenarioName WriteBytes step requires ContentBase64" }
            [System.IO.File]::WriteAllBytes($target, [System.Convert]::FromBase64String($base64))
        }
        '^(?i)copy$' {
            $sourcePath = [string]$Step.Source
            if (-not $sourcePath) { throw "$ScenarioName copy step requires Source" }
            $source = Resolve-FixturePath -FixtureDir (Join-Path $Official ('cases\' + $ScenarioName)) -RelativePath $sourcePath
            Copy-Item -Force $source $target
        }
        default { throw "unsupported FileAction '$fileAction' in scenario $ScenarioName" }
    }

    if ($fileAction -notmatch '^(?i)remove$' -and $fileAction -notmatch '^(?i)writebytes$') {
        Write-FixtureTokens -FixtureDir $FixtureDir -Tokens $Tokens
    }
}
function Get-JavaArgs {
    param(
        [string]$Action,
        [string]$Engine,
        [string]$Location,
        [hashtable]$Options,
        [string]$FixtureDir,
        [string]$ConfigMode
    )
    $p = $Profiles[$Engine]
    $tokens = Get-ImplementationTokens -Implementation 'java' -Engine $Engine -FixtureDir $FixtureDir
    $args = @('-Duser.language=en', '-Duser.country=US', '-Duser.timezone=Asia/Shanghai')
    if ($ConfigMode -and $ConfigMode -ne 'none') {
        $args += ('-Duser.home=' + [string]$tokens['@@PARITY_HOME@@'])
    }
    $args += @('-cp', (Get-JavaClasspath), 'ParityRunner', $Action, $p.Jdbc, $p.User, $p.Password,
        (Expand-FixtureTokens -Text $Location -Tokens $tokens))
    if ($ConfigMode -and $ConfigMode -ne 'none') {
        $args += @(
            ('configMode=' + $ConfigMode),
            ('workingDirectory=' + [string]$tokens['@@PARITY_WORKDIR@@']),
            ('installationDirectory=' + [string]$tokens['@@PARITY_INSTALL@@']),
            'configFileEncoding=UTF-8'
        )
    }
    if ($Options) {
        foreach ($key in ($Options.Keys | Sort-Object)) {
            if ((-not $ConfigMode -or $ConfigMode -eq 'none') -and ([string]$key).ToLowerInvariant() -eq 'locations') { continue }
            $value = Expand-FixtureTokens -Text ([string]$Options[$key]) -Tokens $tokens
            $args += ($key + '=' + $value)
        }
    }
    return $args
}

function Get-GoArgs {
    param(
        [string]$Action,
        [string]$Engine,
        [string]$Location,
        [hashtable]$Options,
        [string]$FixtureDir,
        [string]$ConfigMode
    )
    $tokens = Get-ImplementationTokens -Implementation 'go' -Engine $Engine -FixtureDir $FixtureDir
    if ($ConfigMode -and $ConfigMode -ne 'none') {
        $args = @($Action, '-working-directory', [string]$tokens['@@PARITY_WORKDIR@@'])
    }
    else {
        $args = @($Action, '-url', $Profiles[$Engine].Url, '-locations',
            (Expand-FixtureTokens -Text $Location -Tokens $tokens))
    }
    if (-not $Options) { return $args }

    $stringOptions = @{
        url                = '-url'
        user               = '-user'
        password           = '-password'
        driver             = '-driver'
        locations          = '-locations'
        callbackLocations  = '-callback-locations'
        table              = '-table'
        schemas            = '-schemas'
        defaultSchema      = '-default-schema'
        baselineVersion    = '-baseline-version'
        baselineDescription = '-baseline-description'
        baselineMigrationPrefix = '-baseline-migration-prefix'
        target             = '-target'
        ignoreMigrationPatterns = '-ignore-migration-patterns'
        placeholders       = '-placeholders'
        placeholderPrefix  = '-placeholder-prefix'
        placeholderSuffix  = '-placeholder-suffix'
        placeholderSeparator = '-placeholder-separator'
        connectRetries     = '-connect-retries'
        connectRetriesInterval = '-connect-retries-interval'
        initSQL            = '-init-sql'
        encoding           = '-encoding'
        lockRetryCount     = '-lock-retry-count'
        sqlMigrationPrefix = '-sql-migration-prefix'
        repeatablePrefix   = '-repeatable-sql-migration-prefix'
        sqlMigrationSeparator = '-sql-migration-separator'
        sqlMigrationSuffixes = '-sql-migration-suffixes'
    }
    $boolOptions = @{
        validateOnMigrate       = '-validate-on-migrate'
        baselineOnMigrate       = '-baseline-on-migrate'
        cleanDisabled           = '-clean-disabled'
        outOfOrder              = '-out-of-order'
        validateMigrationNaming = '-validate-migration-naming'
        executeInTransaction    = '-execute-in-transaction'
        placeholderReplacement  = '-placeholder-replacement'
        mixed                   = '-mixed'
        group                   = '-group'
        createSchemas           = '-create-schemas'
        detectEncoding          = '-detect-encoding'
        failOnMissingLocations  = '-fail-on-missing-locations'
        skipDefaultCallbacks    = '-skip-default-callbacks'
        'postgresql.transactional.lock' = '-postgresql.transactional.lock'
    }

    foreach ($key in ($Options.Keys | Sort-Object)) {
        $value = Expand-FixtureTokens -Text ([string]$Options[$key]) -Tokens $tokens
        if ($boolOptions.ContainsKey($key)) {
            $args += ($boolOptions[$key] + '=' + $value)
        }
        elseif ($stringOptions.ContainsKey($key)) {
            $args += @($stringOptions[$key], $value)
        }
    }
    return $args
}
function Get-Canonical {
    param([string[]]$Lines, [string]$FixtureDir)
    $nativeFixture = if ($FixtureDir) { [System.IO.Path]::GetFullPath($FixtureDir) } else { '' }
    $flywayFixture = if ($nativeFixture) { $nativeFixture.Replace('\', '/') } else { '' }
    foreach ($line in $Lines) {
        $clean = $line.Replace("`r", '').Replace("`t", "`t")
        if ($nativeFixture) {
            $clean = $clean.Replace($nativeFixture, '<FIXTURE>')
            if ($flywayFixture -cne $nativeFixture) {
                $clean = $clean.Replace($flywayFixture, '<FIXTURE>')
            }
        }
        if ($clean -match '^(RESULT|INFO|INVALID|REPAIR|ERROR|QUERY|DATA)(\t|$)') {
            $clean
        }
    }
}

function Get-ErrorFamily {
    param([string]$Line)
    if ($Line -match 'Unknown configuration|Invalid value for|Unable to load config file|Using both TOML configuration and CONF configuration') { return 'CONFIGURATION' }
    if ($Line -match 'No migration with a target version') { return 'TARGET' }
    if ($Line -match 'Invalid SQL filenames') { return 'MIGRATION_NAMING' }
    if ($Line -match 'Validate failed|Detected resolved migration not applied|Detected applied migration not resolved') { return 'VALIDATE_ERROR' }
    if ($Line -match 'Found more than one migration with version|Duplicate migration version|duplicate version') { return 'DUPLICATE' }
    if ($Line -match 'Connection refused|Communications link failure|Unable to connect|connection attempt failed|connect:.+refused') { return 'DB_CONNECTION' }
    if ($Line -match 'Migrate failed|Migration failed|Unable to apply migration|failed to (?:apply|execute)|Failed to execute script|语句执行失败|创建历史表.*失败') { return 'MIGRATE_FAILED' }
    return 'ERROR'
}

function Normalize-CompareLine {
    param([string]$Line)
    if ($Line -match '^ERROR') { return ('ERROR' + "`t" + (Get-ErrorFamily $Line)) }
    if ($Line -match '^INVALID\t([^\t]*)\t([^\t]*)\t([^\t]*)\t([^\t]*)\t') {
        return ('INVALID' + "`t" + $Matches[1] + "`t" + $Matches[2] + "`t" + $Matches[3] + "`t" + $Matches[4])
    }
    if ($Line -match '^INFO\t') {
        $parts = $Line.Split("`t")
        if ($parts.Length -ne 12) {
            throw "unexpected INFO column count $($parts.Length), want 12: $Line"
        }
        $local = $parts[10]
        $utc = $parts[11]
        if ($local -ne 'NULL' -and $local -cnotmatch '^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d{1,9})?$') {
            throw "unexpected installed_on format: $local"
        }
        if ($utc -ne 'NULL' -and $utc -cnotmatch '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$') {
            throw "unexpected installed_on_utc format: $utc"
        }
        $parts[10] = '<TIME>'
        $parts[11] = '<TIME_UTC>'
        return ($parts -join "`t")
    }
    return $Line
}

function Compare-Lines {
    param([string[]]$Expected, [string[]]$Actual)
    $e = @($Expected | ForEach-Object { Normalize-CompareLine $_ })
    $a = @($Actual | ForEach-Object { Normalize-CompareLine $_ })
    if (($e -join "`n") -ceq ($a -join "`n")) { return $null }
    $diff = [System.Collections.Generic.List[string]]::new()
    $max = [Math]::Max($e.Count, $a.Count)
    for ($i = 0; $i -lt $max; $i++) {
        $expectedLine = if ($i -lt $e.Count) { $e[$i] } else { '<missing>' }
        $actualLine = if ($i -lt $a.Count) { $a[$i] } else { '<missing>' }
        if ($expectedLine -cne $actualLine) {
            $diff.Add(('@@ line {0} @@' -f ($i + 1))) | Out-Null
            $diff.Add(('- ' + $expectedLine)) | Out-Null
            $diff.Add(('+ ' + $actualLine)) | Out-Null
        }
    }
    return ($diff -join "`n")
}

function Invoke-Implementation {
    param(
        [string]$Implementation,
        [string]$Engine,
        [string]$Action,
        [string]$Location,
        [hashtable]$Options,
        [string]$WorkDir,
        [string]$FixtureDir,
        [string]$ConfigMode,
        [hashtable]$Environment
    )
    if ($env:PARITY_DEBUG) {
        Write-Host ("[DEBUG] {0}/{1} {2} location={3} cwd={4} config={5}" -f $Implementation, $Engine, $Action, $Location, $WorkDir, $ConfigMode)
    }
    if ($Implementation -eq 'java') {
        $javaArgs = Get-JavaArgs -Action $Action -Engine $Engine -Location $Location -Options $Options -FixtureDir $FixtureDir -ConfigMode $ConfigMode
        if ($env:PARITY_DEBUG) { Write-Host ('[DEBUG] java args=' + ($javaArgs -join ' | ')) }
        return Invoke-External -File $Java -Arguments $javaArgs -WorkingDirectory $WorkDir -Environment $Environment
    }
    $exe = if ($ConfigMode -and $ConfigMode -ne 'none') { Join-Path $FixtureDir 'install\flywaygo.exe' } else { $FlywayExe }
    $goArgs = Get-GoArgs -Action $Action -Engine $Engine -Location $Location -Options $Options -FixtureDir $FixtureDir -ConfigMode $ConfigMode
    # The parity harness compares the canonical machine-readable protocol, not
    # the user-facing CLI text. Keep this explicit until a separate user-CLI
    # differential suite is added.
    $goEnvironment = @{}
    foreach ($entry in $Environment.GetEnumerator()) { $goEnvironment[$entry.Key] = $entry.Value }
    $goEnvironment['FLYWAYGO_INTERNAL_HARNESS'] = '1'
    if ($env:PARITY_DEBUG) { Write-Host ('[DEBUG] go args=' + ($goArgs -join ' | ')) }
    return Invoke-External -File $exe -Arguments $goArgs -WorkingDirectory $WorkDir -Environment $goEnvironment
}

function Get-Scenarios {
    $all = [ordered]@{}
    $all['basic'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['placeholders'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ placeholders = 'table_name=ph_table' } },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['callbacks'] = @{
        Engine = 'both'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'info'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'validate'; Options = @{ callbackLocations = 'callbacks' } }
        )
    }
    $all['callback-flow'] = @{
        Engine = 'both'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks'; validateOnMigrate = 'false' } },
            @{ Action = 'validate'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'repair'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'info'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'clean'; Options = @{ cleanDisabled = 'false' } }
        )
    }
    $all['ignore-patterns'] = @{
        Engine = 'both'; Location = '.'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ ignoreMigrationPatterns = '*:pending' } },
            @{ Action = 'info'; Options = @{ ignoreMigrationPatterns = '*:pending' } },
            @{ Action = 'validate'; Options = @{ ignoreMigrationPatterns = '*:pending' } }
        )
    }
    $all['target'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ target = '2' } },
            @{ Action = 'info'; Options = @{ target = '2' } },
            @{ Action = 'validate'; Options = @{ target = '2' } }
        )
    }
    $all['target-optional-missing'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ target = '4?' } },
            @{ Action = 'info'; Options = @{ target = '4?' } },
            @{ Action = 'validate'; Options = @{ target = '4?' } }
        )
    }
    $all['target-missing-required'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ target = '4' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ target = '4' }; ExpectExit = 1 },
            @{ Action = 'validate'; Options = @{ target = '4' }; ExpectExit = 1 }
        )
    }
    $all['pg-schema-create-true'] = @{
        Engine = 'postgres'; Location = 'case/migrations'; PrepareSql = @('DROP SCHEMA IF EXISTS s1 CASCADE')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'true' } },
            @{ Action = 'info'; Options = @{ schemas = 's1'; createSchemas = 'true' } },
            @{ Action = 'validate'; Options = @{ schemas = 's1'; createSchemas = 'true' } }
        )
    }
    $all['pg-schema-create-false'] = @{
        Engine = 'postgres'; Location = 'case/migrations'; PrepareSql = @('DROP SCHEMA IF EXISTS s1 CASCADE')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'false' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ schemas = 's1'; createSchemas = 'false' } }
        )
    }
    $all['pg-schemas-multi'] = @{
        Engine = 'postgres'; Location = 'case/migrations'; PrepareSql = @('DROP SCHEMA IF EXISTS s1 CASCADE','DROP SCHEMA IF EXISTS s2 CASCADE')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1,s2'; createSchemas = 'true' } },
            @{ Action = 'info'; Options = @{ schemas = 's1,s2'; createSchemas = 'true' } },
            @{ Action = 'validate'; Options = @{ schemas = 's1,s2'; createSchemas = 'true' } }
        )
    }
    $all['pg-schema-marker'] = @{
        Engine = 'postgres'; Location = 'case/migrations'; PrepareSql = @('DROP SCHEMA IF EXISTS s1 CASCADE')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'true' } },
            @{ Action = 'info'; Options = @{ schemas = 's1'; createSchemas = 'true' } }
        )
    }
    $all['pg-schema-missing-history'] = @{
        Engine = 'postgres'; Location = 'case/migrations'; PrepareSql = @('DROP SCHEMA IF EXISTS s1 CASCADE','CREATE SCHEMA s1','CREATE TABLE s1.existing_object (id INT)')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'true' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ schemas = 's1'; createSchemas = 'true' } }
        )
    }
    $all['pg-schema-marker-baseline-conflict'] = @{
        Engine = 'postgres'; Location = 'case/migrations'; PrepareSql = @('DROP SCHEMA IF EXISTS s1 CASCADE')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'true' } },
            @{ Action = 'baseline'; Options = @{ schemas = 's1'; createSchemas = 'true'; baselineVersion = '0' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ schemas = 's1'; createSchemas = 'true' } }
        )
    }
    $all['mysql-schema-create-true'] = @{
        Engine = 'mysql'; Location = 'case/migrations'; PrepareSql = @('DROP DATABASE IF EXISTS s1')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'true' } },
            @{ Action = 'info'; Options = @{ schemas = 's1'; createSchemas = 'true' } },
            @{ Action = 'validate'; Options = @{ schemas = 's1'; createSchemas = 'true' } }
        )
    }
    $all['mysql-schema-create-false'] = @{
        Engine = 'mysql'; Location = 'case/migrations'; PrepareSql = @('DROP DATABASE IF EXISTS s1')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'false' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ schemas = 's1'; createSchemas = 'false' } }
        )
    }
    $all['mysql-schemas-multi'] = @{
        Engine = 'mysql'; Location = 'case/migrations'; PrepareSql = @('DROP DATABASE IF EXISTS s1','DROP DATABASE IF EXISTS s2')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1,s2'; createSchemas = 'true' } },
            @{ Action = 'info'; Options = @{ schemas = 's1,s2'; createSchemas = 'true' } },
            @{ Action = 'validate'; Options = @{ schemas = 's1,s2'; createSchemas = 'true' } }
        )
    }
    $all['mysql-schema-marker'] = @{
        Engine = 'mysql'; Location = 'case/migrations'; PrepareSql = @('DROP DATABASE IF EXISTS s1')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'true' } },
            @{ Action = 'info'; Options = @{ schemas = 's1'; createSchemas = 'true' } }
        )
    }
    $all['mysql-schema-missing-history'] = @{
        Engine = 'mysql'; Location = 'case/migrations'; PrepareSql = @('DROP DATABASE IF EXISTS s1','CREATE DATABASE s1','CREATE TABLE s1.existing_object (id INT)')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'true' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ schemas = 's1'; createSchemas = 'true' } }
        )
    }
    $all['mysql-schema-marker-baseline-conflict'] = @{
        Engine = 'mysql'; Location = 'case/migrations'; PrepareSql = @('DROP DATABASE IF EXISTS s1')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'true' } },
            @{ Action = 'baseline'; Options = @{ schemas = 's1'; createSchemas = 'true'; baselineVersion = '0' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ schemas = 's1'; createSchemas = 'true' } }
        )
    }
    $all['validate-migration-naming'] = @{
        Engine = 'both'; Location = '.'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ validateMigrationNaming = 'true' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ validateMigrationNaming = 'true' }; ExpectExit = 1 },
            @{ Action = 'validate'; Options = @{ validateMigrationNaming = 'true' }; ExpectExit = 1 }
        )
    }
    $all['pg-failed'] = @{
        Engine = 'postgres'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; ExpectExit = 1 },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['mysql-failed'] = @{
        Engine = 'mysql'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; ExpectExit = 1 },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['pg-clean'] = @{
        Engine = 'postgres'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'clean'; Options = @{ cleanDisabled = 'false' } }
        )
    }
    $all['mysql-clean'] = @{
        Engine = 'mysql'; Location = '.'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'clean'; Options = @{ cleanDisabled = 'false' } }
        )
    }
    $all['pg-clean-complex'] = @{
        Engine = 'postgres'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'clean'; Options = @{ cleanDisabled = 'false' } }
        )
    }
    $all['mysql-clean-trigger'] = @{
        Engine = 'mysql'; Location = '.'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'clean'; Options = @{ cleanDisabled = 'false' } }
        )
    }
    $all['pg-schema-clean-created'] = @{
        Engine = 'postgres'; Location = 'case/migrations'
        PrepareSql = @('DROP SCHEMA IF EXISTS s1 CASCADE')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'true' } },
            @{ Action = 'clean'; Options = @{ cleanDisabled = 'false'; schemas = 's1'; createSchemas = 'true' } }
        )
    }
    $all['mysql-schema-clean-created'] = @{
        Engine = 'mysql'; Location = '.'
        PrepareSql = @('DROP DATABASE IF EXISTS s1')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ schemas = 's1'; createSchemas = 'true' } },
            @{ Action = 'clean'; Options = @{ cleanDisabled = 'false'; schemas = 's1'; createSchemas = 'true' } }
        )
    }
    $all['pg-concurrent'] = @{
        Engine = 'postgres'; Location = 'case/migrations'
        Options = @{ 'postgresql.transactional.lock' = 'false' }
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['mysql-mix'] = @{
        Engine = 'mysql'; Location = '.'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ mixed = 'true' }; ExpectExit = 1 },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['mysql-routine-plain'] = @{
        Engine = 'mysql'; Location = '.'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['repair-missing'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ FileAction = 'Remove'; Path = 'migrations/R__rr.sql' },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 },
            @{ Action = 'repair' },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 0 }
        )
    }
    $all['repair-missing-versioned'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ FileAction = 'Remove'; Path = 'migrations/V2__two.sql' },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 },
            @{ Action = 'repair' },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 0 }
        )
    }
    $all['repair-version-checksum'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ FileAction = 'Write'; Path = 'migrations/V1__base.sql'; Content = "CREATE TABLE rv_base (id INT, changed INT);`n" },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 },
            @{ Action = 'repair' },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 0 }
        )
    }
    $all['validate-on-migrate-false'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ FileAction = 'Write'; Path = 'migrations/V1__ok.sql'; Content = "CREATE TABLE vom_ok (id INT, changed INT);`n" },
            @{ Action = 'validate'; ExpectExit = 1 },
            @{ Action = 'migrate'; Options = @{ validateOnMigrate = 'false' } },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['out-of-order'] = @{
        Engine = 'both'; Location = '.'
        Steps = @(
            @{ FileAction = 'Remove'; Path = 'V2__two.sql' },
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ FileAction = 'Write'; Path = 'V2__two.sql'; Content = "CREATE TABLE oo2 (id int);`n" },
            @{ Action = 'info' },
            @{ Action = 'migrate'; Options = @{ outOfOrder = 'true' } },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['out-of-order-info'] = @{
        Engine = 'both'; Location = '.'
        Steps = @(
            @{ FileAction = 'Remove'; Path = 'V2__two.sql' },
            @{ Action = 'migrate' },
            @{ FileAction = 'Write'; Path = 'V2__two.sql'; Content = "CREATE TABLE oo_two (id INT);`n" },
            @{ Action = 'info' },
            @{ Action = 'migrate'; Options = @{ outOfOrder = 'true' } },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['callbacks-all'] = @{
        Engine = 'both'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks'; validateOnMigrate = 'false' } },
            @{ Action = 'validate'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'repair'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'info'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'clean'; Options = @{ callbackLocations = 'callbacks'; cleanDisabled = 'false' } }
        )
    }
    $all['callback-default-location'] = @{
        Engine = 'both'; Location = '.'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'query'; Sql = "SELECT event FROM cb_default_events ORDER BY event" }
        )
    }
    $all['info-repeat'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['mysql-repeat-refresh'] = @{
        Engine = 'mysql'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ FileAction = 'Write'; Path = 'migrations/R__rr-view.sql'; Content = "CREATE OR REPLACE VIEW v_rr_refresh AS SELECT id, id AS id2, id AS id3 FROM rr_refresh;`n" },
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['mysql-repeat-repair'] = @{
        Engine = 'mysql'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ FileAction = 'Write'; Path = 'migrations/R__rr-view.sql'; Content = "CREATE OR REPLACE VIEW v_rr AS SELECT id, id AS id2, id AS id3, id AS id4 FROM rr;`n" },
            @{ Action = 'info' },
            @{ Action = 'repair' },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['mysql-repeat-missing'] = @{
        Engine = 'mysql'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ FileAction = 'Remove'; Path = 'migrations/R__rr.sql' },
            @{ Action = 'info' },
            @{ Action = 'repair' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['mysql-repair-failed'] = @{
        Engine = 'mysql'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; ExpectExit = 1 },
            @{ Action = 'info' },
            @{ Action = 'repair' },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['nested-desc'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['pg-nested-comment'] = @{
        Engine = 'postgres'; Location = '.'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['info-states'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['version-markers'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ target = 'current' } },
            @{ Action = 'info'; Options = @{ target = 'current' } }
        )
    }
    $all['pg-group-mixed-error'] = @{
        Engine = 'postgres'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ group = 'true' }; ExpectExit = 1 },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['pg-group-rollback'] = @{
        Engine = 'postgres'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ group = 'true' }; ExpectExit = 1 },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['pg-copy'] = @{
        Engine = 'postgres'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' },
            @{ Action = 'query'; Sql = "SELECT id, val IS NULL, note IS NULL, length(val), encode(convert_to(val, 'UTF8'), 'hex'), encode(convert_to(note, 'UTF8'), 'hex') FROM copy_data ORDER BY id" }
        )
    }
    $all['pg-execute-in-transaction-false'] = @{
        Engine = 'postgres'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ executeInTransaction = 'false' }; ExpectExit = 1 },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['placeholders-off'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ placeholders = 'table_name=none'; placeholderReplacement = 'false' }; ExpectExit = 1 },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['placeholders-custom'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ placeholders = 'table_name=custom_table'; placeholderPrefix = '<<'; placeholderSuffix = '>>' } },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['placeholders-empty'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ placeholders = 'table_name=' }; ExpectExit = 1 },
            @{ Action = 'info' }
        )
    }
    $all['placeholders-undefined'] = @{
        Engine = 'postgres'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate'; ExpectExit = 1 },
            @{ Action = 'info' }
        )
    }
    $all['statement-callbacks'] = @{
        Engine = 'both'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'info' },
            @{ Action = 'validate' },
            @{ Action = 'query'; Sql = "SELECT seq, event FROM stmt_events ORDER BY seq" }
        )
    }
    $all['statement-callbacks-error'] = @{
        Engine = 'both'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks' }; ExpectExit = 1 },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 },
            @{ Action = 'query'; Sql = "SELECT seq, event FROM stmt_events ORDER BY seq" }
        )
    }
    $all['callback-placeholders'] = @{
        Engine = 'both'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks'; placeholders = 'callback_table=callback_placeholder' } },
            @{ Action = 'info'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'validate'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'query'; Sql = "SELECT COUNT(*) FROM callback_placeholder" }
        )
    }
    $all['callback-placeholder-undefined'] = @{
        Engine = 'both'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ callbackLocations = 'callbacks' } }
        )
    }
    $all['callback-conf'] = @{
        Engine = 'both'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'info'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'query'; Sql = "SELECT id, value FROM cb_conf ORDER BY id" }
        )
    }
    $all['callback-parser-postgres'] = @{
        Engine = 'postgres'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks'; placeholders = 'copy_word=world' } },
            @{ Action = 'info'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'validate'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'query'; Sql = "SELECT id, value FROM cb_copy_data ORDER BY id" }
        )
    }
    $all['callback-parser-mysql'] = @{
        Engine = 'mysql'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'info'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'validate'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'query'; Sql = "SELECT id FROM cb_mysql_data ORDER BY id" }
        )
    }
    $all['callback-error-event'] = @{
        Engine = 'both'; Location = 'case/migrations'; CallbackLocations = 'callbacks'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ callbackLocations = 'callbacks' } },
            @{ Action = 'query'; Sql = "SELECT seq, event FROM cb_error_event ORDER BY seq" }
        )
    }
    $all['callback-duplicate'] = @{
        Engine = 'both'; Location = 'case/migrations'; CallbackLocations = 'callbacks1,callbacks2'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ callbackLocations = 'callbacks1,callbacks2' }; ExpectExit = 1 },
            @{ Action = 'info'; Options = @{ callbackLocations = 'callbacks1,callbacks2' }; ExpectExit = 1 }

        )
    }
    $all['baseline-new-default'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline' }
        )
    }
    $all['baseline-new-zero'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline'; Options = @{ baselineVersion = '0' } }
        )
    }
    $all['baseline-new-latest'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline'; Options = @{ baselineVersion = 'latest' } }
        )
    }
    $all['baseline-new-current'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline'; Options = @{ baselineVersion = 'current' } }
        )
    }
    $all['baseline-new-next'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline'; Options = @{ baselineVersion = 'next' } }
        )
    }
    $all['baseline-custom-description'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline'; Options = @{ baselineVersion = '2.5'; baselineDescription = 'custom baseline' } },
            @{ Action = 'info' }
        )
    }
    $all['baseline-existing-history-same'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline' },
            @{ Action = 'baseline' },
            @{ Action = 'info' }
        )
    }
    $all['baseline-existing-history-different'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline'; Options = @{ baselineVersion = '1' } },
            @{ Action = 'baseline'; Options = @{ baselineVersion = '2' }; ExpectExit = 1 },
            @{ Action = 'info' }
        )
    }
    $all['baseline-on-migrate-empty'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'migrate'; Options = @{ baselineOnMigrate = 'true' } },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['baseline-on-migrate-nonempty'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'; PrepareSql = @('CREATE TABLE preexisting_baseline (id INT)')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ baselineOnMigrate = 'true' }; ExpectExit = 1 },
            @{ Action = 'info' },
            @{ Action = 'validate'; ExpectExit = 1 }
        )
    }
    $all['baseline-on-migrate-nonempty-latest'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'; PrepareSql = @('CREATE TABLE preexisting_baseline_latest (id INT)')
        Steps = @(
            @{ Action = 'migrate'; Options = @{ baselineOnMigrate = 'true'; baselineVersion = 'latest' }; ExpectExit = 1 },
            @{ Action = 'info'; ExpectExit = 1 }
        )
    }
    $all['baseline-nonempty-no-baseline-on-migrate'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'; PrepareSql = @('CREATE TABLE preexisting_no_baseline (id INT)')
        Steps = @(
            @{ Action = 'migrate'; ExpectExit = 1 },
            @{ Action = 'info' }
        )
    }
    $all['baseline-special-latest-then-migrate'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline'; Options = @{ baselineVersion = 'latest' } },
            @{ Action = 'migrate'; ExpectExit = 1 }
        )
    }
    $all['baseline-special-current-then-migrate'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline'; Options = @{ baselineVersion = 'current' } },
            @{ Action = 'migrate'; ExpectExit = 1 }
        )
    }
    $all['baseline-special-next-then-migrate'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'baseline'; Options = @{ baselineVersion = 'next' } },
            @{ Action = 'migrate'; ExpectExit = 1 }
        )
    }
    $all['baseline-reject-existing-migrations'] = @{
        Engine = 'both'; Location = 'case/migrations'; SourceCase = 'basic'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'baseline'; Options = @{ baselineVersion = '3' }; ExpectExit = 1 },
            @{ Action = 'info' }
        )
    }
    $configUrl = "flyway.url=@@PARITY_DB_URL@@`nflyway.user=@@PARITY_DB_USER@@`nflyway.password=@@PARITY_DB_PASSWORD@@`n"
    $configMigrations = "flyway.locations=filesystem:@@PARITY_MIGRATIONS@@`n"
    $badConfig = "flyway.url=@@PARITY_INVALID_DB_URL@@`nflyway.locations=filesystem:@@PARITY_FIXTURE@@/missing`nflyway.user=@@PARITY_DB_USER@@`nflyway.password=@@PARITY_DB_PASSWORD@@`n"

    $all['config-file-only'] = @{
        Engine = 'both'; SourceCase = 'basic'; ConfigMode = $true
        Location = 'case/migrations'
        FixtureFiles = @{ 'flyway.conf' = ($configUrl + $configMigrations) }
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['config-env-overrides-file'] = @{
        Engine = 'both'; SourceCase = 'basic'; ConfigMode = $true
        Location = 'case/migrations'
        FixtureFiles = @{ 'flyway.conf' = $badConfig }
        Environment = @{
            FLYWAY_URL = '@@PARITY_DB_URL@@'
            FLYWAY_LOCATIONS = 'filesystem:@@PARITY_MIGRATIONS@@'
        }
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['config-cli-overrides-env'] = @{
        Engine = 'both'; SourceCase = 'basic'; ConfigMode = $true
        Location = 'case/migrations'
        FixtureFiles = @{ 'flyway.conf' = $badConfig }
        Environment = @{
            FLYWAY_URL = '@@PARITY_DB_URL@@'
            FLYWAY_LOCATIONS = 'filesystem:@@PARITY_FIXTURE@@/missing-env'
        }
        Steps = @(
            @{ Action = 'migrate'; Options = @{ locations = 'filesystem:@@PARITY_MIGRATIONS@@' } },
            @{ Action = 'info'; Options = @{ locations = 'filesystem:@@PARITY_MIGRATIONS@@' } },
            @{ Action = 'validate'; Options = @{ locations = 'filesystem:@@PARITY_MIGRATIONS@@' } }
        )
    }
    $all['config-working-directory'] = @{
        Engine = 'both'; SourceCase = 'basic'; ConfigMode = $true
        Location = 'case/migrations'
        FixtureFiles = @{ 'work\flyway.conf' = ($configUrl + $configMigrations) }
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $placeholderMigration = 'CREATE TABLE ${table_name} (id INT);' + "`n"
    $all['config-placeholder-equals'] = @{
        Engine = 'both'; SourceCase = 'basic'; ConfigMode = $true
        Location = 'case/migrations'
        FixtureFiles = @{
            'flyway.conf' = ($configUrl + $configMigrations + "flyway.placeholders.table_name=cfg_table`n")
            'migrations\V3__config_placeholder.sql' = $placeholderMigration
        }
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' },
            @{ Action = 'query'; Sql = 'SELECT COUNT(*) FROM cfg_table' }
        )
    }
    $all['config-env-expansion'] = @{
        Engine = 'both'; SourceCase = 'basic'; ConfigMode = $true
        Location = 'case/migrations'
        FixtureFiles = @{
            'flyway.conf' = ($configUrl + $configMigrations + 'flyway.placeholders.table_name=${PARITY_PLACEHOLDER}' + "`n")
            'migrations\V3__config_placeholder.sql' = $placeholderMigration
        }
        Environment = @{ PARITY_PLACEHOLDER = 'expanded_table' }
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' },
            @{ Action = 'query'; Sql = 'SELECT COUNT(*) FROM expanded_table' }
        )
    }
    $all['config-default-layers'] = @{
        Engine = 'both'; SourceCase = 'basic'; ConfigMode = $true; HistoryTable = 'work_history'
        Location = 'case/migrations'
        FixtureFiles = @{
            'install\conf\flyway.conf' = ($configUrl + $configMigrations + "flyway.table=install_history`n")
            'home\flyway.conf' = "flyway.table=home_history`n"
            'flyway.conf' = "flyway.table=cwd_history`n"
            'work\flyway.conf' = "flyway.table=work_history`n"
        }
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' }
        )
    }
    $all['config-unknown-key'] = @{
        Engine = 'both'; SourceCase = 'basic'; ConfigMode = $true
        Location = 'case/migrations'
        FixtureFiles = @{ 'flyway.conf' = "flyway.notARealSetting=x`n" }
        Steps = @(
            @{ Action = 'migrate'; ExpectExit = 1 }
        )
    }

    $all['sql-literals'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' },
            @{ Action = 'query'; Sql = 'SELECT id, note FROM quote_demo ORDER BY id' }
        )
    }
    $all['sql-version-gap'] = @{
        Engine = 'both'; Location = 'case/migrations'
        Steps = @(
            @{ Action = 'migrate' },
            @{ Action = 'info' },
            @{ Action = 'validate' },
            @{ Action = 'query'; Sql = 'SELECT id, name FROM gap_item_v ORDER BY id' }
        )
    }
    return $all
}

function Get-ScenarioSchemas {
    param([hashtable]$Scenario)
    $schemas = $null
    if ($Scenario.ContainsKey('Options') -and $Scenario.Options -and $Scenario.Options.ContainsKey('schemas')) {
        $schemas = [string]$Scenario.Options['schemas']
    }
    foreach ($step in @($Scenario.Steps)) {
        if ($step.ContainsKey('Options') -and $step.Options -and $step.Options.ContainsKey('schemas')) {
            $schemas = [string]$step.Options['schemas']
        }
    }
    if (-not $schemas) { return @() }
    return @($schemas.Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ })
}

function Reset-ScenarioDatabase {
    param([string]$Engine, [hashtable]$Scenario, [switch]$ResetTargetSchemas)
    $p = $Profiles[$Engine]
    Invoke-DbProbe $Engine 'reset' | Out-Null
    $schemas = @(Get-ScenarioSchemas $Scenario)
    if ($schemas.Count -gt 0 -and $ResetTargetSchemas) {
        $resetSchemas = @($p.Schema) + $schemas | Select-Object -Unique
        Invoke-DbProbe -Engine $Engine -Action 'reset' -Schemas ($resetSchemas -join ',') | Out-Null
    }
    if ($Scenario.ContainsKey('PrepareSql')) {
        foreach ($sql in @($Scenario.PrepareSql)) {
            Invoke-DbProbe -Engine $Engine -Action 'exec' -Sql ([string]$sql) | Out-Null
        }
    }
}

function Convert-FixtureLocationList {
    param([string]$Value, [string]$FixtureDir)
    $parts = @()
    foreach ($item in $Value.Split(',')) {
        $trimmed = $item.Trim()
        if (-not $trimmed) { continue }
        if ([System.IO.Path]::IsPathRooted($trimmed)) { $parts += (ConvertTo-FlywayPath $trimmed) }
        else { $parts += (ConvertTo-FlywayPath (Join-Path $FixtureDir $trimmed)) }
    }
    return ($parts -join ',')
}

function Get-ConfiguredMode {
    param([hashtable]$Scenario)
    if (-not $Scenario.ContainsKey('ConfigMode')) { return 'none' }
    $value = $Scenario.ConfigMode
    if ($value -is [bool]) {
        if ($value) { return 'auto' }
        return 'none'
    }
    $mode = ([string]$value).Trim().ToLowerInvariant()
    if (-not $mode) { return 'none' }
    return $mode
}

function Run-Scenario {
    param([string]$ScenarioName, [hashtable]$Scenario, [string]$Engine)
    $runDir = Join-Path $WorkRoot ($ScenarioName + '-' + $Engine)
    if (Test-Path $runDir) { Remove-Item -Recurse -Force $runDir }
    New-Item -ItemType Directory -Force $runDir | Out-Null

    $outcomes = [ordered]@{}
    foreach ($impl in @('java', 'go')) {
        Reset-ScenarioDatabase -Engine $Engine -Scenario $Scenario -ResetTargetSchemas:($Scenario.ContainsKey('ResetTargetSchemas') -and [bool]$Scenario['ResetTargetSchemas'])
        $sourceCase = if ($Scenario.ContainsKey('SourceCase')) { [string]$Scenario['SourceCase'] } else { '' }
        $fixtureDir = New-ScenarioFixture -ScenarioName $ScenarioName -Implementation $impl -RunDir $runDir -SourceCase $sourceCase
        Initialize-ScenarioFixture -Scenario $Scenario -Implementation $impl -Engine $Engine -FixtureDir $fixtureDir
        $tokens = Get-ImplementationTokens -Implementation $impl -Engine $Engine -FixtureDir $fixtureDir
        $environment = Get-ImplementationEnvironment -Scenario $Scenario -Implementation $impl -Engine $Engine -FixtureDir $fixtureDir
        $configMode = Get-ConfiguredMode -Scenario $Scenario
        $invokeWorkDir = $fixtureDir
        $locationWorkDir = Join-Path $fixtureDir 'work'
        $stepsOut = @()
        $stepIndex = 0
        foreach ($step in @($Scenario.Steps)) {
            $stepIndex++
            if ($step.ContainsKey('FileAction')) {
                Invoke-StepFixtureMutation -Step $step -ScenarioName $ScenarioName -FixtureDir $fixtureDir -RunDir $runDir -Tokens $tokens
                $stepsOut += [pscustomobject]@{
                    Index = $stepIndex; Action = ('file:' + [string]$step.FileAction)
                    Location = $fixtureDir; ExitCode = 0; ExpectedExit = 0
                    Canonical = @(); Raw = @()
                }
                continue
            }
            if (-not $step.ContainsKey('Action') -or -not $step.Action) { continue }
            $action = [string]$step.Action
            $location = Get-ScenarioStepLocation -Scenario $Scenario -Step $step -FixtureDir $fixtureDir -WorkDir $locationWorkDir
            $options = @{}
            if ($Scenario.ContainsKey('Options') -and $Scenario.Options) {
                $Scenario.Options.GetEnumerator() | ForEach-Object { $options[$_.Key] = $_.Value }
            }
            if ($Scenario.ContainsKey('CallbackLocations') -and $Scenario.CallbackLocations) {
                $options['callbackLocations'] = $Scenario.CallbackLocations
            }
            if ($step.ContainsKey('Options') -and $step.Options) {
                $step.Options.GetEnumerator() | ForEach-Object { $options[$_.Key] = $_.Value }
            }
            if ($options.ContainsKey('callbackLocations')) {
                $options['callbackLocations'] = Convert-FixtureLocationList -Value ([string]$options['callbackLocations']) -FixtureDir $fixtureDir
            }

            if ($action -eq 'query') {
                $sql = [string]$step.Sql
                $env:FLYWAYGO_PG_DSN = $Profiles['postgres'].Dsn
                $env:FLYWAYGO_MYSQL_DSN = $Profiles['mysql'].Dsn
                $queryLines = @(Invoke-DbProbe -Engine $Engine -Action 'query' -Sql $sql | ForEach-Object { [string]$_ })
                $r = [pscustomobject]@{ ExitCode = 0; Lines = $queryLines }
            }
            else {
                $r = Invoke-Implementation -Implementation $impl -Engine $Engine -Action $action -Location $location -Options $options -WorkDir $invokeWorkDir -FixtureDir $fixtureDir -ConfigMode $configMode -Environment $environment
            }
            $canonical = @(Get-Canonical -Lines @($r.Lines) -FixtureDir $fixtureDir)
            $expectedExit = 0
            if ($step.ContainsKey('ExpectExit')) { $expectedExit = [int]$step.ExpectExit }
            $stepsOut += [pscustomobject]@{
                Index = $stepIndex; Action = $action; Location = $location
                ExitCode = $r.ExitCode; ExpectedExit = $expectedExit
                Canonical = $canonical; Raw = @($r.Lines)
            }
            $prefix = '{0}-{1}-{2:D2}-{3}' -f $ScenarioName, $impl, $stepIndex, $action
            Set-Content -Path (Join-Path $runDir ($prefix + '.raw.txt')) -Value @($r.Lines) -Encoding UTF8
            Set-Content -Path (Join-Path $runDir ($prefix + '.canonical.txt')) -Value $canonical -Encoding UTF8
        }

        $probeSchemas = @(Get-ScenarioSchemas $Scenario)
        $historyTable = Get-ScenarioTable $Scenario
        $history = if ($probeSchemas.Count -gt 0) {
            @(Invoke-DbProbe -Engine $Engine -Action 'history' -Table $historyTable -Schemas ($probeSchemas -join ',') | ForEach-Object { [string]$_ })
        } else {
            @(Invoke-DbProbe -Engine $Engine -Action 'history' -Table $historyTable | ForEach-Object { [string]$_ })
        }
        $objects = if ($probeSchemas.Count -gt 0) {
            @(Invoke-DbProbe -Engine $Engine -Action 'objects' -Table $historyTable -Schemas ($probeSchemas -join ',') | ForEach-Object { [string]$_ })
        } else {
            @(Invoke-DbProbe -Engine $Engine -Action 'objects' -Table $historyTable | ForEach-Object { [string]$_ })
        }
        Set-Content -Path (Join-Path $runDir ($ScenarioName + '-' + $impl + '.history.txt')) -Value $history -Encoding UTF8
        Set-Content -Path (Join-Path $runDir ($ScenarioName + '-' + $impl + '.objects.txt')) -Value $objects -Encoding UTF8
        $outcomes[$impl] = [pscustomobject]@{ Steps = $stepsOut; History = $history; Objects = $objects }
    }

    $failures = [System.Collections.Generic.List[string]]::new()
    $maxSteps = [Math]::Max($outcomes.java.Steps.Count, $outcomes.go.Steps.Count)
    for ($i = 0; $i -lt $maxSteps; $i++) {
        $j = $outcomes.java.Steps[$i]
        $g = $outcomes.go.Steps[$i]
        if ($null -eq $j -or $null -eq $g) { $failures.Add("step $($i+1): missing implementation result") | Out-Null; continue }
        if ($j.ExitCode -ne $g.ExitCode) { $failures.Add("step $($i+1) $($j.Action): exit java=$($j.ExitCode) go=$($g.ExitCode)") | Out-Null }
        if ($j.ExitCode -ne $j.ExpectedExit) { $failures.Add("step $($i+1) $($j.Action): official exit $($j.ExitCode), expected $($j.ExpectedExit)") | Out-Null }
        $diff = Compare-Lines -Expected @($j.Canonical) -Actual @($g.Canonical)
        if ($diff) {
            $failures.Add("step $($i+1) $($j.Action): canonical output differs") | Out-Null
            Set-Content -Path (Join-Path $runDir ("diff-{0:D2}-{1}.txt" -f ($i+1), $j.Action)) -Value $diff -Encoding UTF8
        }
    }
    $hDiff = Compare-Lines -Expected @($outcomes.java.History) -Actual @($outcomes.go.History)
    if ($hDiff) {
        $failures.Add('final schema history differs') | Out-Null
        Set-Content -Path (Join-Path $runDir 'diff-history.txt') -Value $hDiff -Encoding UTF8
    }
    $oDiff = Compare-Lines -Expected @($outcomes.java.Objects) -Actual @($outcomes.go.Objects)
    if ($oDiff) {
        $failures.Add('final ordinary objects differ') | Out-Null
        Set-Content -Path (Join-Path $runDir 'diff-objects.txt') -Value $oDiff -Encoding UTF8
    }
    return [pscustomobject]@{ Scenario = $ScenarioName; Engine = $Engine; RunDir = $runDir; Failures = $failures }
}

Ensure-Tools
New-Item -ItemType Directory -Force $ResultsRoot | Out-Null
if (Test-Path $WorkRoot) { Remove-Item -Recurse -Force $WorkRoot }
New-Item -ItemType Directory -Force $WorkRoot | Out-Null

$defs = Get-Scenarios
if ($Scenarios.Count -gt 0) {
    foreach ($name in $Scenarios) {
        if (-not $defs.Contains($name)) { throw "unknown scenario: $name" }
    }
}
else {
    $Scenarios = @($defs.Keys)
}

$summary = [System.Collections.Generic.List[object]]::new()
foreach ($name in $Scenarios) {
    $scenario = $defs[$name]
    $selected = $Engines
    if ($scenario.Engine -eq 'postgres') { $selected = @('postgres') }
    elseif ($scenario.Engine -eq 'mysql') { $selected = @('mysql') }
    foreach ($engine in $selected) {
        try {
            $res = Run-Scenario -ScenarioName $name -Scenario $scenario -Engine $engine
            $summary.Add($res) | Out-Null
            $status = if ($res.Failures.Count -eq 0) { 'PASS' } else { 'FAIL' }
            Write-Host ('[{0}] {1}/{2}' -f $status, $name, $engine)
            foreach ($f in $res.Failures) { Write-Host ('       ' + $f) }
            if ($res.Failures.Count -gt 0 -and $FailFast) { throw "parity failed: $name/$engine" }
        }
        catch {
            $summary.Add([pscustomobject]@{ Scenario = $name; Engine = $engine; RunDir = ''; Failures = @($_.Exception.Message) }) | Out-Null
            Write-Host ('[FAIL] {0}/{1}: {2}' -f $name, $engine, $_.Exception.Message)
            if ($FailFast) { throw }
        }
    }
}

$summaryLines = foreach ($r in $summary) {
    $status = if ($r.Failures.Count -eq 0) { 'PASS' } else { 'FAIL' }
    '{0}`t{1}`t{2}`t{3}' -f $status, $r.Scenario, $r.Engine, (($r.Failures -join ' | '))
}
Set-Content -Path (Join-Path $ResultsRoot 'summary.tsv') -Value $summaryLines -Encoding UTF8
$failed = @($summary | Where-Object { $_.Failures.Count -gt 0 })
Write-Host ''
Write-Host ('Parity: {0} passed, {1} failed' -f ($summary.Count - $failed.Count), $failed.Count)
if (-not $KeepWork) { Write-Host ('Raw results: ' + $WorkRoot) }
if ($failed.Count -gt 0) { exit 1 }
