@echo off
rem Switch the console to UTF-8 for correct Unicode output.
chcp 65001 >nul 2>&1
rem ARTEX supervisor launcher (Windows)
rem
rem Usage:
rem   start.bat                  Run in foreground (Ctrl-C to stop)
rem   start.bat -addr :9000      Pass additional arguments unchanged to artex
rem
rem Starts artex.exe and decides whether to restart it based on its exit code.
rem
rem   0      Normal user stop    -> Exit loop
rem   75     Restart requested   -> Restart immediately (UI update or rollback)
rem   Other  Crash                -> Restart after backoff (1->2->4... up to 60 seconds)
rem
rem Downloads, SHA256 verification and replacement are performed by artex on startup
rem (selfupdate package). Keep this script simple; see the comments at the top of start.sh.

setlocal enabledelayedexpansion
cd /d "%~dp0"

if not defined ARTEX_SETUP_TOKEN if exist ".setup-token" set /p ARTEX_SETUP_TOKEN=<".setup-token"

set "BIN=artex.exe"
if not exist "%BIN%" (
	echo [artex] Executable not found: %BIN% 1>&2
	exit /b 1
)

set "RESTART_CODE=75"
set "MAX_DELAY=60"
set /a delay=1

:loop
"%BIN%" %*
set "code=!ERRORLEVEL!"

if "!code!"=="0" (
	echo [artex] Exited normally
	exit /b 0
)

if "!code!"=="%RESTART_CODE%" (
	rem Update/rollback ready: artex applies replacement on restart.
	echo [artex] Restart requested (apply new version)...
	set /a delay=1
	goto loop
)

echo [artex] Abnormal exit ^(code=!code!^); restarting in !delay!s 1>&2
rem timeout fails in redirected consoles; use ping as fallback (N seconds require N+1 requests).
set /a pings=!delay!+1
ping -n !pings! 127.0.0.1 >nul 2>&1
set /a delay=!delay!*2
if !delay! gtr %MAX_DELAY% set /a delay=%MAX_DELAY%
goto loop
