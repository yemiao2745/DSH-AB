<#
  mkpayload.ps1 - assemble the installer payload (a build product; never committed).

  Nothing here comes from a local DSH installation any more. Every
  input is fetched for the requested versions, checked against a recorded hash and
  cached inside the workspace, so the same build repeats offline.

    Node        official nodejs.org zip, checked against the official SHASUMS256.txt
    portable Git GitHub release of git-for-windows (MinGit zip, no self-extracting exe)
    dsh tree    the published npm tree of @deepseek-ai/dsh@<version>; every package is
                pinned by the registry's own integrity hash and by a lockfile

  The payload mirrors the final installation layout, so installer\dsh-ab.iss can copy
  it 1:1:

    payload\DSH_AB.exe  dsh-ab.toml  README.md  LICENSES.txt  .gitignore
    payload\docs\{PLUGINS.md,TODO.md}   (ledger seeds; still carrying the {{DSH_AB_ROOT}} placeholder)
    payload\slot-a\{node,app,data}
    payload\runtime\git\
    payload\payload-manifest.json     (build record; not installed)

  Inputs are only ever READ; the only writes are the payload, the cache and the pins.
#>
[CmdletBinding()]
param(
  # The dsh version this payload carries; the upstream tag is dsh-v<version>.
  [Parameter(Mandatory = $true)][string]$DshVersion,
  # Node comes from the official distribution and is checked against its own SHASUMS256.txt.
  [string]$NodeVersion = '24.18.0',
  # MinGit version as published by the git-for-windows GitHub release (tag v<3 parts>.windows.<4th>).
  [string]$GitVersion  = '2.55.0.5',
  # Only used on the first install of a version; later builds resolve from the lockfile.
  [string]$Registry    = 'https://registry.npmjs.org/',
  [string]$CacheDir,
  [string]$OutDir,
  [string]$AppExe,
  [string]$Templates,
  [string]$PinFile,
  [switch]$Offline
)
$ErrorActionPreference = 'Stop'

$RepoRoot = Split-Path -Parent $PSScriptRoot
if (-not $CacheDir)  { $CacheDir  = Join-Path $RepoRoot 'cache' }
if (-not $OutDir)    { $OutDir    = Join-Path $RepoRoot 'payload' }
if (-not $AppExe)    { $AppExe    = Join-Path $PSScriptRoot 'DSH_AB.exe' }
if (-not $Templates) { $Templates = Join-Path $PSScriptRoot 'templates' }
if (-not $PinFile)   { $PinFile   = Join-Path $PSScriptRoot 'payload-sources.json' }

$NodeDir  = Join-Path $CacheDir 'node'
$GitDir   = Join-Path $CacheDir 'git'
$AppLocks = Join-Path $CacheDir 'app-locks'
$NpmCache = Join-Path $CacheDir 'npm'
foreach ($d in @($NodeDir, $GitDir, $AppLocks, $NpmCache)) {
  New-Item -ItemType Directory -Force -Path $d | Out-Null
}

function Assert-Path([string]$Path, [string]$What) {
  if (-not (Test-Path -LiteralPath $Path)) { throw "$What not found: $Path" }
}

