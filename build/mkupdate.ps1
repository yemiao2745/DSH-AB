<#
  mkupdate.ps1 - assemble the in-place update pack (a build product; never committed).

  The pack is the installation root's program files, minus the two slots, minus the user's
  configuration and minus the ledgers. Overwriting the installation root with it is the whole update:

    DSH_AB.exe  README.md  LICENSES.txt  .gitignore   runtime\git\...

  Not in the pack: any slot, dsh-ab.toml (the user's ports and log settings), docs\ and
  payload-manifest.json (the build record).
  Why docs\ stays out: PLUGINS.md and TODO.md are what *this installation* has accumulated round after
  round, while the copy in the payload is only the seed template - overwriting them with it would wipe
  that record. They live in the installation root for as long as it does, and baking the installation
  root into them is the installer's job, for a fresh installation.
  The pack carries no update script either: overwriting the installation root is what the AI
  maintaining this installation does, by the rules in the skill under build\templates\skills\ (snapshot
  first, clear the old program files, then overwrite; a rollback is that snapshot).
  dsh itself lives inside the slots, so the pack does not depend on the dsh version it was built next
  to: one pack per DSH-AB version.
  The exe in the pack is the installer's own (build\build.ps1 passes it as -AppExe; a release build is
  build\<product>.exe, i.e. the payload's DSH_AB.exe): one source, built exactly once. The end of this
  script still runs its --version and asserts it reports this DSH-AB version.
#>
[CmdletBinding()]
param(
  [string]$Payload,
  [string]$AppExe,
  [string]$OutDir,
  [string]$ProductName = 'DSH-AB',
  [string]$DshabVersion
)
$ErrorActionPreference = 'Stop'

$RepoRoot = Split-Path -Parent $PSScriptRoot
if (-not $Payload)  { $Payload  = Join-Path $RepoRoot 'payload' }
if (-not $AppExe)   { $AppExe   = Join-Path $Payload 'DSH_AB.exe' }
if (-not $OutDir)   { $OutDir   = Join-Path $RepoRoot 'dist' }
if (-not $DshabVersion) {
  $DshabVersion = (Get-Content -LiteralPath (Join-Path $RepoRoot 'src\dsh-ab\VERSION') -Raw).Trim()
}
if (-not $DshabVersion) { throw 'src\dsh-ab\VERSION 是空的' }

# 「安装根里应有哪些文件」这条规则与 build\verify-silent.ps1 共用一份实现（install-layout.ps1）。
. (Join-Path $PSScriptRoot 'install-layout.ps1')

function Assert-Path([string]$Path, [string]$What) {
  if (-not (Test-Path -LiteralPath $Path)) { throw "$What not found: $Path" }
}
Assert-Path $AppExe 'DSH_AB.exe'
Assert-Path (Join-Path $Payload 'README.md') 'payload'

$Scratch = Join-Path $RepoRoot '.tmp-update'
$Stage   = Join-Path $Scratch 'stage'
$Zip     = Join-Path $OutDir "$ProductName-$DshabVersion-update.zip"

try {
  if (Test-Path -LiteralPath $Scratch) { Remove-Item -LiteralPath $Scratch -Recurse -Force }
  New-Item -ItemType Directory -Force -Path $Stage | Out-Null

  # exe 用调用方给的那一份：默认就是载荷里那一份（安装包用的 build\<产品>.exe 的副本），测试构建给
  # build\DSH_ABtest.exe。装进安装根的名字永远是 DSH_AB.exe。
  Copy-Item -LiteralPath $AppExe -Destination (Join-Path $Stage 'DSH_AB.exe')
  foreach ($f in @('README.md', 'LICENSES.txt', '.gitignore')) {
    Assert-Path (Join-Path $Payload $f) 'payload'
    Copy-Item -LiteralPath (Join-Path $Payload $f) -Destination (Join-Path $Stage $f)
  }
  # runtime\ 整套（随包 Git）。它就是"程序文件"里最大的那一块，槽里没有它。
  Copy-Item -LiteralPath (Join-Path $Payload 'runtime') -Destination (Join-Path $Stage 'runtime') -Recurse

  # One fixed timestamp for everything in the pack. The payload files already carry it (mkpayload.ps1
  # normalizes them, and Copy-Item keeps what it copied), but the exe does not: it comes straight from
  # build\<product>.exe, which go build wrote moments ago, while the installer's copy of that same exe
  # goes through the payload and is normalized there. Both ISCC and ZipFile record source timestamps,
  # so that one fresh mtime was enough to make two builds of the same commit produce byte-different
  # packs - measured 2026-09-24: installer 968B22AE... both times, pack 3CA1567B... then 7F1DE71B...
  # Same instant as mkpayload.ps1 uses, so the pack's exe matches the installer's copy byte for byte.
  $PackTime = [datetime]::new(2000, 1, 1, 0, 0, 0, [DateTimeKind]::Utc)
  Get-ChildItem -LiteralPath $Stage -Recurse -File -Force | ForEach-Object { $_.LastWriteTimeUtc = $PackTime }
  Get-ChildItem -LiteralPath $Stage -Recurse -Directory -Force | ForEach-Object { $_.LastWriteTimeUtc = $PackTime }

  # Four things have to hold, or the pack breaks the target machine: the entry point is in it, no slot
  # is in it, no ledger is in it, and "the pack's file set == the installation root's program files -
  # the slots - the user's configuration - the ledgers".
  Assert-Path (Join-Path $Stage 'DSH_AB.exe') 'staged DSH_AB.exe'
  # 上面那段注释是给人看的说明，这里才是真正的清单：两边都从载荷推出来（同一条规则，见
  # install-layout.ps1），包里少一个程序文件、多带一个槽里的文件、dsh-ab.toml 或 docs\ 里的台账
  # 都失败（台账是这份安装自己累计的，包里那份只是模板）。
  $diff = Compare-FileSet (Get-InstallRootFiles $Payload -Ignore 'slot-*', 'dsh-ab.toml', 'docs\*') (Get-InstallRootFiles $Stage)
  if ($diff.Missing.Count -gt 0) {
    throw "更新包少了 $($diff.Missing.Count) 个安装根程序文件：$(Format-FileSetDiff $diff.Missing)"
  }
  if ($diff.Unexpected.Count -gt 0) {
    throw "更新包里多了 $($diff.Unexpected.Count) 个不该带的文件（槽、dsh-ab.toml、docs\ 里的台账、构建记录都不在包里）：$(Format-FileSetDiff $diff.Unexpected)"
  }
  $slots = @(Get-ChildItem -LiteralPath $Stage -Directory -Force | Where-Object { $_.Name -like 'slot-*' })
  if ($slots.Count -gt 0) { throw "更新包里不该有槽：$($slots.Name -join ', ')" }
  # 包里那份 exe 必须报得出 DSH-AB 自己的版本：它就是这一版的程序，报错版本等于发错东西。
  # （以前这里还断言它报不出 dsh 版本——那是「更新包单独编一份 dshVersion 为空的 exe」时代的约束。
  # 现在包里那份 exe 与安装包那份逐字节相同，那条约束连它守着的分支一起没了。）
  $versionTmp = Join-Path $Scratch 'pack-version.txt'
  if (Test-Path -LiteralPath $versionTmp) { Remove-Item -LiteralPath $versionTmp -Force }
  Start-Process -FilePath (Join-Path $Stage 'DSH_AB.exe') -ArgumentList '--version' -NoNewWindow -Wait -RedirectStandardOutput $versionTmp | Out-Null
  $reported = (Get-Content -LiteralPath $versionTmp -Raw).Trim()
  if ($reported -notmatch [regex]::Escape($DshabVersion)) {
    throw "更新包里那份 DSH_AB.exe 没报出 DSH-AB 版本 $DshabVersion（实际：$reported）"
  }
  Write-Host "        包里的 exe --version: $reported"

  New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
  if (Test-Path -LiteralPath $Zip) { Remove-Item -LiteralPath $Zip -Force }
  # ZipFile，不是 Compress-Archive：后者会把 .gitignore 这种隐藏文件漏掉。
  if (-not ('System.IO.Compression.ZipFile' -as [type])) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
  }
  [System.IO.Compression.ZipFile]::CreateFromDirectory(
    $Stage, $Zip, [System.IO.Compression.CompressionLevel]::Optimal, $false)

  $files = @(Get-ChildItem -LiteralPath $Stage -Recurse -File -Force)
  $mb = [math]::Round((Get-Item $Zip).Length / 1MB, 1)
  $sha = (Get-FileHash -LiteralPath $Zip -Algorithm SHA256).Hash
  Write-Host "更新包：$Zip"
  Write-Host "        $($files.Count) 个文件，$mb MB"
  Write-Host "        顶层：$((Get-ChildItem -LiteralPath $Stage -Force | Select-Object -ExpandProperty Name) -join '  ')"
  Write-Host "        （不含任何槽、不含 dsh-ab.toml、不含 docs\ 里的台账）"
  Write-Host "        sha256: $sha"
} finally {
  if (Test-Path -LiteralPath $Scratch) { Remove-Item -LiteralPath $Scratch -Recurse -Force -ErrorAction SilentlyContinue }
}
