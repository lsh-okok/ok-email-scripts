# Test the real Windows entry script with local SSH/SCP functions; no network calls.
$ErrorActionPreference = 'Stop'
$scriptsDir = Split-Path -Parent $PSScriptRoot
$testRoot = Join-Path ([System.IO.Path]::GetTempPath()) ('mail-lite-deploy-client-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path "$testRoot/scripts", "$testRoot/.deploy" -Force | Out-Null
Copy-Item -LiteralPath "$scriptsDir/deploy.ps1", "$scriptsDir/server-deploy.sh" -Destination "$testRoot/scripts"

function Write-FixtureFile([string]$Relative, [string]$Content) {
    $path = Join-Path $testRoot $Relative
    New-Item -ItemType Directory -Path (Split-Path -Parent $path) -Force | Out-Null
    [System.IO.File]::WriteAllText($path, $Content, (New-Object System.Text.UTF8Encoding($false)))
}

$privateFiles = @(
    '.env', '.env.replica', '.env.production', '.env.local', '.env.staging.local',
    'accounts.txt', 'ACCOUNTS.TXT', 'github.txt', 'stock.csv', 'credentials.json',
    'private.pem', 'id_rsa', 'id_ed25519', 'data/data.key', 'bin/mail-lite.exe',
    '.cache/private.go', 'imports/accounts.go', 'exports/mail.json',
    'web/ui/.env', 'web/ui/.env.production', 'web/ui/.npmrc',
    'web/ui/node_modules/private.js', 'web/ui/dist/private.js',
    'web/ui/src/accounts.txt', 'web/ui/src/accounts.csv', 'web/ui/src/private.pem',
    'deploy/private.key', 'deploy/private.pfx', 'deploy/private.p12', 'deploy/private.ppk',
    'deploy/id_rsa', 'deploy/LICENSE.key', 'internal/core/fixture.db',
    'internal/core/.cache/private.go', 'scripts/private.log')
foreach ($relative in $privateFiles) { Write-FixtureFile $relative 'MAIL_LITE_PRIVATE_FIXTURE=must-not-upload' }
$includedFiles = @(
    '.env.example', '.dockerignore', 'Dockerfile', 'compose.yml', 'go.mod', 'go.sum', 'README.md',
    'requirements.txt', 'requirements-dev.txt', 'CMakeLists.txt', 'LICENSE.txt',
    'scripts/deploy.ps1', 'scripts/server-deploy.sh', 'cmd/mail-lite/main.go',
    'internal/core/new-untracked.go', 'web/embed.go', 'web/dist/index.html',
    'web/templates/mailbox.html', 'web/ui/.env.example', 'web/ui/package.json',
    'web/ui/package-lock.json', 'web/ui/src/new component.ts', 'deploy/Caddyfile',
    'docs/build.md', 'examples/requirements-build.txt')
foreach ($relative in $includedFiles) {
    if ($relative -notin @('scripts/deploy.ps1', 'scripts/server-deploy.sh')) {
        Write-FixtureFile $relative ('BUILD_FIXTURE=' + $relative)
    }
}
# A junction inside a source directory must never expose its external target.
New-Item -ItemType Junction -Path "$testRoot/web/ui/src/linked-private" -Target "$testRoot/.cache" | Out-Null
$privateFiles += 'web/ui/src/linked-private/private.go'
$global:mailDeployPrivateFiles = $privateFiles
$global:mailDeployIncludedFiles = $includedFiles

function Assert-DeploymentArchive([string]$Bundle) {
    $entries = @(& tar.exe -tzf $Bundle)
    if ($LASTEXITCODE -ne 0) { throw 'Invalid source archive.' }
    foreach ($relative in $global:mailDeployPrivateFiles) {
        if ($entries -contains "./$relative") { throw "Private file was included: $relative" }
    }
    foreach ($relative in $global:mailDeployIncludedFiles) {
        if ($entries -notcontains "./$relative") { throw "Build/source file was omitted: $relative" }
    }
    $content = (& tar.exe -xOf $Bundle) -join "`n"
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read source archive.' }
    if ($content.Contains('MAIL_LITE_PRIVATE_FIXTURE')) { throw 'Private fixture content leaked into the archive.' }
    $source = (& tar.exe -xOf $Bundle './internal/core/new-untracked.go') -join "`n"
    if ($LASTEXITCODE -ne 0 -or $source -ne 'BUILD_FIXTURE=internal/core/new-untracked.go') {
        throw 'Current uncommitted source content was not preserved.'
    }
}
$settings = @{
    HostName = '192.0.2.1'; UserName = 'root'; Port = 22; RemoteDir = '/opt/mail-lite'
    ProjectName = 'mail-lite'; Domain = ''; EnableMX = $false; MXHostName = ''
}
$settings | ConvertTo-Json | Set-Content -LiteralPath "$testRoot/.deploy/server.json" -Encoding ASCII
$global:mailDeployScpCalled = $false
$global:mailDeploySshCalled = $false
$global:mailDeployExpectedArgs = "'' 'false' '' 'update' 'direct' ''"

function global:scp.exe {
    if ($args[-1] -ne 'root@192.0.2.1:/tmp/') { throw 'Unexpected SCP destination.' }
    $bundle = $args[-3]
    $helper = $args[-2]
    Assert-DeploymentArchive $bundle
    if ([IO.File]::ReadAllText($helper).Contains("`r")) { throw 'Remote helper contains CRLF.' }
    $global:mailDeployScpCalled = $true
    $global:LASTEXITCODE = 0
}

function global:ssh.exe {
    if (!$global:mailDeployScpCalled) { throw 'SSH ran before upload.' }
    if ($args[-2] -ne 'root@192.0.2.1') { throw 'Unexpected SSH destination.' }
    $command = $args[-1]
    if (!$command.Contains($global:mailDeployExpectedArgs)) { throw 'Remote arguments were lost or reordered.' }
    if (!$command.Contains('mail_deploy_status=$?') -or !$command.Contains('exit "$mail_deploy_status"')) { throw 'SSH does not preserve remote deployment errors.' }
    $global:mailDeploySshCalled = $true
    $global:LASTEXITCODE = 0
}

& "$testRoot/scripts/deploy.ps1" -Action Update
if (!$global:mailDeployScpCalled -or !$global:mailDeploySshCalled) { throw 'The update entry did not upload and invoke SSH.' }
if (@(Get-ChildItem -LiteralPath "$testRoot/.deploy" -Filter '*.tar.gz').Count -ne 0) { throw 'Successful upload did not remove its local package.' }
Write-Host 'PASS: Windows update preserves current source/examples/build text, excludes private files/junctions, uploads before SSH and preserves remote arguments/status.'
$settings.Domain = 'mail.example.com'
$settings.ProxyMode = 'npm'
$settings.NPMNetwork = ''
$settings | ConvertTo-Json | Set-Content -LiteralPath "$testRoot/.deploy/server.json" -Encoding ASCII
$global:mailDeployExpectedArgs = "'mail.example.com' 'false' '' 'update' 'npm' ''"
$global:mailDeployScpCalled = $false
$global:mailDeploySshCalled = $false
& "$testRoot/scripts/deploy.ps1" -Action Update
if (!$global:mailDeployScpCalled -or !$global:mailDeploySshCalled) { throw 'The NPM update entry did not run.' }
Write-Host 'PASS: Windows NPM update transmits proxy selection and an empty network for automatic detection.'
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$testRoot/scripts/deploy.ps1" -PackageOnly
if ($LASTEXITCODE -ne 0) { throw 'PackageOnly failed.' }
$bundles = @(Get-ChildItem -LiteralPath "$testRoot/.deploy" -Filter '*.tar.gz')
if ($bundles.Count -ne 1) { throw 'PackageOnly did not leave exactly one package.' }
Assert-DeploymentArchive $bundles[0].FullName
if (@(Get-ChildItem -LiteralPath "$testRoot/.deploy" -Filter '*.files').Count -ne 0) {
    throw 'Packaging left its temporary file manifest behind.'
}
Write-Host 'PASS: PackageOnly applies the same privacy rules and leaves a reviewable package without SSH/SCP.'
Write-Host "Test fixtures: $testRoot"
