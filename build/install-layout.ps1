<#
  install-layout.ps1 - 「安装根里有哪些文件」这条规则的唯一实现（dot-source 进来用，不单独运行）。

  载荷的目录结构就是安装根的目录结构：installer\dsh-ab.iss 的 [Files] 按原相对路径 1:1 拷进去，
  唯一的例外是 payload-manifest.json（构建记录，从不安装）。三个地方要用同一条规则：

    build\mkpayload.ps1      把这次载荷的文件清单落进 payload-manifest.json（构建期记录）
    build\verify-silent.ps1  拿那份记录比对安装根，并核对载荷在构建后有没有被动过
    build\mkupdate.ps1       比对更新包的文件集合（安装根程序文件去掉槽与用户配置）

  写成一处，而不是每个调用点各写一份路径清单：两边各写一遍必然漂移。2026-09-23 安装器的
  Excludes 少一个反斜杠，45 个文件静默没进安装根，而当时 verify-silent 里那 13 条硬编码路径
  照旧全绿——「应装的文件」只能有一条来源。
#>

# Get-InstallRootFiles 列出 $Root 下所有文件，返回相对路径（不带前导反斜杠）。
# Ignore 是调用方自己的白名单（通配模式）：本就不该出现在目标树里的东西，比如安装器自己生成的
# 卸载程序 unins*、更新包里本来就不带的东西。
function Get-InstallRootFiles {
  [CmdletBinding()]
  param(
    [Parameter(Mandatory = $true)][string]$Root,
    [string[]]$Ignore
  )
  if (-not (Test-Path -LiteralPath $Root)) { throw "找不到目录：$Root" }
  $full = (Get-Item -LiteralPath $Root).FullName
  # payload-manifest.json 是构建记录，从不安装：它出现在安装根里同样是错的。
  $neverInstalled = Join-Path $full 'payload-manifest.json'
  @(Get-ChildItem -LiteralPath $full -Recurse -File -Force |
    Where-Object { $_.FullName -ne $neverInstalled } |
    ForEach-Object { $_.FullName.Substring($full.Length + 1) } |
    Where-Object {
      $rel = $_
      -not ($Ignore | Where-Object { $rel -like $_ })
    })
}

# Compare-FileSet 比两个相对路径集合，返回 @{ Missing; Unexpected }（都是数组，可能为空）。
# 用 HashSet 而不是 -contains：安装根近 3 万个文件，逐项线性查找会跑到天荒地老。
# Windows 的路径不分大小写，所以两侧都用 OrdinalIgnoreCase。
function Compare-FileSet {
  [CmdletBinding()]
  param(
    [string[]]$Expected,
    [string[]]$Actual
  )
  $expectedSet = [System.Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
  foreach ($f in $Expected) { [void]$expectedSet.Add($f) }
  $actualSet = [System.Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
  foreach ($f in $Actual) { [void]$actualSet.Add($f) }
  [pscustomobject]@{
    Missing    = @($Expected | Where-Object { -not $actualSet.Contains($_) })
    Unexpected = @($Actual | Where-Object { -not $expectedSet.Contains($_) })
  }
}

# Format-FileSetDiff 把差异写成人看得懂的一行（数量 + 前几个），报错时用。
function Format-FileSetDiff {
  [CmdletBinding()]
  param([string[]]$Files)
  $head = @($Files | Select-Object -First 5)
  $line = ($head -join '; ')
  if ($Files.Count -gt $head.Count) { $line += "; …（共 $($Files.Count) 个）" }
  return $line
}