function Get-Sha256([string]$Path) {
  (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

# This network drops TLS connections now and then, so every download is retried a few times
# before it is allowed to fail. Only a file that passed its hash check is ever moved on.
function Invoke-Retry([scriptblock]$Action, [string]$What, [int]$Tries = 5) {
  for ($i = 1; $i -le $Tries; $i++) {
    try { return & $Action } catch {
      if ($i -eq $Tries) { throw }
      Write-Host "      $What 第 $i 次失败：$($_.Exception.Message)"
      Start-Sleep -Seconds (5 * $i)
    }
  }
}

# ---- hash pins -------------------------------------------------------------------------------
# Node publishes SHASUMS256.txt, so its zip is checked against the publisher. git-for-windows
# publishes no checksum for its release assets, so that one is pinned here on first download and
# every later build must match the pin - a replaced asset cannot slip through unnoticed.
function Read-Pins {
  if (Test-Path -LiteralPath $PinFile) {
    return (Get-Content -LiteralPath $PinFile -Raw | ConvertFrom-Json)
  }
  return [pscustomobject]@{ node = [pscustomobject]@{}; mingit = [pscustomobject]@{} }
}

function Get-Pin($Pins, [string]$Kind, [string]$Key) {
  if ($Pins.$Kind -and ($Pins.$Kind.PSObject.Properties.Name -contains $Key)) { return $Pins.$Kind.$Key }
  return $null
}

function Set-Pin([string]$Kind, [string]$Key, [string]$Url, [string]$Sha256) {
  $Pins = Read-Pins
  if (-not $Pins.$Kind) { $Pins | Add-Member -NotePropertyName $Kind -NotePropertyValue ([pscustomobject]@{}) }
  if ($Pins.$Kind.PSObject.Properties.Name -contains $Key) {
    $Pins.$Kind.$Key.url = $Url
    $Pins.$Kind.$Key.sha256 = $Sha256
  } else {
    $Pins.$Kind | Add-Member -NotePropertyName $Key -NotePropertyValue ([pscustomobject]@{ url = $Url; sha256 = $Sha256 })
  }
  $Pins | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $PinFile -Encoding UTF8
}

# Fetch returns a verified local file: reused when its hash matches the pin, downloaded otherwise.
function Fetch([string]$Url, [string]$Target, [string]$PinnedSha, [string]$What) {
  if (Test-Path -LiteralPath $Target) {
    $have = Get-Sha256 $Target
    if ($have -eq $PinnedSha) { Write-Host "      cached $What ($have)"; return $Target }
    Write-Host "      缓存的 $What 哈希不符（期望 $PinnedSha，实际 $have），重新下载"
    Remove-Item -LiteralPath $Target -Force
  }
  if ($Offline) { throw "-Offline 已指定，但缓存里没有可用的 $What：$Target" }
  Write-Host "      downloading $What ..."
  $tmp = "$Target.part"
  if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force }
  Invoke-Retry { Invoke-WebRequest -Uri $Url -OutFile $tmp -UseBasicParsing -TimeoutSec 1800 } "$What 下载"
  $got = Get-Sha256 $tmp
  if ($got -ne $PinnedSha) {
    Remove-Item -LiteralPath $tmp -Force
    throw "$What 哈希不符：期望 $PinnedSha，实际 $got（$Url）"
  }
  Move-Item -LiteralPath $tmp -Destination $Target -Force
  Write-Host "      verified $What ($got)"
  return $Target
}

function Expand-Zip([string]$Zip, [string]$Dest) {
  if (Test-Path -LiteralPath $Dest) { Remove-Item -LiteralPath $Dest -Recurse -Force }
  New-Item -ItemType Directory -Force -Path $Dest | Out-Null
  Expand-Archive -LiteralPath $Zip -DestinationPath $Dest -Force
}

Assert-Path $AppExe 'DSH_AB.exe'
Assert-Path (Join-Path $Templates 'dsh-ab.toml') 'templates'

# ---- 1. node ---------------------------------------------------------------------------------
Write-Host '[1/5] Node 运行时'
$nodeZip = Join-Path $NodeDir "node-v$NodeVersion-win-x64.zip"
$nodeUrl = "https://nodejs.org/dist/v$NodeVersion/node-v$NodeVersion-win-x64.zip"
if (-not $Offline) {
  # The authoritative hash comes from the publisher, not from us: it replaces any local pin.
  $shasums = Invoke-Retry { (Invoke-WebRequest -Uri "https://nodejs.org/dist/v$NodeVersion/SHASUMS256.txt" -UseBasicParsing -TimeoutSec 300).Content } 'SHASUMS256.txt'
  $want = "node-v$NodeVersion-win-x64.zip"
  $line = ($shasums -split '\r?\n') | Where-Object { $_ -match "\s$([regex]::Escape($want))$" } | Select-Object -First 1
  if (-not $line) { throw "SHASUMS256.txt 里没有 $want" }
  $official = ($line -split '\s+')[0].ToLowerInvariant()
  $nodePin = Get-Pin (Read-Pins) 'node' $NodeVersion
  if ($nodePin -and $nodePin.sha256 -ne $official) {
    throw "node $NodeVersion 的官方哈希变了：pin $($nodePin.sha256)，SHASUMS256 $official"
  }
  if (-not $nodePin) { Set-Pin 'node' $NodeVersion $nodeUrl $official }
}
$nodePin = Get-Pin (Read-Pins) 'node' $NodeVersion
if (-not $nodePin) { throw "没有 node $NodeVersion 的哈希 pin（$PinFile）：离线时无法校验" }
Fetch $nodeUrl $nodeZip $nodePin.sha256 "node $NodeVersion" | Out-Null

# ---- 2. portable git -------------------------------------------------------------------------
Write-Host '[2/5] 便携 Git（GitHub release 的 MinGit）'
$gitTag = 'v' + ($GitVersion -replace '^(\d+\.\d+\.\d+)\.(\d+)$', '$1.windows.$2')
$gitAsset = "MinGit-$GitVersion-64-bit.zip"
$gitUrl = "https://github.com/git-for-windows/git/releases/download/$gitTag/$gitAsset"
$gitZip = Join-Path $GitDir $gitAsset
$gitPin = Get-Pin (Read-Pins) 'mingit' $GitVersion
if (-not $gitPin) {
  if ($Offline) { throw "-Offline 已指定，但 $gitAsset 还没有 pin（$PinFile）" }
  Write-Host "      $gitAsset 上游没有发布校验和，先下载一次并把哈希记进 $PinFile"
  if (Test-Path -LiteralPath $gitZip) { Remove-Item -LiteralPath $gitZip -Force }
  Invoke-Retry { Invoke-WebRequest -Uri $gitUrl -OutFile $gitZip -UseBasicParsing -TimeoutSec 1800 } "$gitAsset 下载"
  $sha = Get-Sha256 $gitZip
  Set-Pin 'mingit' $GitVersion $gitUrl $sha
  Write-Host "      已记录 pin：$sha（请复核后提交 $PinFile）"
} else {
  Fetch $gitUrl $gitZip $gitPin.sha256 "MinGit $GitVersion" | Out-Null
}

if (Test-Path -LiteralPath $OutDir) { Remove-Item -LiteralPath $OutDir -Recurse -Force }

# ---- 3. slot-a\node --------------------------------------------------------------------------
Write-Host '[3/5] slot-a\node'
$slot = Join-Path $OutDir 'slot-a'
New-Item -ItemType Directory -Force -Path $slot | Out-Null
$nodeTmp = Join-Path $CacheDir 'node-extract'
Expand-Zip $nodeZip $nodeTmp
$nodeInner = Join-Path $nodeTmp "node-v$NodeVersion-win-x64"
Assert-Path (Join-Path $nodeInner 'node.exe') '解压后的 node.exe'
Move-Item -LiteralPath $nodeInner -Destination (Join-Path $slot 'node')
$gotNode = (& (Join-Path $slot 'node\node.exe') --version).TrimStart('v')
if ($gotNode -ne $NodeVersion) { throw "slot-a 里的 node 是 $gotNode，期望 $NodeVersion" }

# ---- 4. runtime\git --------------------------------------------------------------------------
Write-Host '[4/5] runtime\git'
$gitTmp = Join-Path $CacheDir 'git-extract'
Expand-Zip $gitZip $gitTmp
$gitExe = Join-Path $gitTmp 'cmd\git.exe'
Assert-Path $gitExe '解压后的 cmd\git.exe'
# git reports the release it was cut from, e.g. "2.55.0.windows.5" for tag v2.55.0.windows.5:
# this is the check that the extracted binary really is the release we downloaded.
$gotGit = (& $gitExe --version) -replace '^git version\s+', ''
$expectedGit = $gitTag.TrimStart('v')
if ($gotGit -ne $expectedGit) { throw "MinGit 报的是 $gotGit，期望 $expectedGit（release $gitTag）" }
New-Item -ItemType Directory -Force -Path (Join-Path $OutDir 'runtime') | Out-Null
Move-Item -LiteralPath $gitTmp -Destination (Join-Path $OutDir 'runtime\git')

# ---- 5. slot-a\app: the published dsh tree ---------------------------------------------------
Write-Host '[5/5] slot-a\app（dsh 依赖树）'
$app = Join-Path $slot 'app'
New-Item -ItemType Directory -Force -Path $app | Out-Null
$npm = Join-Path $slot 'node\npm.cmd'
Assert-Path $npm 'node 自带的 npm'
# Exact version, no range: the payload carries one and only one dsh.
$pkgJson = [ordered]@{
  name         = 'app'
  version      = '1.0.0'
  private      = $true
  dependencies = [ordered]@{ '@deepseek-ai/dsh' = $DshVersion }
}
$pkgJson | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $app 'package.json') -Encoding UTF8

