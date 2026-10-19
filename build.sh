#!/bin/sh
# Builds dist/spa-audio.exe for 64-bit Windows (works from macOS/Linux/Windows with Git Bash).
set -e
cd "$(dirname "$0")"
go run github.com/tc-hib/go-winres@latest make --arch amd64
GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui -s -w" -o dist/spa-audio.exe .
echo "Built dist/spa-audio.exe"
