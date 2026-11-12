# Windows PowerShell 5.1+
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('start', 'stop')]
    [string]$Action,
    [switch]$Build,
    [switch]$NoBrowser
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$executable = Join-Path $projectRoot 'bin\mail-lite.exe'
$cacheDir = Join-Path $projectRoot '.cache'
$statePath = Join-Path $cacheDir 'service-state.json'
$pidPath = Join-Path $cacheDir 'local-server.pid'
$stdoutPath = Join-Path $cacheDir 'local-server.stdout.log'
$stderrPath = Join-Path $cacheDir 'local-server.stderr.log'
$serviceLock = $null
$launchedProcess = $null

function Get-ServiceProcess {
    # Check both executable path and creation time to avoid stopping a reused PID.
    if (Test-Path -LiteralPath $statePath) {
        $state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
        $candidate = Get-Process -Id $state.processId -ErrorAction SilentlyContinue
        if ($candidate -and $candidate.Path -eq $executable -and
            [string]$candidate.StartTime.ToUniversalTime().Ticks -eq $state.startedUtcTicks) {
            return $candidate
        }
        return $null
    }

    # Adopt the previous local launcher, including its existing PID file.
    if (Test-Path -LiteralPath $pidPath) {
        $savedId = 0
        if ([int]::TryParse((Get-Content -LiteralPath $pidPath -Raw).Trim(), [ref]$savedId)) {
            $candidate = Get-Process -Id $savedId -ErrorAction SilentlyContinue
            if ($candidate -and $candidate.Path -eq $executable) { return $candidate }
        }
    }
    $matches = @(Get-Process -Name 'mail-lite' -ErrorAction SilentlyContinue |
        Where-Object { $_.Path -eq $executable })
    if ($matches.Count -gt 1) {
        throw 'Multiple instances use this executable. Stop them individually before using this script.'
    }
    if ($matches.Count -eq 1) { return $matches[0] }
    return $null
}

function Save-ServiceState($ServiceProcess, [string]$Url) {
    [pscustomobject]@{
        processId = $ServiceProcess.Id
        startedUtcTicks = [string]$ServiceProcess.StartTime.ToUniversalTime().Ticks
        url = $Url
    } | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding UTF8
    $ServiceProcess.Id | Set-Content -LiteralPath $pidPath -Encoding ASCII
}

function Import-ServiceEnvironment {
    $envPath = Join-Path $projectRoot '.env'
    $allowed = @('NODE_ROLE', 'LISTEN_ADDR', 'DATA_DIR', 'DATA_KEY', 'ADMIN_PASSWORD',
        'APP_ORIGIN', 'UPSTREAM_CONCURRENCY', 'MASTER_URL', 'NODE_ID', 'NODE_TOKEN',
        'ALLOW_INSECURE_NODE_HTTP', 'TLS_CERT', 'TLS_KEY',
        'SMTP_ENABLED', 'SMTP_LISTEN_ADDR', 'SMTP_HOSTNAME', 'SMTP_TLS_CERT', 'SMTP_TLS_KEY')
    if (Test-Path -LiteralPath $envPath) {
        $lineNumber = 0
        foreach ($line in Get-Content -LiteralPath $envPath -Encoding UTF8) {
            $lineNumber++
            if ($line -match '^\s*(#|$)') { continue }
            if ($line -notmatch '^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$') {
                throw "Invalid .env syntax on line $lineNumber. Use NAME=value."
            }
            $name = $Matches[1]
            $value = $Matches[2].Trim()
            if ($allowed -notcontains $name) { continue }
            if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or
                ($value.StartsWith("'") -and $value.EndsWith("'")))) {
                $value = $value.Substring(1, $value.Length - 2)
            } else {
                $value = ($value -replace '\s+#.*$', '').TrimEnd()
            }
            # Explicit environment variables take precedence over .env.
            if ([string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($name, 'Process'))) {
                [Environment]::SetEnvironmentVariable($name, $value, 'Process')
            }
        }
    } else {
        # Keep the existing local deployment's address and data directory.
        $runtimePath = Join-Path $cacheDir 'local-runtime.json'
        if (Test-Path -LiteralPath $runtimePath) {
            $runtime = Get-Content -LiteralPath $runtimePath -Raw | ConvertFrom-Json
            if (-not $env:LISTEN_ADDR) { $env:LISTEN_ADDR = $runtime.listen }
            if (-not $env:DATA_DIR) { $env:DATA_DIR = $runtime.dataDir }
            if (-not $env:APP_ORIGIN) { $env:APP_ORIGIN = $runtime.url }
        }
    }
    if (-not $env:NODE_ROLE) { $env:NODE_ROLE = 'primary' }
    if (-not $env:LISTEN_ADDR) { $env:LISTEN_ADDR = '127.0.0.1:8080' }
    if (-not $env:DATA_DIR) { $env:DATA_DIR = Join-Path $projectRoot 'data' }
    if (-not [System.IO.Path]::IsPathRooted($env:DATA_DIR)) {
        $env:DATA_DIR = [System.IO.Path]::GetFullPath((Join-Path $projectRoot $env:DATA_DIR))
    }
}

function Get-LocalUrl {
    $listen = $env:LISTEN_ADDR
    if ($listen.StartsWith(':')) { $listen = '127.0.0.1' + $listen }
    $listen = $listen -replace '^0\.0\.0\.0:', '127.0.0.1:' -replace '^\[::\]:', '[::1]:'
    $scheme = 'http'
    if ($env:TLS_CERT) { $scheme = 'https' }
    return "$($scheme)://$listen"
}

