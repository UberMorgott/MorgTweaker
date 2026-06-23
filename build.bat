@echo off
REM ===========================================================================
REM  MorgTweaker release build.
REM  Double-click or run from any directory: produces dist\MorgTweaker.exe with
REM  the embedded requireAdministrator manifest, stripped for a smaller release.
REM ===========================================================================

REM Work from the script's own directory so relative paths resolve correctly.
cd /d "%~dp0"

echo === Step 1/3: embedding manifest + version resource (go generate) ===
go generate ./...
if errorlevel 1 (
    echo.
    echo [ERROR] go generate failed.
    echo If goversioninfo is missing, install it with:
    echo     go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
    echo and make sure %%GOPATH%%\bin is on your PATH, then re-run build.bat.
    pause
    exit /b 1
)

REM Release build environment.
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64

if not exist dist mkdir dist

echo.
echo === Step 2/3: building dist\MorgTweaker.exe (windows/amd64, stripped) ===
go build -trimpath -ldflags "-s -w" -o dist\MorgTweaker.exe .\cmd\morgtweaker
if errorlevel 1 (
    echo.
    echo [ERROR] go build failed ^(see error above^).
    pause
    exit /b 1
)

echo.
echo === Step 3/3: done ===
echo Output: %~dp0dist\MorgTweaker.exe
for %%F in (dist\MorgTweaker.exe) do echo Size:   %%~zF bytes
echo.
echo Launch it manually (UAC prompt will appear): dist\MorgTweaker.exe
pause
