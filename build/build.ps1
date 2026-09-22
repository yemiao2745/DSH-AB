<#
  build.ps1 - build DSH_AB.exe and the DSH-AB installer for one dsh version.

  Steps: 1) icon/manifest resource
         2) Go build, GUI subsystem, both version numbers injected with -ldflags
         3) payload assembly (build\mkpayload.ps1: fetched from the network, verified, cached)
         4) Inno Setup compile -> dist\<product>-<dshab version>-<dsh version>-setup.exe
         5) in-place update pack -> dist\<product>-<dshab version>-update.zip (build\mkupdate.ps1)

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
  & $Go build -trimpath -ldflags $ldflags -o $AppExe .
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

  # ISCC 读不了超过 MAX_PATH 的**源**路径：载荷里只要有一条（npm 的嵌套 node_modules 会造出来，
  # 实测 dsh 0.1.5-alpha.2 的载荷里有一条 261 字符的），编译就以「系统找不到指定的路径」中止。
  # 所以把载荷根接到一个空闲盘符上再编译：源路径前缀从 <仓库>\payload\ 变成 X:\，长度不再跟着
  # 仓库位置走（CI 的 D:\a\<repo>\<repo> 比开发机还长二十来个字符）。目的路径与产物内容一个字节
  # 都不变，装出来的东西和以前完全一样。
  $letter = $null
  foreach ($l in 'X', 'Y', 'Z', 'W', 'V', 'U', 'T', 'S', 'R', 'Q') {
    if (-not (Test-Path -LiteralPath ($l + ':\'))) { $letter = $l; break }
  }
  if (-not $letter) { throw '没有空闲盘符可以给载荷用（ISCC 读不了超长源路径）' }
  subst ($letter + ':') (Join-Path $RepoRoot 'payload')
  if ($LASTEXITCODE -ne 0) { throw "subst 失败：$letter（退出码 $LASTEXITCODE）" }
  try {
    & $Iscc @(@($isccArgs) + ('/DPayload=' + $letter + ':\')) (Join-Path $RepoRoot 'installer\dsh-ab.iss')
    $isccCode = $LASTEXITCODE
  } finally {
    subst ($letter + ':') /D | Out-Null
  }
  if ($isccCode -ne 0) { throw "ISCC failed with exit code $isccCode" }
  $setup = Join-Path $RepoRoot "dist\$artifactId.exe"
  if (-not (Test-Path -LiteralPath $setup)) { throw "期望的产物不存在：$setup" }
  Write-Host "产物：$setup（$([math]::Round((Get-Item $setup).Length / 1MB, 1)) MB）"
} else { Step '4/5 installer (skipped)' }

# 就地更新包：内容 = payload 里除两个槽以外的程序文件。dsh 本体在槽里，
# 所以它与旁边一起构建的那个 dsh 版本无关——一个 DSH-AB 版本只有一份。CI 里每个矩阵格都会构建它，
# 但只把它作为 release asset 上传一次（见 .github\workflows\build-dsh-ab.yml 的 update_cell）。
if (-not $SkipInstaller) {
  Step '5/5 更新包'
  & (Join-Path $PSScriptRoot 'mkupdate.ps1') -AppExe $AppExe -ProductName $ProductName -DshabVersion $DshabVersion
  if ($LASTEXITCODE -ne 0) { throw "mkupdate.ps1 failed with exit code $LASTEXITCODE" }
} else { Step '5/5 更新包 (skipped)' }

Write-Host ""
Write-Host 'dist:' -ForegroundColor Green
Get-ChildItem (Join-Path $RepoRoot 'dist') -ErrorAction SilentlyContinue |
  Select-Object Name, @{n = 'MB'; e = { [math]::Round($_.Length / 1MB, 1) } } | Format-Table -AutoSize | Out-String | Write-Host
