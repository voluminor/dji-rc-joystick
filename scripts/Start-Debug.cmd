@echo off
cd /d "%~dp0"
set DJI_RC_GAMEPAD=xbox
set GOTRACEBACK=all
dji-rc-debug.exe --config "%~dp0debug-settings.json"
echo Application exited with code %errorlevel%.
pause
