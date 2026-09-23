<#
  verify-silent.ps1 - the only sanctioned way to run the DSH-AB installer and uninstaller.

  Contract:
    * every installer run is preceded by "clear the target directory, then assert
      it really is empty". Every run, not just the first.
    * clear -> assert empty -> install -> uninstall is one atomic cycle.
    * a silent run never shows a modal frame: a non-empty target is refused with a
      log line and a non-zero exit code.
    * every process is bounded by a timeout. A dialog blocking a silent run is reported as a
      failure and its process tree is killed, so it can never sit on the user's desktop.
    * the HKCU uninstall entry and the two shortcuts the user's installation owns are
      snapshotted before the run and restored after it. They belong to that installation
      alone now - the entry key, the display name and both .lnk names are derived from the
      installation directory - so a test install must leave them untouched; the snapshot is
      what shows that, and what puts them back if it ever regresses.

  Exit code 0 = every scenario passed.
#>
[CmdletBinding()]
param(
  # Defaults are resolved below, from this script's own location: a clone anywhere then runs
  # unchanged. Hardcoded D:\dshab\... defaults pointed -Iss at a file that does not exist in a
  # clone, and under $ErrorActionPreference='Stop' the static gates blew up instead of failing
  # loudly on the thing they were meant to check.
  [string]$Dir        = '',
  # Empty = the newest dist\DSH-AB-*-setup.exe built by build\build.ps1.
  [string]$Setup      = '',
  [string]$Iss        = '',
  [int]   $TimeoutSec = 900,
  [int]   $RefuseSec  = 300
)
$ErrorActionPreference = 'Stop'

$NL       = [char]10
$RepoRoot = Split-Path -Parent $PSScriptRoot
if (-not $Dir) { $Dir = Join-Path $RepoRoot '.tmp-verify\install' }
if (-not $Iss) { $Iss = Join-Path $RepoRoot 'installer\dsh-ab.iss' }
$LogDir   = Join-Path $RepoRoot '.tmp-verify'
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

# 「安装根里应有哪些文件」这条规则与 mkupdate.ps1 共用一份实现（install-layout.ps1）。
. (Join-Path $PSScriptRoot 'install-layout.ps1')

# The artifact name, the payload record and the versions compiled
# into the exe must agree. The payload manifest is written by build\mkpayload.ps1 and
# travels with the payload, so it is the one place the expected pair comes from.
$Manifest = Join-Path $RepoRoot 'payload\payload-manifest.json'
if (-not (Test-Path -LiteralPath $Manifest)) { throw "找不到 payload 清单：$Manifest（先跑 build\build.ps1）" }
$mf = Get-Content -LiteralPath $Manifest -Raw | ConvertFrom-Json
$expectedSetupName = "DSH-AB-$($mf.dshab_version)-$($mf.dsh_version)-setup.exe"

# 「安装根里应当有哪些文件」的期望集合来自构建期记录（mkpayload.ps1 写进清单的那份文件清单），不是
# 当前的 payload 目录。原因见 install-layout.ps1：两侧都读活载荷时，只要在编安装包之前动过载荷，载荷
# 与安装根就一起少、一起多，比对无差异、门禁全绿。清单里没有这条记录，说明载荷是旧版 mkpayload.ps1
# 组装的：宁可在这里停住，也绝不悄悄退回「拿活载荷当期望」——那正是要堵住的那条路。
$recordedFiles = @($mf.install_files)
if ($recordedFiles.Count -eq 0) {
  throw "payload 清单里没有 install_files（构建期记录）：$Manifest 是旧版 mkpayload.ps1 写的，payload 要重跑一次构建"
}

if (-not $Setup) {
  # 只认清单说的那个正式产物名：dist 里现在也可能有一份测试构建（DSH-ABtest-…-setup.exe），
  # 而通配符 'DSH-AB-*-setup.exe' 会把 "DSH-ABtest-…" 也匹配进来（-Filter 不做词边界），
  # 于是「按最后写入时间取最新」会挑中测试产物，再被下面的名字闸门拒掉。
  $cand = @(Get-ChildItem (Join-Path $RepoRoot 'dist') -Filter '*.exe' -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -eq $expectedSetupName } |
            Sort-Object LastWriteTime -Descending)
  if ($cand.Count -eq 0) { throw "找不到安装程序：dist\$expectedSetupName" }
  $Setup = $cand[0].FullName
}
Write-Host "被测安装包：$Setup"
if ((Split-Path -Leaf $Setup) -ne $expectedSetupName) {
  throw "产物名不符合 DSH-AB-<dshab版本>-<dsh版本>-setup.exe：实际 $(Split-Path -Leaf $Setup)，期望 $expectedSetupName"
}

# "Nothing of ours outside the installation" is checked as a delta: the user's own
# installation in %LOCALAPPDATA% exists on this machine by design and is not a leftover of
# this run. Anything that appears while the run is going on still fails the scenario.
$Outside = @((Join-Path $env:LOCALAPPDATA 'DSH-AB'), (Join-Path $env:LOCALAPPDATA 'Programs\DSH-AB'))
$Preexisting = @{}
foreach ($p in $Outside) {
  $Preexisting[$p] = Test-Path -LiteralPath $p
  if ($Preexisting[$p]) { Write-Host "      运行前已存在（不是本次运行的痕迹）：$p" }
}

# The same delta idea for processes: the user's own DSH_AB.exe is normally running while this script
# runs, so "is DSH_AB running?" says nothing about this run. Only a DSH_AB.exe that appears *during*
# the run is evidence - that is the tray icon a silent install must never create.
function Get-DshAbPids {
  @(Get-Process -Name 'DSH_AB' -ErrorAction SilentlyContinue | ForEach-Object { $_.Id } | Sort-Object)
}
$DshAbPidsBefore = Get-DshAbPids
if ($DshAbPidsBefore.Count -gt 0) {
  Write-Host "      运行前已有 DSH_AB.exe 在跑（不是本次运行的痕迹）：PID $($DshAbPidsBefore -join ', ')"
}

# ---- user-visible state that the real installation owns -------------------------------------
# The user's installation owns exactly one HKCU uninstall entry and two shortcuts. One AppId and one
# shortcut name used to mean that a silent install of a *test* copy wrote those same three things,
# pointing at the test directory, and that the test uninstall then deleted them - two runs already
# cost the user their shortcuts. Installations have an identity of their own now, so this run has
# to leave all three exactly as it found them. They are snapshotted before
# the run and put back afterwards - byte for byte for the shortcuts, reg import for the entry - and a
# restore that finds nothing to put back is the regression check for that. The key below carries the
# *old* fixed AppId on purpose: it is the existing installation's entry, the one this setup no longer
# shares (its own entries are keyed by the installation directory).
$UninstallKey = 'HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\{8F3A1C42-6D7E-4B9A-9E15-2C4F7A0B6D31}_is1'