$lockPath = Join-Path $AppLocks "app-$DshVersion.json"
$npmArgs = @('--no-audit', '--no-fund', '--loglevel=error', "--cache=$NpmCache")
if (Test-Path -LiteralPath $lockPath) {
  Copy-Item -LiteralPath $lockPath -Destination (Join-Path $app 'package-lock.json')
  if ($Offline) { $npmArgs += '--offline' }
  Write-Host "      npm ci（依赖树由 $lockPath 锁定）"
  Push-Location $app
  try { & $npm ci @npmArgs } finally { Pop-Location }
  if ($LASTEXITCODE -ne 0) { throw "npm ci 失败，退出码 $LASTEXITCODE（$lockPath）" }
} else {
  if ($Offline) { throw "-Offline 已指定，但 $DshVersion 还没有锁文件：$lockPath" }
  $npmArgs += "--registry=$Registry"
  $npmArgs += '--prefer-offline'
  Write-Host "      npm install（首次：解析 $DshVersion 的依赖树并锁定）"
  Push-Location $app
  try { & $npm install @npmArgs } finally { Pop-Location }
  if ($LASTEXITCODE -ne 0) { throw "npm install 失败，退出码 $LASTEXITCODE" }
  Copy-Item -LiteralPath (Join-Path $app 'package-lock.json') -Destination $lockPath -Force
}
$dshPkg = Join-Path $app 'node_modules\@deepseek-ai\dsh\package.json'
Assert-Path $dshPkg '依赖树里的 @deepseek-ai/dsh'
$gotDsh = (Get-Content -LiteralPath $dshPkg -Raw | ConvertFrom-Json).version
if ($gotDsh -ne $DshVersion) { throw "依赖树里的 dsh 是 $gotDsh，期望 $DshVersion" }
Assert-Path (Join-Path $app 'node_modules\@deepseek-ai\dsh\lib\bin.js') 'dsh 入口 lib\bin.js'

