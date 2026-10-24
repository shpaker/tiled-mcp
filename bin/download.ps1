# Downloads the tiled-mcp release binary for bin\tiled-mcp.cmd into -Dir and
# checks its sha256 against the release's checksums.txt, as bin/tiled-mcp does
# on macOS and Linux.
#
# Windows' own curl.exe and tar.exe (Windows 10 1803 and later) do the work:
# GNU tar from Git Bash, if it comes first in PATH, reads C:\ as a remote host.
param(
  [Parameter(Mandatory)] [string] $Version,
  [Parameter(Mandatory)] [string] $Arch,
  [Parameter(Mandatory)] [string] $Dir
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
# One line per failure on stderr, as from bin/tiled-mcp, not a PowerShell trace.
trap {
  [Console]::Error.WriteLine($_.Exception.Message)
  exit 1
}

$repository = 'shpaker/tiled-mcp'
$curl = Join-Path $env:SystemRoot 'System32\curl.exe'
$tar = Join-Path $env:SystemRoot 'System32\tar.exe'
$archive = "tiled-mcp_${Version}_windows_${Arch}.tar.gz"
# TILED_MCP_RELEASE_URL points the launcher at a local build when testing.
$url = $env:TILED_MCP_RELEASE_URL
if (-not $url) {
  $url = "https://github.com/$repository/releases/download/v$Version"
}

$work = Join-Path ([IO.Path]::GetTempPath()) ('tiled-mcp-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $work | Out-Null
try {
  [Console]::Error.WriteLine("tiled-mcp: downloading $archive")
  foreach ($name in $archive, 'checksums.txt') {
    & $curl -fsSL --retry 3 -o (Join-Path $work $name) "$url/$name"
    if ($LASTEXITCODE -ne 0) { throw "tiled-mcp: cannot download $url/$name" }
  }

  $expected = Get-Content (Join-Path $work 'checksums.txt') |
    ForEach-Object { $fields = -split $_; if ($fields[1] -eq $archive) { $fields[0] } } |
    Select-Object -First 1
  # .NET rather than Get-FileHash, which needs a module PowerShell may not find.
  $stream = [IO.File]::OpenRead((Join-Path $work $archive))
  try {
    $actual = -join ([Security.Cryptography.SHA256]::Create().ComputeHash($stream) | ForEach-Object { $_.ToString('x2') })
  } finally {
    $stream.Close()
  }
  if (-not $expected -or $expected -ne $actual) {
    throw "tiled-mcp: checksum mismatch for $archive"
  }

  & $tar -xzf (Join-Path $work $archive) -C $work
  if ($LASTEXITCODE -ne 0) { throw "tiled-mcp: cannot unpack $archive" }

  New-Item -ItemType Directory -Force -Path $Dir | Out-Null
  $binary = Join-Path $Dir 'tiled-mcp.exe'
  # Several agent sessions may start at once: move into place atomically. A
  # binary that is already there came from one of them.
  $partial = "$binary.$PID"
  Move-Item -Force (Join-Path $work 'tiled-mcp.exe') $partial
  try {
    [IO.File]::Move($partial, $binary)
  } catch {
    Remove-Item -Force $partial
    if (-not (Test-Path $binary)) { throw }
  }
} finally {
  Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