# 两条快捷方式落在哪儿，问那份安装自己的登记项，不写死名字：安装器把真实路径写进
# DshAbStartMenuLink / DshAbDesktopLink（dsh-ab.iss 的 WriteUninstallEntry，与 naming.go 是同一对
# 值名），而名字随本机份数变（DSH-AB / DSH-AB (端口) / DSH-AB (tag)）。原来这里是写死的
# Start Menu\Programs\DSH-AB\DSH-AB.lnk —— 一个已经不存在的子文件夹加一个固定名字，用户真实的
# 那条平铺的 <Programs>\DSH-AB.lnk 因此根本不在清单里，门禁中途失败也还原不回来。
$UserEntryPath = $UninstallKey -replace '^HKCU\\', 'HKCU:\'
$userEntry = Get-ItemProperty -LiteralPath $UserEntryPath -ErrorAction SilentlyContinue
$ShortcutPaths = @()
foreach ($item in @(
    @{ Value = 'DshAbStartMenuLink'; Dir = (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs') },
    @{ Value = 'DshAbDesktopLink';   Dir = [Environment]::GetFolderPath('DesktopDirectory') })) {
  $recorded = if ($userEntry) { $userEntry.($item.Value) } else { $null }
  if ($recorded) {
    $ShortcutPaths += $recorded
  } elseif ($userEntry -and $userEntry.DisplayName) {
    # 登记项里没有这两个值（2026-09-22 之前的安装）：按现在的口径补一条 <Programs|桌面>\<名字>.lnk
    # —— 没有子文件夹，名字就是 InstallName（[Code] 的 InstallIconPath 返回同一个字符串，它也正是
    # 登记项里的 DisplayName）。
    $ShortcutPaths += (Join-Path $item.Dir ($userEntry.DisplayName + '.lnk'))
  }
}
if ($ShortcutPaths.Count -gt 0) {
  Write-Host "      快捷方式保护清单 $($ShortcutPaths.Count) 条（取自登记项，缺值时按安装器口径推）：$($ShortcutPaths -join ' | ')"
} else {
  Write-Host '      这台机器没有既有的卸载登记项，没有快捷方式需要保护'
}
$StateDir = Join-Path $LogDir 'user-state'
$script:StateSnapshotTaken = $false
$script:StateRestored      = $false

function Save-UserVisibleState {
  if (Test-Path -LiteralPath $StateDir) { Remove-Item -LiteralPath $StateDir -Recurse -Force }
  New-Item -ItemType Directory -Force -Path $StateDir | Out-Null

  $i = 0
  foreach ($p in $ShortcutPaths) {
    if (Test-Path -LiteralPath $p) {
      Copy-Item -LiteralPath $p -Destination (Join-Path $StateDir "shortcut$i.lnk") -Force
      Write-Host "      已快照快捷方式（$((Get-Item -LiteralPath $p).Length) B）：$p"
    }
    $i += 1
  }

  & reg.exe query $UninstallKey *> $null
  $keyExisted = ($LASTEXITCODE -eq 0)
  if ($keyExisted) {
    $regFile = Join-Path $StateDir 'uninstall.reg'
    & reg.exe export $UninstallKey $regFile /y *> $null
    if ($LASTEXITCODE -ne 0) { throw "无法快照卸载登记项（reg export 退出码 $LASTEXITCODE）：$UninstallKey" }
    Write-Host "      已快照卸载登记项：$UninstallKey"
  } else {
    Write-Host "      运行前没有卸载登记项，运行后要把它删掉：$UninstallKey"
  }
  Set-Content -LiteralPath (Join-Path $StateDir 'key-existed.txt') -Value ([string]$keyExisted) -Encoding ascii
  $script:StateSnapshotTaken = $true
}

# Restore-UserVisibleState puts the snapshotted state back and fails loudly when it cannot: a silent
# success would be worse than the error, because the user's shortcuts would stay lost.
function Restore-UserVisibleState {
  if ($script:StateRestored) { return }
  $script:StateRestored = $true
  if (-not $script:StateSnapshotTaken) { return }
  $problems = @()

  $keyExisted = ((Get-Content -LiteralPath (Join-Path $StateDir 'key-existed.txt') -Raw).Trim() -eq 'True')
  if ($keyExisted) {
    # Delete first: the test install may have written value names the snapshot does not have, and
    # reg import merges instead of replacing.
    & reg.exe delete $UninstallKey /f *> $null
    & reg.exe import (Join-Path $StateDir 'uninstall.reg') *> $null
    if ($LASTEXITCODE -ne 0) { $problems += "卸载登记项还原失败（reg import 退出码 $LASTEXITCODE）：$UninstallKey" }
  } else {
    & reg.exe delete $UninstallKey /f *> $null
  }
  & reg.exe query $UninstallKey *> $null
  if ($keyExisted -ne ($LASTEXITCODE -eq 0)) {
    $problems += "卸载登记项状态与运行前不一致（运行前存在=$keyExisted）：$UninstallKey"
  }

  $i = 0
  foreach ($p in $ShortcutPaths) {
    $backup = Join-Path $StateDir "shortcut$i.lnk"
    if (Test-Path -LiteralPath $backup) {
      New-Item -ItemType Directory -Force -Path (Split-Path -Parent $p) | Out-Null
      Copy-Item -LiteralPath $backup -Destination $p -Force
      $now = (Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash
      $was = (Get-FileHash -LiteralPath $backup -Algorithm SHA256).Hash
      if ($now -ne $was) { $problems += "快捷方式还原后与快照不一致：$p" }
    } elseif (Test-Path -LiteralPath $p) {
      # It did not exist before, so whatever is there now belongs to this run.
      Remove-Item -LiteralPath $p -Force
    }
    $i += 1
  }

  if ($problems.Count -gt 0) {
    throw ('用户可见状态没有还原干净：' + $NL + ($problems -join $NL))
  }
  Write-Host '      用户可见状态已还原（快捷方式逐字节、卸载登记项按快照）'
}

Save-UserVisibleState
# The restore must not depend on the run succeeding: a scenario that fails is exactly when the user's
# state would otherwise stay clobbered. break in a trap re-raises the original error (a plain "throw"
# here would replace the failing scenario's message with a ScriptHalted at this line).
trap { Restore-UserVisibleState; break }

function Clear-Dir([string]$Path) {
  if (Test-Path -LiteralPath $Path) { Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction SilentlyContinue }
  # rd /s /q clears what Remove-Item chokes on (read-only or long-path files).
  if (Test-Path -LiteralPath $Path) { & cmd.exe /c rd /s /q "$Path" | Out-Null }
  if (Test-Path -LiteralPath $Path) { throw "目标目录没清干净：$Path" }
}

# Clear-AndAssertEmpty is "clear the target directory, then assert it really is empty", in code.
function Clear-AndAssertEmpty([string]$Path) {
  Clear-Dir $Path
  New-Item -ItemType Directory -Force -Path $Path | Out-Null
  $n = @(Get-ChildItem -LiteralPath $Path -Force).Count
  if ($n -ne 0) { throw "目标目录没清干净：$Path（还有 $n 项）" }
}

function Assert-Exists([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) { throw "缺少文件：$Path" }
}

# Run-Bounded starts a process and never lets it outlive $Seconds. A modal dialog blocking a
# silent run shows up here as a timeout instead of hanging the caller forever.
function Run-Bounded([string]$File, [string[]]$Arguments, [int]$Seconds) {
  $p = Start-Process -FilePath $File -ArgumentList $Arguments -PassThru
  if (-not $p.WaitForExit($Seconds * 1000)) {
    & taskkill.exe /PID $p.Id /T /F 2>&1 | Out-Null
    $p.WaitForExit(30000) | Out-Null
    throw "超时：$File 在 $Seconds 秒内没有退出（疑似模态框阻塞），其进程树已被结束"
  }
  return $p.ExitCode
}

# Wait-Uninstaller polls until Inno's uninstaller has really finished. unins000.exe hands the
# work to a copy in %TEMP% and returns immediately, so its exit code says nothing about the
# files: without this wait the next scenario races the deletion still in flight.
function Wait-Uninstaller([string]$Path, [int]$Seconds) {
  $deadline = (Get-Date).AddSeconds($Seconds)
  while ((Get-Date) -lt $deadline) {
    $busy = @(Get-Process -ErrorAction SilentlyContinue |
              Where-Object { $_.ProcessName -match '^(unins|_unins)' }).Count
    if ($busy -eq 0 -and -not (Test-Path -LiteralPath (Join-Path $Path 'unins000.exe'))) {
      Start-Sleep -Milliseconds 800
      return
    }
    Start-Sleep -Milliseconds 400
  }
  throw "卸载程序在 $Seconds 秒内没有真正结束（$Path）"
}

if (-not (Test-Path -LiteralPath $Setup)) { throw "找不到安装程序：$Setup" }

# ---- static gate: a silent run may never meet a modal frame ---------------------------------
# A script MsgBox ignores /SUPPRESSMSGBOXES and blocks a silent run forever; only
# SuppressibleMsgBox is suppressed. This is the recorded root cause of the 2026-09-19 popups.
$bare = @(Select-String -LiteralPath $Iss -Pattern '(?<!Suppressible)\bMsgBox' |
          Where-Object { $_.Line.TrimStart() -notlike '//*' })
if ($bare.Count -gt 0) {
  $where = ($bare | ForEach-Object { "  " + $Iss + "(" + $_.LineNumber + "): " + $_.Line.Trim() }) -join $NL
  throw ("安装器脚本里还有未被抑制的 MsgBox（它会无视 /SUPPRESSMSGBOXES，把静默安装永远堵住）：" + $NL + $where)
}

# ---- static gate: the installer must not mitigate its own children ---------------------------
# Inno Setup 6.4+ defaults RedirectionGuard to on. Setup then starts every [Run] entry
# with the "enforce redirection trust" mitigation, Windows hands it down the process tree,
# and the dsh child is refused when it follows the junctions of its own profile fallback:
# every @deepseek-ai package fails to resolve and dsh exits with ERR_MODULE_NOT_FOUND.
# That is exactly the first start after an install - a failure the installer causes itself.
if (-not (Select-String -LiteralPath $Iss -Pattern '^\s*RedirectionGuard\s*=\s*no\s*$')) {
  throw "安装器脚本没有 RedirectionGuard=no（Setup 会给 [Run] 子进程套上重定向信任缓解，dsh 的 junction 会被拒绝）"
}

# ---- static gate: the installer and its uninstaller are Chinese -------------------------------
# Inno Setup 6.7.3 ships no Chinese translation, so the wizard, the uninstaller and every
# built-in prompt come from the vendored file; its two "currently running" entries have been
# unreachable since the AppMutex went away (gate below). A silent run shows no UI at
# all, so this gate is the only thing here that would catch a revert to the English default or
# a missing language file.
$isl = Join-Path (Split-Path -Parent $Iss) 'languages\ChineseSimplified.isl'
if (-not (Select-String -LiteralPath $Iss -Pattern 'MessagesFile:\s*"languages\\ChineseSimplified\.isl"')) {
  throw "安装器脚本没有指向随仓库的中文语言文件"
}
if (-not (Test-Path -LiteralPath $isl)) { throw "中文语言文件不存在：$isl" }

# ---- static gate: the artifact name and the built-in dsh version -----------------------------
# A silent run shows no wizard, so the file name and the two pages that must carry the
# built-in dsh version are checked in the script itself.
if (-not (Select-String -LiteralPath $Iss -Pattern '^\s*OutputBaseFilename=\{#AppName\}-\{#DshabVersion\}-\{#DshVersion\}-setup\s*$')) {
  throw "安装包产物名不是 <产品名>-<dshab版本>-<dsh版本>-setup（产品名是 {#AppName}，正式构建即 DSH-AB）"
}
if (-not (Select-String -LiteralPath $Iss -Pattern '(?m)^\s*WelcomeLabel2=.*\{#DshVersion\}')) {
  throw "欢迎页没有显示内置 dsh 版本"
}
foreach ($label in @('FinishedLabel', 'FinishedLabelNoIcons')) {
  $line = Select-String -LiteralPath $Iss -Pattern "^\s*$label=.*\{#DshVersion\}"
  if (-not $line) { throw "完成页的 $label 没有显示内置 dsh 版本" }
}

# ---- static gate: a second installation must stay possible -----------------------------------
# AppMutex is the one [Setup] directive that makes Setup refuse while a DSH_AB.exe is running,
# which bans a second DSH-AB on the machine - and it is what made this script fail while the
# user's instance was up. Nothing here may bring it back.
if (Select-String -LiteralPath $Iss -Pattern '^\s*AppMutex\s*=') {
  throw "安装器脚本又有了 AppMutex（它会拒绝装第二份，也会在用户实例运行时拒绝安装）"
}

# ---- static gate: every installation keeps its own identity ----------------------------------
# One AppId and one fixed shortcut name made the second installation take the first one's Add/Remove
# entry and both of its shortcuts over, and made uninstalling either copy delete them for the other
# The identity is per installation now, derived by [Code] from the
# installation directory: Inno must not own the Add/Remove entry, the wizard must not offer a
# directory it would refuse, and no shortcut may carry a fixed name.
if (-not (Select-String -LiteralPath $Iss -Pattern '^\s*CreateUninstallRegKey\s*=\s*no\s*$')) {
  throw '安装器脚本又让 Inno 自建卸载登记项了（一个编译期 AppId 无法描述多份安装，登记项必须由 [Code] 按安装根派生）'
}
if (-not (Select-String -LiteralPath $Iss -Pattern '^\s*UsePreviousAppDir\s*=\s*no\s*$')) {
  throw '安装器脚本又「记住上次安装目录」了（非空目录必被拒，记住的目录只会把用户领进墙）'
}
# 名字与位置都交给 [Code] 算（InstallIconPath / DesktopIconPath），所以这两行的 Name 由
# {userprograms} / {autodesktop} 打头、叶子名来自脚本常量。固定名字（DSH-AB.lnk）正是两份安装
# 互相覆盖的成因，这条闸门守的就是它。{group} 不再出现：DefaultGroupName 会把单份安装也塞进
# DSH-AB 子文件夹，而单份的要求就是裸的那一个。
foreach ($prefix in @('Name: "{userprograms}\', 'Name: "{autodesktop}\')) {
  $hit = @(Select-String -LiteralPath $Iss -Pattern $prefix -SimpleMatch)
  if ($hit.Count -ne 1) { throw "找不到唯一的 [Icons] 入口（$prefix）：$($hit.Count) 条" }
  if (-not $hit[0].Line.Contains('{code:')) {
    throw "快捷方式名又写死了（两份安装会互相覆盖）：$($hit[0].Line.Trim())"
  }
}

# ---- static gate: the installer tells DSH-AB where its shortcuts are (2026-09-22) -------------
# DSH-AB renames and moves its own shortcut when the name or the layout changes, and it can only
# do that by path: the shell splits a .lnk's target into shell items plus a relative LinkInfo
# path, so the absolute path never appears contiguously in the file (measured on a fresh install
# and on a C: shortcut pointing at a D: target), a content scan finds nothing, and a scan that
# guessed by file name could rename another installation's shortcut instead. These two value
# names are the contract between the installer and src\dsh-ab\naming.go, so both halves are
# checked here.
$Nam = Join-Path $RepoRoot 'src\dsh-ab\naming.go'
if (-not (Test-Path -LiteralPath $Nam)) { throw "找不到命名实现：$Nam" }
foreach ($value in @('DshAbStartMenuLink', 'DshAbDesktopLink')) {
  if (-not (Select-String -LiteralPath $Iss -Pattern ("RegWriteStringValue\(HKCU, Key, '" + $value + "'"))) {
    throw "安装器没有记录 $value（DSH-AB 只能按这条路径搬自己的快捷方式，认 .lnk 的内容是认不出来的）"
  }
  if (-not (Select-String -LiteralPath $Nam -Pattern ('"' + $value + '"'))) {
    throw "naming.go 里没有 $value：安装器与 DSH-AB 的约定对不上了"
  }
}

# ---- static gate: the installer does no git at all --------------------------------------------
# Git is the AI's job now (the maintenance skill says so). A leftover git call here would recreate
# the baseline repository that was dropped, on a machine the user may not expect it on.
foreach ($pattern in @('RunGit', 'InitBaselineRepo', 'git init')) {
  if (Select-String -LiteralPath $Iss -Pattern $pattern -SimpleMatch) {
    throw "安装器脚本里还有 git 操作：$pattern（安装器不做任何 git）"
  }
}

# ---- scenario 1: clear -> assert empty -> silent install -> silent uninstall -----------------
Write-Host '[1/5] 静默安装 + 静默卸载（D15：默认保留用户数据）'
Clear-AndAssertEmpty $Dir
$installLog = Join-Path $LogDir 's1-install.log'
# The port travels on the command line on purpose: a silent install never shows the ports page, so
# this is the only path that can carry it, and the assertion below fails if it is not wired up.
$silentProductionPort = 3190
$code = Run-Bounded $Setup @(
  '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/LOG=$installLog", "/DIR=$Dir",
  "/PRODUCTION=$silentProductionPort") $TimeoutSec
if ($code -ne 0) { throw "安装失败，退出码 $code（日志 $installLog）" }

# 一个集合比对替掉原来那串 13 条硬编码路径：构建期记录里该装进安装根的文件必须与安装根实际的每个
# 文件逐个对上，缺一个就失败。硬编码永远追不上载荷，2026-09-23 安装器的 Excludes 少一个反斜杠、45 个
# 文件（pi-ai 的 providers\data\*.json、node-gyp 的 gyp\data）静默没进安装根，那 13 条照样全绿。
# 期望集合用那份记录而不是当前载荷，正是这次要堵的盲区：编完（或编之前动过载荷、只编了安装包）之后
# 删掉载荷里一个文件，安装根缺这一个而记录照旧列着它——原先两侧都读活载荷，一起少、一起绿。
# 白名单只有安装器自己生成的那两个文件：unins*（卸载程序）。载荷里唯一不装的是
# payload-manifest.json，那条规则在 install-layout.ps1 里。
$payloadFiles   = Get-InstallRootFiles (Join-Path $RepoRoot 'payload')
$installedFiles = Get-InstallRootFiles $Dir -Ignore 'unins*'
$diff = Compare-FileSet $recordedFiles $installedFiles
if ($diff.Missing.Count -gt 0) {
  throw "构建期记录里有 $($diff.Missing.Count) 个文件没装进安装根：$(Format-FileSetDiff $diff.Missing)"
}
if ($diff.Unexpected.Count -gt 0) {
  throw "安装根里有 $($diff.Unexpected.Count) 个构建期记录里没有的文件：$(Format-FileSetDiff $diff.Unexpected)"
}
# 载荷自己也要与记录一致：比记录少（构建后删了文件，而安装包是按记录编的）或者比记录多（构建后加进去、
# 又没有重编安装包，那它永远不会进安装根，上一条比对查不出来）。两个方向都是红的。
$drift = Compare-FileSet $recordedFiles $payloadFiles
if ($drift.Missing.Count -gt 0) {
  throw "payload 比构建期记录少 $($drift.Missing.Count) 个文件（构建后删过？）：$(Format-FileSetDiff $drift.Missing)"
}
if ($drift.Unexpected.Count -gt 0) {
  throw "payload 里有 $($drift.Unexpected.Count) 个构建期记录里没有的文件（构建后加的？没重编安装包就不会进安装根）：$(Format-FileSetDiff $drift.Unexpected)"
}
# state\ 与 logs\ 是 [Dirs] 建的目录，载荷里没有它们，所以要单独说一句；slot-b 的检查在下面。
foreach ($d in @('state', 'logs')) {
  if (-not (Test-Path -LiteralPath (Join-Path $Dir $d) -PathType Container)) { throw "安装根缺少目录：$d" }
}
Write-Host "      安装根与构建期记录逐个对上：$($installedFiles.Count) 个文件（载荷 $($payloadFiles.Count) 个，记录 $($recordedFiles.Count) 个）"

# ---- no AGENTS.md, and no git at all ----------------------------------------------------------
# dsh injects <DSH_HOME>\AGENTS.md into every session, whatever the working directory is, so a
# copy inside a slot turns the maintenance manual into permanent prompt pollution. The AI
# instructions are a user-level skill now, and the installer no longer runs git (the AI does).
foreach ($f in @(
  'AGENTS.md', '.git', 'slot-a\data\AGENTS.md', 'slot-b\data\AGENTS.md',
  'slot-a\.git', 'slot-b\.git', 'docs\AGENTS.md')) {
  if (Test-Path -LiteralPath (Join-Path $Dir $f)) {
    throw "不该存在的东西出现了：$f（安装期不再放 AGENTS.md，也不做任何 git 操作）"
  }
}

# ---- slot-b ships empty -----------------------------------------------------------------------
# An empty slot is safe: slotInstalled() needs node\node.exe and the dsh entry together, so the
# tray reports it as "未安装" and refuses to register a switch to it.
if (-not (Test-Path -LiteralPath (Join-Path $Dir 'slot-b'))) { throw '缺少空的 slot-b 目录' }
$slotB = @(Get-ChildItem -LiteralPath (Join-Path $Dir 'slot-b') -Force)
if ($slotB.Count -ne 0) { throw "slot-b 必须是空目录，实际有 $($slotB.Count) 项" }
Write-Host '      slot-b 存在且为空'

# ---- the installation path is baked in at install time ----------------------------------------
# The installed bytes have to be exactly the template with {{DSH_AB_ROOT}} replaced by the real
# installation path. Comparing the whole file is what catches a placeholder the bake step missed, a
# BOM that a non-raw write added (dsh needs the file to start with ---), and any re-encoding of the
# UTF-8 body on the way through the installer, which rewrites all three files from disk.
$baked = @(
  @{ Rel = 'slot-a\data\skills\dsh-self-maintenance\SKILL.md'; Src = 'skills\dsh-self-maintenance\SKILL.md' },
  @{ Rel = 'docs\PLUGINS.md'; Src = 'docs\PLUGINS.md' },
  @{ Rel = 'docs\TODO.md';    Src = 'docs\TODO.md' })
foreach ($item in $baked) {
  $installed = Join-Path $Dir $item.Rel
  $head = [System.IO.File]::ReadAllBytes($installed)[0..2]
  if ($head[0] -eq 0xEF -and $head[1] -eq 0xBB -and $head[2] -eq 0xBF) {
    throw "$($item.Rel) 被写回了 UTF-8 BOM（dsh 要求 skill 文件以 --- 开头）"
  }
  $expected = (Get-Content -LiteralPath (Join-Path $PSScriptRoot "templates\$($item.Src)") -Raw -Encoding UTF8).
              Replace('{{DSH_AB_ROOT}}', $Dir)
  $got = Get-Content -LiteralPath $installed -Raw -Encoding UTF8
  if ($got -ne $expected) {
    if ($got.Contains('{{')) { throw "$($item.Rel) 里还有未替换的 {{...}} 占位符（安装期没有烧入绝对路径）" }
    throw "$($item.Rel) 与「模板替换占位符后」的内容不一致：安装期读写它时改动了正文"
  }
}
Write-Host "      三个随包文件与模板逐字一致，且都已烧入安装路径：$Dir"

# ---- the skill itself is loadable -------------------------------------------------------------
# dsh silently ignores a skill whose directory layout, frontmatter or name is wrong - the AI would
# just never receive the instructions. build\verify-skill.mjs is the one place that is checked.
$nodeExe = Join-Path $RepoRoot 'payload\slot-a\node\node.exe'
if (-not (Test-Path -LiteralPath $nodeExe)) { throw "找不到随包 node：$nodeExe" }
$verifySkill = Join-Path $PSScriptRoot 'verify-skill.mjs'
foreach ($skillsRoot in @((Join-Path $PSScriptRoot 'templates\skills'), (Join-Path $Dir 'slot-a\data\skills'))) {
  & $nodeExe $verifySkill $skillsRoot
  if ($LASTEXITCODE -ne 0) { throw "skill 不合规：$skillsRoot" }
}

# The installer must ship the executable the current source produced. A payload assembled
# before the last Go build silently ships an older entry point (it once survived
# a rebuilt binary), so the shipped bytes are compared with the build output here.
$builtExe = Join-Path $RepoRoot 'build\DSH_AB.exe'
if (-not (Test-Path -LiteralPath $builtExe)) { throw "找不到构建产物：$builtExe" }
$hBuilt = (Get-FileHash -LiteralPath $builtExe -Algorithm SHA256).Hash
$hInstalled = (Get-FileHash -LiteralPath (Join-Path $Dir 'DSH_AB.exe') -Algorithm SHA256).Hash
if ($hBuilt -ne $hInstalled) {
  Write-Host "  build:     $hBuilt"
  Write-Host "  installed: $hInstalled"
  throw '安装包里的 DSH_AB.exe 与 build\DSH_AB.exe 不一致（payload 过期）'
}
Write-Host "      安装包内的 DSH_AB.exe 与 build\DSH_AB.exe 一致：$hInstalled"

# The versions really are inside the shipped bytes: the file name, the payload record and
# what the installed exe reports about itself must be the same pair.
if ($mf.exe_sha256 -and $mf.exe_sha256 -ne $hBuilt.ToLowerInvariant()) {
  throw "payload 清单记的 exe 哈希 $($mf.exe_sha256) 与 build\DSH_AB.exe 的 $($hBuilt.ToLowerInvariant()) 不一致（payload 过期）"
}
$versionTmp = Join-Path $LogDir 'installed-version.txt'
if (Test-Path -LiteralPath $versionTmp) { Remove-Item -LiteralPath $versionTmp -Force }
Start-Process -FilePath (Join-Path $Dir 'DSH_AB.exe') -ArgumentList '--version' -NoNewWindow -Wait -RedirectStandardOutput $versionTmp | Out-Null
$reported = (Get-Content -LiteralPath $versionTmp -Raw).Trim()
Write-Host "      装出来的 DSH_AB.exe --version：$reported"
foreach ($v in @($mf.dshab_version, $mf.dsh_version)) {
  if ($reported -notmatch [regex]::Escape($v)) {
    throw "装出来的 exe 报的版本里没有 $v（实际：$reported）：产物名、payload 清单和 exe 里的版本号必须一致"
  }
}

# [Run] has skipifsilent, so a silent install must not put a tray icon on the desktop - judged as a
# delta against the processes that were already running: the user's own DSH_AB.exe is not evidence
# about this run, a *new* PID is.
$newDshAbPids = @(Get-DshAbPids | Where-Object { $DshAbPidsBefore -notcontains $_ })
if ($newDshAbPids.Count -ne 0) {
  throw "静默安装启动了 DSH_AB.exe（桌面会多出托盘图标）：新 PID $($newDshAbPids -join ', ')"
}
Write-Host '      静默安装没有启动任何新的 DSH_AB.exe'

# The installer used to create a baseline repository; it does no git at all
# now, so the bundled git is only there for the AI that maintains the installation - it still has
# to work.
$gitVer = & (Join-Path $Dir 'runtime\git\cmd\git.exe') --version
if ($LASTEXITCODE -ne 0) { throw '随包 git 不可用' }
Write-Host "      随包 git：$gitVer（安装器不再用它建仓库）"

# The port asked for on the command line has to be the port in the installed config. The
# old script only printed these lines, which is why it never noticed that a silent install ignored
# /PRODUCTION and always wrote the built-in 3090/3091.
$tomlPorts = @{}
foreach ($line in Get-Content -LiteralPath (Join-Path $Dir 'dsh-ab.toml') -Encoding UTF8) {
  $m = [regex]::Match($line, '^\s*(production|test)\s*=\s*(\d+)\s*$')
  if ($m.Success) { $tomlPorts[$m.Groups[1].Value] = [int]$m.Groups[2].Value }
}
if ($tomlPorts.ContainsKey('test')) {
  throw "dsh-ab.toml 里还有测试端口那一行（本轮已删除这个键）：test = $($tomlPorts['test'])"
}
if ($tomlPorts['production'] -ne $silentProductionPort) {
  throw "静默安装没有用命令行给的端口：dsh-ab.toml 里是 production=$($tomlPorts['production'])，期望 $silentProductionPort（/PRODUCTION= 没有被安装器读进去）"
}
Write-Host "      端口来自命令行参数：production = $silentProductionPort"

# ---- EstimatedSize: the "大小" column of 应用和功能 (this round) --------------------------------
# Windows computes nothing itself: the column shows the EstimatedSize DWORD the entry carries (KB),
# and an entry without it shows an empty cell. This installation's entry is keyed by the installation
# directory exactly the way [Code] derives it - SHA-256 of the lowercased root without a trailing
# backslash, first 12 hex characters - and the value is compared with the bytes really on disk, so a
# helper that measures the wrong tree, or nothing at all, fails here instead of in 应用和功能.
# ($Dir is ASCII in every run of this script: LowerCase() in [Code] is ASCII-only.)
$Tag = [BitConverter]::ToString(
  [Security.Cryptography.SHA256]::Create().ComputeHash(
    [Text.Encoding]::Unicode.GetBytes($Dir.ToLower().TrimEnd('\')))).Replace('-', '').ToLower().Substring(0, 12)
$EntryKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{6F1D2A74-3B58-4C9E-8A17-5E0C4D3B9A62}_' + $Tag + '_is1'
if (-not (Test-Path -LiteralPath $EntryKey)) { throw "这次静默安装没有自己的卸载登记项：$EntryKey" }
$estimatedKB = (Get-ItemProperty -LiteralPath $EntryKey -Name 'EstimatedSize' -ErrorAction SilentlyContinue).EstimatedSize
if ($null -eq $estimatedKB) {
  throw "卸载登记项没有 EstimatedSize（应用和功能的「大小」列会是空的）：$EntryKey"
}
$actualBytes = [double](Get-ChildItem -LiteralPath $Dir -Recurse -File -Force | Measure-Object -Property Length -Sum).Sum
if ($estimatedKB -le 0 -or $estimatedKB * 1KB -lt $actualBytes * 0.9 -or $estimatedKB * 1KB -gt $actualBytes * 1.1) {
  throw "EstimatedSize 与安装目录的实际体积对不上：登记项 $estimatedKB KB，实测 $([math]::Round($actualBytes / 1MB, 1)) MB（$EntryKey）"
}
Write-Host "      EstimatedSize = $estimatedKB KB（实测 $([math]::Round($actualBytes / 1MB, 1)) MB）：$EntryKey"

# ---- 应用和功能里的名字：不带版本号，括号在名字里 ---------------------------------------------
# The name is one string shared by every face (naming.go's installName and [Code]'s InstallName are the
# same rule), and the installer writes it into DisplayName verbatim: DSH-AB when this is the only one,
# DSH-AB (3190) when it coexists with another DSH-AB, DSH-AB (<tag>) when the port is unusable or
# already claimed. The version belongs in DisplayVersion only - a DisplayName carrying it is the exact
# regression this rule removed ('DSH-AB 0.1.0 (DSH-AB 3190)'). Which of the three shapes appears depends on
# what else is installed on this machine, so the shape is asserted, not one literal.
$displayName = (Get-ItemProperty -LiteralPath $EntryKey -Name 'DisplayName').DisplayName
if ($displayName -notmatch '^DSH-AB( \([0-9]+\)| \([0-9a-f]{12}\))?$') {
  throw "卸载登记项的 DisplayName 不是「DSH-AB / DSH-AB (端口) / DSH-AB (tag)」之一：$displayName"
}
if ($displayName.Contains($mf.dshab_version)) {
  throw "卸载登记项的 DisplayName 里出现了版本号 $($mf.dshab_version)：$displayName（版本只该在 DisplayVersion 里）"
}
$displayVersion = (Get-ItemProperty -LiteralPath $EntryKey -Name 'DisplayVersion').DisplayVersion
if ($displayVersion -ne $mf.dshab_version) {
  throw "卸载登记项的 DisplayVersion 不是 $($mf.dshab_version)：$displayVersion"
}
Write-Host "      DisplayName = $displayName（无版本号、括号在名字里），DisplayVersion = $displayVersion"

# A marker in the user data must survive the silent uninstall (the default is keep).
$marker = Join-Path $Dir 'slot-a\data\user-session.txt'
Set-Content -LiteralPath $marker -Value 'keep me' -Encoding ascii
# 光有这个 marker 不够：它是安装之后手写的，安装器从没把自己写进过 [Files]，测不出「安装器把自己
# 放进用户数据的文件一起删掉」。2026-09-23 真机实测到的正是这个漏：卸载后 slot-a\data 从 8 个文件
# 变成 7 个，少的是随包那份 skill（data\skills\<skill>\SKILL.md）。所以这里把安装器放进去的东西
# 全部记下来，卸载后逐个对。
$installedDataFiles = @(Get-ChildItem -LiteralPath (Join-Path $Dir 'slot-a\data') -Recurse -File -Force |
  ForEach-Object { $_.FullName.Substring($Dir.Length) })
Write-Host "      slot-a\data 里的文件（卸载后必须一个不少）：$($installedDataFiles.Count) 个"

# ---- 本安装根下的进程、条目里记的快捷方式路径：两条都要真做一遍 ---------------------------
# 2026-09-22 真机验证：KillScript 生成的 PowerShell 用了不带括号的 if（PowerShell 5.1 里那是
# ParserError，不是「条件为假」），退出码 1 又被调用方读成「匹配到 1 个进程」，日志于是写
# 「结束了本安装根下的进程，共 1 个」而一个都没结束——卸载因此删不掉正在运行的安装；改名或搬过家的
# 快捷方式也留在原地，指向已经删掉的 exe。两件事在这里都真做一遍。
$recordedLinks = @()
foreach ($value in @('DshAbStartMenuLink', 'DshAbDesktopLink')) {
  $p = (Get-ItemProperty -LiteralPath $EntryKey -Name $value -ErrorAction SilentlyContinue).$value
  if (-not $p) { throw "卸载登记项没有 $value：安装期没写，DSH-AB 就找不到自己那条快捷方式" }
  $recordedLinks += $p
}
if (-not (Test-Path -LiteralPath $recordedLinks[0])) {
  throw "条目记的开始菜单快捷方式不存在：$($recordedLinks[0])"
}
Write-Host "      条目记录的两条快捷方式：$($recordedLinks -join ' | ')"

# 替身用随包 node 本体：进程名 node.exe、可执行文件路径在本安装根之下，正是卸载要结束的那一类。
# 它是静默卸载里唯一能证明「结束本安装进程」真生效的东西——不结束它，紧接着的删除就会失败，目录也删不干净。
$standIn = Start-Process -FilePath (Join-Path $Dir 'slot-a\node\node.exe') `
  -ArgumentList '-e', 'setInterval(function(){},1000)' -PassThru -WindowStyle Hidden
$standInDeadline = (Get-Date).AddSeconds(15)
while ((Get-Date) -lt $standInDeadline -and -not (Get-Process -Id $standIn.Id -ErrorAction SilentlyContinue)) {
  Start-Sleep -Milliseconds 200
}
if (-not (Get-Process -Id $standIn.Id -ErrorAction SilentlyContinue)) {
  throw "替身进程没有起来（PID $($standIn.Id)），这一轮证明不了卸载会结束本安装的进程"
}

try {
  $unins = Join-Path $Dir 'unins000.exe'
  Assert-Exists $unins
  $uninstallLog = Join-Path $LogDir 's2-uninstall.log'
  $code = Run-Bounded $unins @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/LOG=$uninstallLog") $TimeoutSec
  if ($code -ne 0) { throw "卸载失败，退出码 $code（日志 $uninstallLog）" }
  Wait-Uninstaller $Dir $TimeoutSec
  if (Get-Process -Id $standIn.Id -ErrorAction SilentlyContinue) {
    throw "静默卸载没有结束本安装根下的进程（PID $($standIn.Id) 还在）：结束进程的那段 PowerShell 又没跑成"
  }
  foreach ($p in $recordedLinks) {
    if (Test-Path -LiteralPath $p) {
      throw "静默卸载没有删掉自己那条快捷方式：$p（要按条目里记的真实路径删）"
    }
  }
  if (Test-Path -LiteralPath (Join-Path $Dir 'DSH_AB.exe')) { throw '卸载后 DSH_AB.exe 还在' }
  if (-not (Test-Path -LiteralPath $marker)) { throw '静默卸载删掉了用户数据（默认必须保留）' }
  $gone = @($installedDataFiles | Where-Object { -not (Test-Path -LiteralPath (Join-Path $Dir $_)) })
  if ($gone.Count -gt 0) { throw "静默卸载删掉了用户数据里的文件（默认必须保留）：$($gone -join '; ')" }
} finally {
  # 替身无论如何都不能留下来：失败路径上它可能还活着，而那正是上面那条断言要报的事。
  if (Get-Process -Id $standIn.Id -ErrorAction SilentlyContinue) {
    Stop-Process -Id $standIn.Id -Force -ErrorAction SilentlyContinue
  }
}
Write-Host '      卸载完成：本安装根下的进程已结束、自己的快捷方式已删除、用户数据保留'

# ---- scenario 2: 卸载「删用户数据=是」这一路（/DELETEUSERDATA=1）--------------------------------
# 卸载器的用户数据询问是 SuppressibleMsgBox(..., MB_DEFBUTTON2, IDNO)：静默卸载自己答的是默认
# 按钮＝「保留」，所以「删」这一路在任何自动化里都走不到——它既没被验证过，也没法被脚本驱动。
# /DELETEUSERDATA=1 是 [Code] 读得到的开关（dsh-ab.iss 的 UserDataDeleteRequested），这条场景真走
# 一遍「删」：槽里的用户数据必须真的没了、安装根不许留下任何东西、卸载登记项也不许留下。
Write-Host '[2/5] 静默卸载 /DELETEUSERDATA=1：槽内用户数据被删、安装根不留东西'
Clear-AndAssertEmpty $Dir
$wipeInstallLog = Join-Path $LogDir 's2-wipe-install.log'
$code = Run-Bounded $Setup @(
  '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/LOG=$wipeInstallLog", "/DIR=$Dir",
  "/PRODUCTION=$silentProductionPort") $TimeoutSec
if ($code -ne 0) { throw "安装失败，退出码 $code（日志 $wipeInstallLog）" }

$wipeMarker = Join-Path $Dir 'slot-a\data\wipe-me.txt'
Set-Content -LiteralPath $wipeMarker -Value 'delete me' -Encoding ascii
$wipeUninstallLog = Join-Path $LogDir 's2-wipe-uninstall.log'
$wipeUnins = Join-Path $Dir 'unins000.exe'
Assert-Exists $wipeUnins
$code = Run-Bounded $wipeUnins @(
  '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/DELETEUSERDATA=1',
  "/LOG=$wipeUninstallLog") $TimeoutSec
if ($code -ne 0) { throw "卸载失败，退出码 $code（日志 $wipeUninstallLog）" }
Wait-Uninstaller $Dir $TimeoutSec
if (Test-Path -LiteralPath $wipeMarker) {
  throw "卸载器没有删掉用户数据（/DELETEUSERDATA=1 没有被 [Code] 读到？）：$wipeMarker"
}
$left = @(Get-ChildItem -LiteralPath $Dir -Recurse -Force -ErrorAction SilentlyContinue)
if ($left.Count -gt 0) {
  throw "「删用户数据」那一路卸载后安装根里还剩 $($left.Count) 项：$($left[0].FullName)"
}
if (Test-Path -LiteralPath $EntryKey) { throw "「删用户数据」那一路卸载后卸载登记项还在：$EntryKey" }
Write-Host '      用户数据已删、安装根已空、卸载登记项已删'

# ---- scenario 3: a non-empty target must be refused, silently, non-zero, without blocking ----
# The port has to be named here even though this scenario is only about the directory: in a silent run
# InitializeSetup refuses an occupied port *before* the wizard ever reaches the non-empty directory
# check (NextButtonClick, wpSelectDir). Without /PRODUCTION the run would carry the installer's
# built-in default (3090 today), which is exactly what the user's own installation holds on this
# machine - the refusal would then come from the port instead of the directory, the log assertion
# below would fail, and the gate would be permanently red. One explicit port keeps the refusal coming
# from the non-empty directory whatever the built-in default becomes.
Write-Host '[3/5] 非空目录 + 静默参数：必须立即以非 0 退出码结束，不弹框'
Clear-Dir $Dir
New-Item -ItemType Directory -Force -Path $Dir | Out-Null
$pre = Join-Path $Dir 'pre-existing.txt'
Set-Content -LiteralPath $pre -Value 'not mine' -Encoding ascii

$refuseLog = Join-Path $LogDir 's3-nonempty.log'
if (Test-Path -LiteralPath $refuseLog) { Remove-Item -LiteralPath $refuseLog -Force }
$code = Run-Bounded $Setup @(
  '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/LOG=$refuseLog", "/DIR=$Dir",
  "/PRODUCTION=$silentProductionPort") $RefuseSec
if ($code -eq 0) { throw '非空目录竟然安装成功了（应该被拒绝）' }
Write-Host "      退出码：$code（非 0，已确认）"
if (-not (Test-Path -LiteralPath $refuseLog)) { throw "没有写日志：$refuseLog" }
if (-not (Select-String -LiteralPath $refuseLog -Pattern 'target directory is not empty' -SimpleMatch -Quiet)) {
  throw "日志里没有拒绝记录：$refuseLog"
}
Assert-Exists $pre
if (Test-Path -LiteralPath (Join-Path $Dir 'DSH_AB.exe')) { throw '非空目录里被装进了文件' }
Write-Host '      已拒绝，日志有记录，已存在的文件原样保留'

# ---- scenario 4: an occupied port must be refused, silently, non-zero, without writing ---------
# This is the user's second check ("填写的端口没有占用就行"), and a silent install has no ports page:
# InitializeSetup is then the only place that can refuse it, and under /SUPPRESSMSGBOXES the log line
# is the only Chinese explanation that reaches anyone - so the exit code, the wording and the
# untouched target directory are all asserted. The listener is held on 127.0.0.1, exactly where the
# installer probes and where dsh listens (a wildcard listener would not be the same test). There is
# one port now, so there is one case.
Write-Host '[4/5] 生产端口被占用 + 静默参数：必须被查，且必须立即以非 0 退出码结束、目标目录不留文件'
Write-Host "      占用生产端口 $silentProductionPort"
Clear-AndAssertEmpty $Dir
$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $silentProductionPort)
$listener.Start()
try {
  $portLog = Join-Path $LogDir 's4-port-taken.log'
  if (Test-Path -LiteralPath $portLog) { Remove-Item -LiteralPath $portLog -Force }
  $code = Run-Bounded $Setup @(
    '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/LOG=$portLog", "/DIR=$Dir",
    "/PRODUCTION=$silentProductionPort") $RefuseSec
  if ($code -eq 0) { throw "生产端口 $silentProductionPort 已被占用，安装却成功了（应该被拒绝）" }
  if (-not (Test-Path -LiteralPath $portLog)) { throw "没有写日志：$portLog" }
  if (-not (Select-String -LiteralPath $portLog -Pattern '已被占用' -SimpleMatch -Quiet)) {
    throw "日志里没有中文的端口占用说明（静默安装时那是唯一的说明）：$portLog"
  }
  $left = @(Get-ChildItem -LiteralPath $Dir -Recurse -Force -ErrorAction SilentlyContinue)
  if ($left.Count -ne 0) { throw "被拒绝的安装仍在目标目录留下了 $($left.Count) 项：$($left[0].FullName)" }
  Write-Host "      已拒绝，退出码 $code，日志有中文说明，目标目录为空"
} finally {
  $listener.Stop()
}

# ---- scenario 5: nothing of ours is left behind ----------------------------------------------
Write-Host '[5/5] 清理：本次运行不得在工作区外留下安装痕迹'
Clear-Dir $Dir
foreach ($p in $Outside) {
  if ($Preexisting[$p]) {
    Write-Host "      运行前就存在，原样保留（不属于本次运行）：$p"
  } elseif (Test-Path -LiteralPath $p) {
    throw "工作区外留下了安装痕迹：$p"
  }
}

# The run is over: put the user's shortcuts and uninstall entry back before claiming success. This
# runs even when a scenario above threw (see the trap) - and it throws if it could not restore.
Restore-UserVisibleState

Write-Output "OK install+uninstall exit=0 dir=$Dir"
