<#
  build.ps1 - build DSH_AB.exe and the DSH-AB installer for one dsh version.

  Steps: 1) icon/manifest resource
         2) Go build, GUI subsystem, both version numbers injected with -ldflags
         3) payload assembly (build\mkpayload.ps1: fetched from the network, verified, cached)
         4) Inno Setup compile -> dist\<product>-<dshab version>-<dsh version>-setup.exe
         5) in-place update pack -> dist\<product>-<dshab version>-update.zip (build\mkupdate.ps1).
            It packs the very same exe the installer carries (payload\DSH_AB.exe), so the same
            source is built exactly once per run. This step is decided by -SkipUpdatePack alone,
            never by -SkipInstaller: CI builds it in the standalone update-pack job (which skips
            the installer, and assembles only the program files: see -ProgramOnly) and every
            matrix cell passes -SkipUpdatePack.

  -ProgramOnly skips the slots: the payload is the program files alone (mkpayload.ps1 -ProgramOnly),
  which is exactly what step 5/5 packs and what CI's update-pack job builds - no node runtime, no
  dsh tree, and therefore no dsh version to resolve from the network at all.

  The dsh version is a build parameter, not a constant: one installer is produced per
  upstream rc. This program's own version comes from src\dsh-ab\VERSION.

  -TestProduct builds the very same source as a *separate product*, DSH-ABtest (see the
  parameter below): its own name, default installation directory, Start Menu entry, Add/Remove
  entry prefix and artifact name, all injected, so a test build installs next to the release
  without either one seeing the other as "another DSH-AB".

  Build-time tools; none of them is shipped inside the installer:
    Go           D:\dshab\tools\go\bin\go.exe
    Inno Setup   %LOCALAPPDATA%\Programs\Inno Setup 6\ISCC.exe
    rsrc         fetched once with 'go run', only when the .syso is absent
#>
[CmdletBinding()]
param(
  # The dsh this build carries (upstream tag dsh-v<version>), e.g. 0.1.5-rc.2.
  [Parameter(Mandatory = $true)][string]$DshVersion,
  # Defaults to the content of src\dsh-ab\VERSION.
  [string]$DshabVersion,
  [string]$NodeVersion = '24.18.0',
  [string]$GitVersion  = '2.55.0.5',
  [string]$Registry    = 'https://registry.npmjs.org/',
  [switch]$Offline,
  [switch]$SkipPayload,
  [switch]$SkipInstaller,
  # The in-place update pack is the same content for every dsh version, so one run needs
  # exactly one: CI builds it in the standalone update-pack job and every matrix cell
  # passes this, its job being the installer. This switch alone decides step 5/5 -
  # -SkipInstaller does not skip the update pack.
  [switch]$SkipUpdatePack,
  # 只组装程序文件载荷（build\mkpayload.ps1 -ProgramOnly）：exe、dsh-ab.toml、README/LICENSES/
  # .gitignore、docs\、runtime\git\，没有槽、没有 dsh 树，因此也不需要联网解析任何 dsh 版本。
  # The update pack takes these program files and leaves the docs\ ledgers out; CI's update-pack job
  # builds exactly this. -ProgramOnly cannot produce an installer (an installer must carry slot-a), so it
  # only goes with -SkipInstaller, and combining it with -SkipPayload means nothing.
  [switch]$ProgramOnly,
  # Build the test product DSH-ABtest instead of DSH-AB. One switch changes the product name the
  # installer carries, the default installation directory, the Start Menu entry, the Add/Remove
  # entry prefix, the Go appName constant (injected with -ldflags -X, never edited in the source)
  # and the artifact name. It implies -SkipPayload: the payload is byte-identical between the two
  # products (only that one injected string differs), so the test build must never rewrite
  # payload\ or build\DSH_AB.exe - a normal build has to be run once first.
  [switch]$TestProduct
)
$ErrorActionPreference = 'Stop'

$RepoRoot = Split-Path -Parent $PSScriptRoot
$Go       = Join-Path $RepoRoot 'tools\go\bin\go.exe'
$Iscc     = Join-Path $env:LOCALAPPDATA 'Programs\Inno Setup 6\ISCC.exe'
$SrcDir   = Join-Path $RepoRoot 'src\dsh-ab'
$Syso     = Join-Path $SrcDir 'rsrc_windows_amd64.syso'

# The product this run builds. Only the name differs; every per-product artifact is derived from it.
$ProductName = if ($TestProduct) { 'DSH-ABtest' } else { 'DSH-AB' }
$ExeName     = if ($TestProduct) { 'DSH_ABtest.exe' } else { 'DSH_AB.exe' }
$AppExe      = Join-Path $PSScriptRoot $ExeName

function Step([string]$Text) { Write-Host ""; Write-Host "=== $Text" -ForegroundColor Cyan }

