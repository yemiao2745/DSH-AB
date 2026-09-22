<#
  mkupdate.ps1 - 组装「就地更新包」zip（构建产物，从不提交）。

  更新包就是安装根里除了两个槽以外的程序文件，覆盖到目标机上就完成就地更新：

    DSH_AB.exe  README.md  LICENSES.txt  .gitignore
    docs\{PLUGINS.md,TODO.md}   还带着 {{DSH_AB_ROOT}} 占位符，装到目标机上后要替换成真实安装根
    runtime\git\...             随包整套 Git

  不在包里：任何槽、dsh-ab.toml（用户的端口与日志设置）、payload-manifest.json（构建记录）。
  包里也没有任何更新脚本：把包覆盖到安装根这件事由维护这套安装的 AI 按 build\templates\skills\ 里
  那份 skill 的流程自己做（先 git 快照、再删目录、再覆盖、再烧入占位符；回退就是取回那次快照）。
  dsh 本身在槽里，所以包与它旁边一起构建的那个 dsh 版本无关：一个 DSH-AB 版本只有一份更新包。
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

  # exe 用调用方给的那一份：测试构建给的是 build\DSH_ABtest.exe，装进安装根的名字永远是 DSH_AB.exe。
  Copy-Item -LiteralPath $AppExe -Destination (Join-Path $Stage 'DSH_AB.exe')
  foreach ($f in @('README.md', 'LICENSES.txt', '.gitignore')) {
    Assert-Path (Join-Path $Payload $f) 'payload'
    Copy-Item -LiteralPath (Join-Path $Payload $f) -Destination (Join-Path $Stage $f)
  }
  Copy-Item -LiteralPath (Join-Path $Payload 'docs') -Destination (Join-Path $Stage 'docs') -Recurse
  # runtime\ 整套（随包 Git）。它就是"程序文件"里最大的那一块，槽里没有它。
  Copy-Item -LiteralPath (Join-Path $Payload 'runtime') -Destination (Join-Path $Stage 'runtime') -Recurse

  # 三件事必须成立，否则这个包装到目标机上会坏：有入口程序、一个槽都不带、占位符还是占位符。
  Assert-Path (Join-Path $Stage 'DSH_AB.exe') 'staged DSH_AB.exe'
  $slots = @(Get-ChildItem -LiteralPath $Stage -Directory -Force | Where-Object { $_.Name -like 'slot-*' })
  if ($slots.Count -gt 0) { throw "更新包里不该有槽：$($slots.Name -join ', ')" }
  foreach ($ledger in @('docs\PLUGINS.md', 'docs\TODO.md')) {
    $text = Get-Content -LiteralPath (Join-Path $Stage $ledger) -Raw
    if ($text -notlike '*{{DSH_AB_ROOT}}*') { throw "$ledger 里的 {{DSH_AB_ROOT}} 占位符不在了：更新包必须原样带着它，装到目标机上再替换成真实安装根" }
  }

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
  Write-Host "        （不含任何槽、不含 dsh-ab.toml）"
  Write-Host "        sha256: $sha"
} finally {
  if (Test-Path -LiteralPath $Scratch) { Remove-Item -LiteralPath $Scratch -Recurse -Force -ErrorAction SilentlyContinue }
}
