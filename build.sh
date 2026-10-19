#!/bin/sh
# Builds dist/spa-audio.exe and dist/log-viewer.exe for 64-bit Windows
# (works from macOS/Linux/Windows with Git Bash).
set -e
cd "$(dirname "$0")"
WINRES="go run github.com/tc-hib/go-winres@latest"
$WINRES make --arch amd64,arm64 --in winres/winres.json --out rsrc
$WINRES make --arch amd64,arm64 --in winres/logviewer.json --out cmd/logviewer/rsrc
export GOOS=windows GOARCH=amd64
go build -ldflags "-H=windowsgui -s -w" -o dist/spa-audio.exe .
go build -ldflags "-H=windowsgui -s -w" -o dist/log-viewer.exe ./cmd/logviewer
echo "Built dist/spa-audio.exe and dist/log-viewer.exe"
