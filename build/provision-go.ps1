<#
  provision-go.ps1 - 把 Go 工具链装到 <仓库>\tools\go\bin\go.exe 上。

  build\build.ps1 与 CI 都按 <仓库>\tools\go\bin\go.exe 找 Go，而 tools\ 是 git-ignored，所以每个
  CI job 都得现装一次。这段以前在 workflow 里逐字写了两遍（build job 与 update-pack job），版本
  pin 因此有两处会漂；抽成一个脚本之后 pin 只有这里一处，而且本地也能直接语法检查（workflow 里的
  run 块本地跑不了）。

  只做这一件事：下载官方 zip、解压、把 go\ 放到 tools\go，最后打印 go version。不装 Inno Setup
  （只有 build job 需要它，那一行留在 workflow 里：它要装到 %LOCALAPPDATA%，与 Go 的落点无关）。
#>
[CmdletBinding()]
param(
  # 与 .github\workflows\build-dsh-ab.yml 里 test job 的 actions/setup-go 保持同一版。
  [string]$GoVersion = '1.24.0'
)
$ErrorActionPreference = 'Stop'

$RepoRoot = Split-Path -Parent $PSScriptRoot
$TempDir = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { $env:TEMP }
$ToolsDir = Join-Path $RepoRoot 'tools'

$goZip = Join-Path $TempDir "go$GoVersion.windows-amd64.zip"
Invoke-WebRequest -Uri "https://go.dev/dl/go$GoVersion.windows-amd64.zip" -OutFile $goZip -UseBasicParsing
$goExtract = Join-Path $TempDir 'go-extract'
Remove-Item -Recurse -Force $goExtract -ErrorAction SilentlyContinue
Expand-Archive -LiteralPath $goZip -DestinationPath $goExtract
Remove-Item -Recurse -Force (Join-Path $ToolsDir 'go') -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $ToolsDir | Out-Null
Move-Item -LiteralPath (Join-Path $goExtract 'go') -Destination (Join-Path $ToolsDir 'go')
& (Join-Path $ToolsDir 'go\bin\go.exe') version
if ($LASTEXITCODE -ne 0) { throw "装好的 Go 跑不起来：$(Join-Path $ToolsDir 'go\bin\go.exe')" }