try {
    New-Item -ItemType Directory -Path $cacheDir -Force | Out-Null
    try {
        $serviceLock = [System.IO.File]::Open((Join-Path $cacheDir 'service.lock'),
            [System.IO.FileMode]::OpenOrCreate, [System.IO.FileAccess]::ReadWrite,
            [System.IO.FileShare]::None)
    } catch {
        throw 'Another start/stop operation is in progress. Try again after it finishes.'
    }

    $serviceProcess = Get-ServiceProcess
    if ($Action -eq 'stop') {
        if ($serviceProcess) {
            Stop-Process -InputObject $serviceProcess -ErrorAction Stop
            if (-not $serviceProcess.WaitForExit(10000)) { throw 'Timed out stopping Mail Lite.' }
            Write-Host "Mail Lite stopped (PID $($serviceProcess.Id))."
        } else {
            Write-Host 'Mail Lite is already stopped.'
        }
        Remove-Item -LiteralPath $statePath, $pidPath -Force -ErrorAction SilentlyContinue
        exit 0
    }

    Import-ServiceEnvironment
    $localUrl = Get-LocalUrl
    if ($serviceProcess) {
        if ($Build) { throw 'Stop Mail Lite before rebuilding: run stop.bat first.' }
        if (Test-Path -LiteralPath $statePath) {
            $localUrl = (Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json).url
        }
        Save-ServiceState $serviceProcess $localUrl
        Write-Host "Mail Lite is already running (PID $($serviceProcess.Id))."
        Write-Host "$localUrl/admin/"
        if (-not $NoBrowser) {
            try { Start-Process "$localUrl/admin/" | Out-Null }
            catch { Write-Warning 'Open the URL above in your browser.' }
        }
        exit 0
    }

    # Fail before creating a database if the requested address is occupied.
    $address = [uri]$localUrl
    $listener = [System.Net.Sockets.TcpListener]::new(
        [System.Net.IPAddress]::Any, $address.Port)
    $listener.ExclusiveAddressUse = $true
    try { $listener.Start() } catch { throw "Port $($address.Port) is already in use." }
    finally { $listener.Stop() }

    if ($Build -or -not (Test-Path -LiteralPath $executable)) {
        if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
            throw 'Go 1.27+ is required to build. Install Go or put mail-lite.exe in mail-lite\bin.'
        }
        Write-Host 'Building Mail Lite (using the existing web/dist assets)...'
        New-Item -ItemType Directory -Path (Split-Path -Parent $executable) -Force | Out-Null
        Push-Location $projectRoot
        try {
            & go build -trimpath '-ldflags=-s -w' -o $executable ./cmd/mail-lite
            if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
        } finally { Pop-Location }
    }

    $databasePath = Join-Path $env:DATA_DIR 'mail-lite.db'
    if ($env:NODE_ROLE -eq 'primary' -and -not $env:ADMIN_PASSWORD -and
        -not (Test-Path -LiteralPath $databasePath)) {
        $securePassword = Read-Host 'Set the initial admin password (at least 12 characters)' -AsSecureString
        $credential = New-Object System.Management.Automation.PSCredential('admin', $securePassword)
        $env:ADMIN_PASSWORD = $credential.GetNetworkCredential().Password
    }
    if ($env:ADMIN_PASSWORD -and ([System.Text.Encoding]::UTF8.GetByteCount($env:ADMIN_PASSWORD) -lt 12 -or
        $env:ADMIN_PASSWORD -eq 'replace-with-at-least-12-characters')) {
        throw 'Set ADMIN_PASSWORD to your own password of at least 12 characters.'
    }
    if ($env:NODE_ROLE -eq 'primary' -and -not $env:ADMIN_PASSWORD -and
        -not (Test-Path -LiteralPath $databasePath)) {
        throw 'An initial admin password is required.'
    }

    $launchedProcess = Start-Process -FilePath $executable -WorkingDirectory $projectRoot -WindowStyle Hidden `
        -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath -PassThru
    Save-ServiceState $launchedProcess $localUrl
    $started = $false
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($launchedProcess.HasExited) { throw "Mail Lite exited. See $stderrPath" }
        try {
            # Replicas may be alive while waiting for their first synchronization.
            $health = Invoke-RestMethod -Uri "$localUrl/health/live" -TimeoutSec 2
            if ($health.ok -eq $true -and -not $launchedProcess.HasExited) { $started = $true; break }
        } catch { }
        Start-Sleep -Milliseconds 250
    }
    if (-not $started) { throw "Mail Lite startup timed out. See $stderrPath" }
    Write-Host "Mail Lite started (PID $($launchedProcess.Id))."
    Write-Host "$localUrl/admin/"
    Write-Host "Data: $env:DATA_DIR"
    Write-Host "Log:  $stderrPath"
    # Do not treat a browser failure as a service failure.
    if (-not $NoBrowser) {
        try { Start-Process "$localUrl/admin/" | Out-Null }
        catch { Write-Warning 'Open the URL above in your browser.' }
    }
} catch {
    if ($launchedProcess) {
        if (-not $launchedProcess.HasExited) {
            Stop-Process -InputObject $launchedProcess -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $statePath, $pidPath -Force -ErrorAction SilentlyContinue
    }
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
} finally {
    if ($serviceLock) { $serviceLock.Dispose() }
}
