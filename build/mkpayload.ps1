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

  -ProgramOnly assembles the same tree without any slot: exe, dsh-ab.toml, README/LICENSES/.gitignore,
  docs\ and runtime\git\ - no node, no dsh tree, no bundled skill, and therefore no dsh version to
  resolve at all. That is nearly all of what the in-place update pack carries: build\mkupdate.ps1 takes
  these program files and leaves the docs\ ledgers out, so the CI update-pack job builds the payload in
  this mode and never touches npm:
  a dsh release that has since disappeared from the registry can no longer stop a new DSH-AB version
  from getting its update pack.

  Inputs are only ever READ; the only writes are the payload, the cache and the pins.
#>
[CmdletBinding()]
param(
  # The dsh version this payload carries; the upstream tag is dsh-v<version>.
  # -ProgramOnly 不带 dsh，所以那时可以不传（build.ps1 仍然会传：随包的 exe 里编着它）。
  [string]$DshVersion,
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
  [switch]$Offline,
  # 只装程序文件：不装任何槽（node 运行时、dsh 树、随包 skill 都不在里面），因此也不需要 dsh 版本。
  # 就地更新包要的正是这些文件（见文件头的说明）。
  [switch]$ProgramOnly
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
$DshVersion = "$DshVersion".Trim()
if (-not $DshVersion -and -not $ProgramOnly) {
  throw '-DshVersion 是必填的（只有 -ProgramOnly 那种不带 dsh 的程序文件载荷可以不传）'
}

# ---- 1. node ---------------------------------------------------------------------------------
if ($ProgramOnly) {
  Write-Host '[1/5] Node 运行时（跳过：-ProgramOnly 只装程序文件，不带槽）'
} else {
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
}

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
if ($ProgramOnly) {
  Write-Host '[3/5] slot-a\node（跳过：同上）'
} else {
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
}

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
if ($ProgramOnly) {
  Write-Host '[5/5] slot-a\app（跳过：同上）'
} else {
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

  # npm 解析的是「此刻」的 registry，而 @deepseek-ai/dsh@<ver> 用 ^ 引用与它同一批发布的兄弟包：
  # 于是 dsh 停在请求的那个版本，兄弟包却全是当时最新的那一代。这个错位会让 npm 把整族兄弟塞进
  # node_modules\@deepseek-ai\dsh\node_modules\ 里——树是合法的，但载荷内的相对路径会涨到 280 字符，
  # Windows 与 ISCC 都建不出来（实测 0.1.5-alpha.2；结尾的路径门禁就是为它设的）。
  # --before 把解析放回这个版本自己的那一代。取值：它发布之后一小时，但不越过它与下一版的中点，
  # 两个边界都不踩（贴着边界时 npm 会先解析到边界那一版再把它剪掉，然后报 ETARGET——实测过）。
  # 拿不到发布时间（上游建了 Release 却没发 npm）就返回 $null，让 npm install 自己报错。
  function Get-DshBefore([string]$Version, [string]$Npm, [string]$Registry) {
    $raw = & $Npm view '@deepseek-ai/dsh' time --json "--registry=$Registry"
    if ($LASTEXITCODE -ne 0) { throw "读不到 @deepseek-ai/dsh 的发布时间表（npm view 退出码 $LASTEXITCODE）" }
    $list = @((($raw | Out-String) | ConvertFrom-Json).PSObject.Properties |
              Where-Object { $_.Name -match '^\d+\.\d+\.\d+' } |
              ForEach-Object { [pscustomobject]@{ V = $_.Name; T = ([datetime]$_.Value).ToUniversalTime() } } |
              Sort-Object T)
    $i = -1
    for ($k = 0; $k -lt $list.Count; $k++) { if ($list[$k].V -eq $Version) { $i = $k; break } }
    if ($i -lt 0) { return $null }
    $end = $list[$i].T.AddHours(1)
    if ($i + 1 -lt $list.Count) {
      $mid = $list[$i].T.AddHours((($list[$i + 1].T - $list[$i].T).TotalHours) / 2)
      if ($mid -lt $end) { $end = $mid }
    }
    return $end.ToString('yyyy-MM-ddTHH:mm:ssZ')
  }

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
    $before = Get-DshBefore $DshVersion $npm $Registry
    if ($before) { $npmArgs += "--before=$before" }
    Write-Host "      npm install（首次：解析 $DshVersion 的依赖树并锁定；--before=$before）"
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
}

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

# ---- 路径深度门禁 ------------------------------------------------------------------------------
# 载荷里的相对部分会被原样装到用户机器上（常见安装根约 40 字符），CI 还要在 38 字符的临时目录下
# 验证一遍；Windows 与 ISCC 都建不出超过 MAX_PATH（260）的路径，所以相对部分必须先卡住：
# 40 + 180 = 220，两种安装根都够，同时给未来的版本留了余量。
# 这条断言的理由就是它真踩过：0.1.5-alpha.2 的载荷里曾有一条 280 字符的相对路径，ISCC 压到那一条
# 时以「系统找不到指定的路径」中止——接短源路径前缀（subst 换盘符）也救不了，因为超的是相对部分。
$MaxRelPath = 180
$outFull = (Get-Item -LiteralPath $OutDir).FullName
$worst = Get-ChildItem -LiteralPath $outFull -Recurse -File -Force |
         ForEach-Object { [pscustomobject]@{ Len = $_.FullName.Length - $outFull.Length - 1; Rel = $_.FullName.Substring($outFull.Length + 1) } } |
         Sort-Object Len -Descending | Select-Object -First 1
Write-Host "      载荷内最长相对路径 $($worst.Len) 字符（上限 $MaxRelPath）：$($worst.Rel)"
if ($worst.Len -gt $MaxRelPath) {
  throw "载荷内有 $($worst.Len) 字符的相对路径，超过上限 $MaxRelPath（装到安装根后必然超过 MAX_PATH）：$($worst.Rel)"
}

# ---- manifest --------------------------------------------------------------------------------
$dshabVersion = '0.0.0'
$versionFile = Join-Path $RepoRoot 'src\dsh-ab\VERSION'
if (Test-Path -LiteralPath $versionFile) { $dshabVersion = (Get-Content -LiteralPath $versionFile -Raw).Trim() }
# 「载荷里该有哪些文件」＝「安装根里该有哪些文件」，这条规则只有一份实现（install-layout.ps1），
# 这里把它**落成构建期记录**写进清单：verify-silent 拿它当期望集合，不再拿活 payload 目录去比安装根。
# 这是 2026-09-23 那条门禁盲区：两侧都读当前载荷时，只要在编安装包**之前**动过载荷（build.ps1
# -SkipPayload 那条路，或者手工删了再编），载荷与安装根就一起少，比对无差异、门禁照样全绿。
# 记录是构建期的产物，之后删掉的这一个文件就只在安装根里缺，门禁必红。
# 记整份清单而不是一个哈希：报错时要说得出是哪个文件。这个门禁存在的理由就是那次静默丢了 45 个
# 文件，「少了 45 个」而不说是哪 45 个，等于让人再查一遍。清单只进这份构建记录：安装包与更新包
# 都不带它（install-layout.ps1 里「payload-manifest.json 从不安装」那条规则把它排除在外）。
. (Join-Path $PSScriptRoot 'install-layout.ps1')
# Only what a reader actually checks (build\verify-silent.ps1): the pair of versions that names
# the artifact, the hash of the exe that went into the payload, and the file list that became the
# installation. Anything else recorded here -
# build time, registry, node/git inputs, upstream tag, lockfile - had no reader.
$manifest = [ordered]@{
  dshab_version = $dshabVersion
  # -ProgramOnly 的载荷里没有 dsh（也就没有版本可说），照实留空，不编一个出来。
  dsh_version   = if ($ProgramOnly) { '' } else { $DshVersion }
  exe_sha256    = (Get-Sha256 (Join-Path $OutDir 'DSH_AB.exe'))
  # 上面那条注释说的规则算出来的清单；payload-manifest.json 自己不在里面（它从不安装）。
  install_files = @(Get-InstallRootFiles $OutDir)
}
$manifest | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $OutDir 'payload-manifest.json') -Encoding UTF8

