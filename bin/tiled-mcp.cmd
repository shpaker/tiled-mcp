@echo off
rem Starts tiled-mcp for the agent on Windows; bin/tiled-mcp does the same on
rem macOS and Linux, and agents find this file for the same command through
rem PATHEXT. On first use bin\download.ps1 downloads the release binary
rem matching this plugin's VERSION into %USERPROFILE%\.cache\tiled-mcp\<version>\
rem and checks its sha256 against the release's checksums.txt.
rem
rem TILED_MCP_DEV=1 builds from this checkout instead (needs Go).
rem
rem Standard output belongs to the MCP protocol: all messages go to stderr.
setlocal

for %%i in ("%~dp0..") do set "root=%%~fi"
for /f "usebackq" %%v in ("%root%\VERSION") do set "version=%%v"

if "%TILED_MCP_DEV%"=="1" goto dev

set "arch=%PROCESSOR_ARCHITEW6432%"
if not defined arch set "arch=%PROCESSOR_ARCHITECTURE%"
if /i "%arch%"=="AMD64" (
  set "arch=amd64"
) else if /i "%arch%"=="ARM64" (
  set "arch=arm64"
) else (
  echo tiled-mcp: unsupported architecture %arch% 1>&2
  exit /b 1
)

if defined TILED_MCP_CACHE (
  set "cache=%TILED_MCP_CACHE%"
) else if defined XDG_CACHE_HOME (
  set "cache=%XDG_CACHE_HOME%\tiled-mcp"
) else (
  set "cache=%USERPROFILE%\.cache\tiled-mcp"
)
set "dir=%cache%\%version%"
set "binary=%dir%\tiled-mcp.exe"

if exist "%binary%" goto run
rem Standard input is the agent's too: PowerShell must not read from it.
rem PSModulePath inherited from PowerShell 7 hides Windows PowerShell's own
rem modules.
set "PSModulePath="
powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "%~dp0download.ps1" -Version "%version%" -Arch "%arch%" -Dir "%dir%" <nul 1>&2
if errorlevel 1 exit /b 1
goto run

:dev
set "binary=%root%\.build\tiled-mcp.exe"
pushd "%root%"
go build -o "%binary%" ./cmd/tiled-mcp 1>&2
if errorlevel 1 exit /b 1
popd

:run
"%binary%" %*
exit /b %errorlevel%