# The one source of truth for this program's own version.
if (-not $DshabVersion) {
  $versionFile = Join-Path $SrcDir 'VERSION'
  if (-not (Test-Path -LiteralPath $versionFile)) { throw "找不到版本来源：$versionFile" }
  $DshabVersion = (Get-Content -LiteralPath $versionFile -Raw).Trim()
}
if (-not $DshabVersion) { throw 'src\dsh-ab\VERSION 是空的' }
$DshVersion = $DshVersion.Trim()
$DshVersion = $DshVersion -replace '^dsh-v', ''
Write-Host "dshab $DshabVersion / dsh $DshVersion / product $ProductName ($ExeName)"
if ($ProgramOnly -and -not $SkipInstaller) {
  throw '-ProgramOnly 的载荷里没有槽：拿它编不出能用的安装包，要装安装包就别用 -ProgramOnly'
}
if ($ProgramOnly -and $SkipPayload) {
  throw '-ProgramOnly 只改载荷那一步，与 -SkipPayload 一起用没有意义'
}
if ($TestProduct) {
  $SkipPayload = $true
  if (-not (Test-Path -LiteralPath (Join-Path $RepoRoot 'payload\dsh-ab.toml'))) {
    throw '测试构建复用正式构建的 payload，但 payload\ 不存在：先跑一次正式构建（不加 -TestProduct）'
  }
}

Step '1/5 icon and manifest resource'
# The .syso carries the icon Explorer and the Start menu show. Rebuild it whenever the
# icon or the manifest changed: a stale .syso is how a fixed icon stays broken inside the
# shipped exe while src\dsh-ab\assets\dsh.ico already looks right.
$Icon     = Join-Path $SrcDir 'assets\dsh.ico'
$Manifest = Join-Path $PSScriptRoot 'dsh-ab.exe.manifest'
$stale = $false
if (Test-Path $Syso) {
  $sysoTime = (Get-Item $Syso).LastWriteTimeUtc
  foreach ($src in @($Icon, $Manifest)) {
    if ((Get-Item $src).LastWriteTimeUtc -gt $sysoTime) { $stale = $true }
  }
}
if (-not (Test-Path $Syso) -or $stale) {
  if ($stale) { Write-Host 'icon or manifest is newer than the .syso, regenerating it ...' }
  else { Write-Host 'rsrc_windows_amd64.syso missing, regenerating it ...' }
  New-Item -ItemType Directory -Force -Path (Join-Path $RepoRoot '.tmp-src\rsrcgen') | Out-Null
  Push-Location (Join-Path $RepoRoot '.tmp-src\rsrcgen')
  try {
    $env:GOFLAGS = ''
    & $Go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico $Icon -manifest $Manifest -o $Syso
    if ($LASTEXITCODE -ne 0) { throw 'rsrc failed' }
  } finally { Pop-Location }
} else {
  Write-Host "using existing $Syso"
}

Step '2/5 go build (GUI subsystem, versions injected)'
Push-Location $SrcDir
try {
  $env:GOFLAGS = ''
  # appName is injected rather than edited: the same source is DSH-AB or DSH-ABtest depending on it,
  # and that one string is the whole product identity on the Go side (naming.go).
  $ldflags = "-H=windowsgui -s -w -X main.dshabVersion=$DshabVersion -X main.dshVersion=$DshVersion -X main.appName=$ProductName"
  # -buildvcs=false 不能少。Go 1.18 起 go build 默认把 VCS 戳记编进二进制，vcs.modified 直接取自
  # git status：**仓库根上多一个未跟踪、未被忽略的文件**，就把这一位从 false 翻成 true，同一份源码
  # 于是编出另一个哈希——2026-09-23 实测，干净树 8CA4973C…，往仓库根放一个未跟踪的 vcs-probe.txt
  # 就变成 CBBB690D…，go version -m 里那几行正是 build vcs=git / vcs.modified=true，连 module 版本
  # 都成了 v0.0.0-<rev>+dirty。后果不是「少了一行元数据」而是构建不可复现：dist 里的安装包、更新包
  # 里那份 exe，与别人按同一个 commit 编出来的不是同一份字节，哈希对不上却查不出任何源码差异。
  # 本程序没有一行读这个戳记（版本号与产品名全由 -ldflags 注入），所以直接关掉。
  # 只在这条产出随包二进制的 go build 上加：go vet / go test 不产出发布产物。
  & $Go build -buildvcs=false -trimpath -ldflags $ldflags -o $AppExe .
  if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
  & $Go vet ./...
  if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
} finally { Pop-Location }

$bytes = [IO.File]::ReadAllBytes($AppExe)
$subsystem = [BitConverter]::ToUInt16($bytes, [BitConverter]::ToInt32($bytes, 0x3C) + 0x5C)
if ($subsystem -ne 2) { throw "$ExeName is not a GUI-subsystem binary (subsystem=$subsystem)" }
Write-Host "${ExeName}: GUI subsystem, $([math]::Round($bytes.Length / 1MB, 2)) MB"