# A fresh slot starts with an empty DSH_HOME that carries one thing: the AI instructions as a dsh
# user-level skill. dsh scans
# <DSH_HOME>\skills\<name>\SKILL.md and loads it only when the task matches its description -
# unlike AGENTS.md, which dsh injected into every session no matter the working directory.
$skillSrc = Join-Path $Templates 'skills\dsh-self-maintenance'
$skillDst = Join-Path $slot 'data\skills\dsh-self-maintenance'
New-Item -ItemType Directory -Force -Path $skillDst | Out-Null
Copy-Item -LiteralPath (Join-Path $skillSrc 'SKILL.md') -Destination (Join-Path $skillDst 'SKILL.md')

Write-Host 'install root files ...'
Copy-Item -LiteralPath $AppExe -Destination (Join-Path $OutDir 'DSH_AB.exe')
foreach ($f in @('dsh-ab.toml', 'README.md', 'LICENSES.txt')) {
  Copy-Item -LiteralPath (Join-Path $Templates $f) -Destination (Join-Path $OutDir $f)
}
Copy-Item -LiteralPath (Join-Path $Templates 'gitignore.template') -Destination (Join-Path $OutDir '.gitignore')
# The two ledgers the skill requires: seeds, still carrying the placeholder the installer
# bakes into the real installation path.
New-Item -ItemType Directory -Force -Path (Join-Path $OutDir 'docs') | Out-Null
foreach ($f in @('PLUGINS.md', 'TODO.md')) {
  Copy-Item -LiteralPath (Join-Path $Templates "docs\$f") -Destination (Join-Path $OutDir "docs\$f")
}

# ---- manifest --------------------------------------------------------------------------------
$dshabVersion = '0.0.0'
$versionFile = Join-Path $RepoRoot 'src\dsh-ab\VERSION'
if (Test-Path -LiteralPath $versionFile) { $dshabVersion = (Get-Content -LiteralPath $versionFile -Raw).Trim() }
# Only what a reader actually checks (build\verify-silent.ps1): the pair of versions that names
# the artifact and the hash of the exe that went into the payload. Anything else recorded here -
# build time, registry, node/git inputs, upstream tag, lockfile - had no reader.
$manifest = [ordered]@{
  dshab_version = $dshabVersion
  dsh_version   = $DshVersion
  exe_sha256    = (Get-Sha256 (Join-Path $OutDir 'DSH_AB.exe'))
}
$manifest | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $OutDir 'payload-manifest.json') -Encoding UTF8

$mb = [math]::Round((Get-ChildItem $OutDir -Recurse -File -Force | Measure-Object Length -Sum).Sum / 1MB, 1)
Write-Host ""
Write-Host "payload ready: $OutDir ($mb MB) dsh $DshVersion"
Get-ChildItem $OutDir -Force | Select-Object Name, @{n = 'MB'; e = { if ($_.PSIsContainer) { [math]::Round((Get-ChildItem $_.FullName -Recurse -File -Force | Measure-Object Length -Sum).Sum / 1MB, 1) } else { [math]::Round($_.Length / 1MB, 2) } } } | Format-Table -AutoSize | Out-String -Width 120 | Write-Host
