//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("spa-audio runs on Windows only. Build with: GOOS=windows GOARCH=amd64 go build")
	os.Exit(1)
}