# The injected versions are read back out of the built binary, not assumed: the installer
# name and the installation pages are derived from exactly this report.
$versionTmp = Join-Path $env:TEMP "dsh-ab-version-$PID.txt"
if (Test-Path -LiteralPath $versionTmp) { Remove-Item -LiteralPath $versionTmp -Force }
Start-Process -FilePath $AppExe -ArgumentList '--version' -NoNewWindow -Wait -RedirectStandardOutput $versionTmp | Out-Null
$reported = (Get-Content -LiteralPath $versionTmp -Raw).Trim()
Remove-Item -LiteralPath $versionTmp -Force
Write-Host "$ExeName --version: $reported"
foreach ($v in @($DshabVersion, $DshVersion)) {
  if ($reported -notmatch [regex]::Escape($v)) { throw "$ExeName 没有报出版本 $v（实际：$reported）" }
}

if (-not $SkipPayload) {
  Step '3/5 payload'
  $payloadArgs = @{
    DshVersion  = $DshVersion
    NodeVersion = $NodeVersion
    GitVersion  = $GitVersion
    Registry    = $Registry
    OutDir      = (Join-Path $RepoRoot 'payload')
    AppExe      = $AppExe
  }
  if ($Offline) { $payloadArgs['Offline'] = $true }
  if ($ProgramOnly) { $payloadArgs['ProgramOnly'] = $true }
  & (Join-Path $PSScriptRoot 'mkpayload.ps1') @payloadArgs
} else { Step '3/5 payload (skipped)' }

$artifactId = "$ProductName-$DshabVersion-$DshVersion-setup"
if (-not $SkipInstaller) {
  Step '4/5 installer'
  if (-not (Test-Path $Iscc)) { throw "ISCC not found: $Iscc" }
  # /DTestProduct=1 switches every product-scoped constant in dsh-ab.iss (AppName, the default
  # directory, the icon folder, the Add/Remove prefix, the exe source, the output file name).
  $isccArgs = @("/DDshabVersion=$DshabVersion", "/DDshVersion=$DshVersion")
  if ($TestProduct) { $isccArgs += '/DTestProduct=1' }

  # ISCC 读不了超过 MAX_PATH 的**源**路径（仓库路径 + 载荷内相对路径）。载荷的相对部分由
  # mkpayload.ps1 结尾的路径门禁卡在 180 字符以内，所以这里直接用仓库里的 payload\ 编译：CI 的
  # D:\a\<repo>\<repo>\payload\ 也才 40 来个字符，加上 180 仍远低于 260。
  # 曾经为此把载荷 subst 到一个空闲盘符再 /DPayload=X:\ 传进来——那是治标：真正超标的是载荷内的
  # 相对路径，接短前缀救不了它。
  & $Iscc @isccArgs (Join-Path $RepoRoot 'installer\dsh-ab.iss')
  $isccCode = $LASTEXITCODE
  if ($isccCode -ne 0) { throw "ISCC failed with exit code $isccCode" }
  $setup = Join-Path $RepoRoot "dist\$artifactId.exe"
  if (-not (Test-Path -LiteralPath $setup)) { throw "期望的产物不存在：$setup" }
  Write-Host "产物：$setup（$([math]::Round((Get-Item $setup).Length / 1MB, 1)) MB）"
} else { Step '4/5 installer (skipped)' }

# The update pack is the payload's program files, minus the slots, the user's configuration and the
# ledgers (the authoritative list is mkupdate.ps1's -Ignore). dsh lives inside the slots, so the pack
# is independent of the dsh version it is built next to.
# CI builds that one pack per DSH-AB version in its own update-pack job and uploads it.
# 做不做这一步只看 -SkipUpdatePack，与装不装安装器无关：-SkipInstaller 时照样做，那个 job 就是这么跑的。
if (-not $SkipUpdatePack) {
  Step '5/5 更新包'
  # 包里那份 exe 就是安装包那份：payload\DSH_AB.exe 是上面第 2 步编出来的 build\<产品>.exe 的副本，
  # 也是 mkupdate.ps1 的默认 -AppExe。同一份源码不再编两次——以前这里为更新包单独编一份 dshVersion
  # 为空的 exe，那份 exe 与安装包那份只差一个编译常量，代价却是每次构建多一次 go build，还逼着
  # versionText 留一条「没有 dsh 版本」的分支。
  # -AppExe 显式传这一次构建出来的那一份（正式构建就是 payload\DSH_AB.exe 的源），测试构建因此装的
  # 还是测试产品那份 exe，不会把正式产品的 exe 塞进测试更新包。
  & (Join-Path $PSScriptRoot 'mkupdate.ps1') -AppExe $AppExe -ProductName $ProductName -DshabVersion $DshabVersion
  if ($LASTEXITCODE -ne 0) { throw "mkupdate.ps1 failed with exit code $LASTEXITCODE" }
} else { Step '5/5 更新包 (skipped)' }

Write-Host ""
Write-Host 'dist:' -ForegroundColor Green
Get-ChildItem (Join-Path $RepoRoot 'dist') -ErrorAction SilentlyContinue |
  Select-Object Name, @{n = 'MB'; e = { [math]::Round($_.Length / 1MB, 1) } } | Format-Table -AutoSize | Out-String | Write-Host