# ---- reproducible artifacts ----------------------------------------------------------------------
# ISCC and ZipFile.CreateFromDirectory both write the source files' timestamps into what they produce,
# and this script refreshes every payload file's mtime on each run (re-unpacking the zips, a fresh npm
# ci, the plain copies above), so the same commit produced byte-different artifacts: measured, touching
# only payload\README.md's mtime - same content - moved the installer's sha256. One fixed instant for
# every file in the payload makes the payload, and therefore the installer and the update pack, a pure
# function of this build's inputs. 2000-01-01T00:00:00Z: after 1980 (the DOS epoch every zip stores),
# before any real build, and nowhere near "now". The update pack needs no gate of its own: mkupdate.ps1
# stages the payload with Copy-Item, which preserves what is set here.
$PayloadTime = [datetime]::new(2000, 1, 1, 0, 0, 0, [DateTimeKind]::Utc)
Get-ChildItem -LiteralPath $OutDir -Recurse -File -Force | ForEach-Object { $_.LastWriteTimeUtc = $PayloadTime }
Get-ChildItem -LiteralPath $OutDir -Recurse -Directory -Force | ForEach-Object { $_.LastWriteTimeUtc = $PayloadTime }
(Get-Item -LiteralPath $OutDir).LastWriteTimeUtc = $PayloadTime
Write-Host "      载荷时间戳归一到 $($PayloadTime.ToString('yyyy-MM-ddTHH:mm:ssZ'))（安装包与更新包因此可复现）"

$mb = [math]::Round((Get-ChildItem $OutDir -Recurse -File -Force | Measure-Object Length -Sum).Sum / 1MB, 1)
Write-Host ""
if ($ProgramOnly) { Write-Host "payload ready: $OutDir ($mb MB)（只装程序文件，不带 dsh）" }
else { Write-Host "payload ready: $OutDir ($mb MB) dsh $DshVersion" }
Get-ChildItem $OutDir -Force | Select-Object Name, @{n = 'MB'; e = { if ($_.PSIsContainer) { [math]::Round((Get-ChildItem $_.FullName -Recurse -File -Force | Measure-Object Length -Sum).Sum / 1MB, 1) } else { [math]::Round($_.Length / 1MB, 2) } } } | Format-Table -AutoSize | Out-String -Width 120 | Write-Host
