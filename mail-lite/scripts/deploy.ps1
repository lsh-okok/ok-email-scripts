[CmdletBinding()]
param(
    [ValidateSet('Deploy', 'Update')][string]$Action = 'Deploy',
    [switch]$Configure,
    [switch]$PackageOnly
)

$ErrorActionPreference = 'Stop'
$appRoot = Split-Path -Parent $PSScriptRoot
$stateDir = Join-Path $appRoot '.deploy'
$configPath = Join-Path $stateDir 'server.json'
$utf8 = New-Object System.Text.UTF8Encoding($false)

function Read-Default([string]$Prompt, [string]$Default) {
    $answer = Read-Host "$Prompt [$Default]"
    if ([string]::IsNullOrWhiteSpace($answer)) { return $Default }
    return $answer.Trim()
}

function Assert-Config($Config) {
    if ($Config.HostName -notmatch '^[a-zA-Z0-9][a-zA-Z0-9.-]*$') { throw 'Invalid SSH hostname or IPv4 address.' }
    if ($Config.UserName -notmatch '^[a-z_][a-z0-9_-]*$') { throw 'Invalid SSH username.' }
    if ("$($Config.Port)" -notmatch '^\d+$' -or [int]$Config.Port -lt 1 -or [int]$Config.Port -gt 65535) { throw 'SSH port must be 1-65535.' }
    if ($Config.RemoteDir -notmatch '^/[a-zA-Z0-9_./-]+$' -or $Config.RemoteDir -match '(^|/)\.\.(/|$)' -or $Config.RemoteDir -match '^/+$') { throw 'Use an absolute remote directory without spaces or .. segments.' }
    if ($Config.ProjectName -notmatch '^[a-z0-9][a-z0-9_-]*$') { throw 'Invalid Compose project name.' }
    foreach ($name in @($Config.Domain, $Config.MXHostName)) {
        if ($name -and ($name -notmatch '^(?=.{1,253}$)([a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$')) { throw 'Use a DNS hostname, without https://, a path or a port.' }
    }
    if ($Config.EnableMX -isnot [bool]) { throw 'EnableMX must be true or false.' }
    if ($Config.EnableMX -and !$Config.MXHostName) { throw 'MX hostname is required when MX is enabled.' }
    if ($Config.ProxyMode -notin @('npm', 'caddy', 'direct')) { throw 'ProxyMode must be npm, caddy or direct.' }
    if ($Config.ProxyMode -ne 'direct' -and !$Config.Domain) { throw 'A website domain is required for npm/caddy mode.' }
    if ($Config.NPMNetwork -and $Config.NPMNetwork -notmatch '^[a-zA-Z0-9][a-zA-Z0-9_.-]*$') { throw 'Invalid NPM Docker network name.' }
}

function Quote-Sh([string]$Value) {
    return "'" + $Value.Replace("'", "'\''") + "'"
}

function Invoke-Checked([string]$Program, [string[]]$Arguments) {
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Program failed (exit $LASTEXITCODE)." }
}

function Test-DeploymentFile([System.IO.FileInfo]$File) {
    # Include build inputs, not arbitrary files from a developer's workspace.
    # New source files are selected from disk, regardless of their Git status.
    $name = $File.Name
    if ($name -like '.env*') { return $name -eq '.env.example' }
    if ($name.StartsWith('.') -and $name -notin @('.dockerignore', '.gitignore')) { return $false }
    if ($File.Extension.ToLowerInvariant() -in @('.pem', '.key', '.p12', '.pfx', '.ppk', '.der')) { return $false }
    if ($name -in @('Dockerfile', 'Caddyfile', 'Makefile', 'go.mod', 'go.sum', 'go.work', 'go.work.sum',
            '.dockerignore', '.gitignore', 'yarn.lock', 'CMakeLists.txt', 'LICENSE', 'NOTICE', 'COPYING') -or
        $name -like 'Dockerfile.*' -or $name -like 'requirements*.txt' -or
        $name -like 'LICENSE.*' -or $name -like 'NOTICE.*' -or $name -like 'COPYING.*') {
        return $true
    }
    # Ordinary import TXT/CSV files, databases, archives and private-key formats
    # deliberately have no allowed extension. Add future build inputs explicitly.
    return $File.Extension.ToLowerInvariant() -in @(
        '.go', '.js', '.mjs', '.cjs', '.ts', '.tsx', '.jsx', '.vue',
        '.css', '.scss', '.sass', '.less', '.html', '.htm',
        '.json', '.yaml', '.yml', '.toml', '.xml', '.sql', '.graphql', '.proto',
        '.md', '.rst', '.sh', '.ps1', '.bat',
        '.svg', '.png', '.jpg', '.jpeg', '.gif', '.webp', '.avif', '.ico',
        '.woff', '.woff2', '.ttf', '.otf', '.eot', '.wasm', '.map')
}

function Get-DeploymentFiles {
    $sourceRoots = @('cmd', 'internal', 'web', 'scripts', 'deploy', 'docs', 'examples')
    $rootFiles = @('Dockerfile', '.dockerignore', '.gitignore', '.env.example',
        'go.mod', 'go.sum', 'go.work', 'go.work.sum', 'Makefile', 'CMakeLists.txt',
        'package.json', 'package-lock.json', 'npm-shrinkwrap.json', 'pnpm-lock.yaml', 'yarn.lock')
    $pending = New-Object 'System.Collections.Generic.Stack[System.IO.DirectoryInfo]'
    foreach ($entry in Get-ChildItem -LiteralPath $appRoot -Force) {
        if ($entry.Attributes -band [System.IO.FileAttributes]::ReparsePoint) { continue }
        if ($entry.PSIsContainer) {
            if ($entry.Name -in $sourceRoots) { $pending.Push($entry) }
        } elseif ($entry.Name -in $rootFiles -or $entry.Name -like 'Dockerfile.*' -or
            $entry.Name -like 'compose*.yml' -or $entry.Name -like 'compose*.yaml' -or
            $entry.Name -like '*.md' -or $entry.Name -like 'requirements*.txt' -or
            $entry.Name -match '^(LICENSE|NOTICE|COPYING)(\..*)?$') {
            if (Test-DeploymentFile $entry) { './' + $entry.Name }
        }
    }
    while ($pending.Count -gt 0) {
        foreach ($entry in Get-ChildItem -LiteralPath $pending.Pop().FullName -Force) {
            # Never follow symlinks/junctions into private directories or files.
            if ($entry.Attributes -band [System.IO.FileAttributes]::ReparsePoint) { continue }
            $relative = $entry.FullName.Substring($appRoot.Length + 1).Replace('\', '/')
            if ($entry.PSIsContainer) {
                if (!$entry.Name.StartsWith('.') -and $entry.Name -ne 'node_modules' -and
                    $relative -ne 'web/ui/dist') { $pending.Push($entry) }
            } elseif (Test-DeploymentFile $entry) { './' + $relative }
        }
    }
}

try {
    foreach ($program in @('tar.exe', 'ssh.exe', 'scp.exe')) {
        if (!(Get-Command $program -ErrorAction SilentlyContinue)) { throw "Missing $program. Install Windows OpenSSH Client and tar first." }
    }
    New-Item -ItemType Directory -Path $stateDir -Force | Out-Null
    if (!$PackageOnly) {
        if ($Configure -or !(Test-Path -LiteralPath $configPath)) {
            if ($Action -eq 'Update' -and !$Configure) { throw 'Run deploy.bat once to configure the server.' }
            Write-Host 'First deployment: enter the SSH connection and website settings.'
            $server = [ordered]@{
                HostName = (Read-Default 'Server IPv4 or SSH hostname' '')
                UserName = (Read-Default 'SSH user (root or a user with passwordless sudo)' 'root')
                Port = (Read-Default 'SSH port' '22')
                RemoteDir = (Read-Default 'Server installation directory' '/opt/mail-lite')
                ProjectName = (Read-Default 'Compose project name (keep the existing name when updating)' 'mail-lite')
                ProxyMode = (Read-Default 'Website proxy: npm (existing Nginx Proxy Manager), caddy, direct' 'npm').ToLowerInvariant()
                Domain = (Read-Default 'Website domain (required for npm/caddy; empty = direct HTTP by IP)' '')
                NPMNetwork = ''
                EnableMX = ((Read-Default 'Enable domain MX receiving? y/n' 'n') -match '^(y|yes)$')
                MXHostName = ''
            }
            if ($server.ProxyMode -eq 'npm') { $server.NPMNetwork = Read-Default 'Existing NPM Docker network (empty = detect automatically)' '' }
            if ($server.EnableMX) { $server.MXHostName = Read-Default 'Public MX hostname, for example mx.example.com' '' }
            Assert-Config $server
            [System.IO.File]::WriteAllText($configPath, ($server | ConvertTo-Json), $utf8)
        }
        $server = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
        # Keep connection files written before proxy selection was introduced usable.
        if (!$server.PSObject.Properties['ProxyMode']) {
            $legacyMode = 'direct'
            if ($server.Domain) { $legacyMode = 'caddy' }
            $server | Add-Member -NotePropertyName ProxyMode -NotePropertyValue $legacyMode
        }
        if (!$server.PSObject.Properties['NPMNetwork']) { $server | Add-Member -NotePropertyName NPMNetwork -NotePropertyValue '' }
        Assert-Config $server
    }

    $id = (Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
    $bundlePath = Join-Path $stateDir "mail-lite-$id.tar.gz"
    $helperPath = Join-Path $stateDir "mail-lite-$id.sh"
    $listPath = Join-Path $stateDir "mail-lite-$id.files"
    $files = @(Get-DeploymentFiles | Sort-Object)
    if ($files.Count -eq 0) { throw 'No application source files found to package.' }
    # A NUL-separated, explicit file list avoids shell limits and safely handles
    # spaces/Unicode. No directories are passed to tar for implicit recursion.
    [System.IO.File]::WriteAllBytes($listPath, $utf8.GetBytes(($files -join "`0") + "`0"))
    $tarArgs = @('-czf', $bundlePath, '-C', $appRoot, '--no-recursion', '--null', '-T', $listPath)
    Write-Host 'Packaging the current source, including uncommitted changes...'
    try { Invoke-Checked 'tar.exe' $tarArgs }
    finally { Remove-Item -LiteralPath $listPath -Force -ErrorAction SilentlyContinue }
    if ($PackageOnly) {
        Write-Host "Package ready: $bundlePath"
        exit 0
    }
    $helper = [System.IO.File]::ReadAllText((Join-Path $PSScriptRoot 'server-deploy.sh')).Replace("`r`n", "`n")
    [System.IO.File]::WriteAllText($helperPath, $helper, $utf8)
    $target = "$($server.UserName)@$($server.HostName)"
    $connectionOptions = @('-o', 'ConnectTimeout=20', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=4')
    Write-Host "Uploading to $target..."
    Invoke-Checked 'scp.exe' (@('-P', "$($server.Port)") + $connectionOptions + @($bundlePath, $helperPath, "${target}:/tmp/"))
    $remoteBundle = '/tmp/' + (Split-Path -Leaf $bundlePath)
    $remoteHelper = '/tmp/' + (Split-Path -Leaf $helperPath)
    $remoteArgs = @($remoteBundle, $server.RemoteDir, $server.ProjectName, $server.HostName,
        $server.Domain, $server.EnableMX.ToString().ToLowerInvariant(), $server.MXHostName, $Action.ToLowerInvariant(),
        $server.ProxyMode, $server.NPMNetwork)
    # SSH passes a command string to the remote shell. Quote every argument there.
    $remoteCommand = 'bash ' + (Quote-Sh $remoteHelper) + ' ' + (($remoteArgs | ForEach-Object { Quote-Sh "$_" }) -join ' ')
    $remoteCommand += '; mail_deploy_status=$?; rm -f -- ' + (Quote-Sh $remoteHelper) + '; exit "$mail_deploy_status"'
    Write-Host 'Building and starting the server. The first build can take several minutes...'
    Invoke-Checked 'ssh.exe' (@('-T', '-p', "$($server.Port)") + $connectionOptions + @($target, $remoteCommand))
    Remove-Item -LiteralPath $bundlePath, $helperPath -Force
    Write-Host 'Done. Connection settings are saved for the next one-click update.'
} catch {
    Write-Host "Deployment failed: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host 'The package and connection settings, if created, remain in mail-lite/.deploy for retry.'
    exit 1
}
