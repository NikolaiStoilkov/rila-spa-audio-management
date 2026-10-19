//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("log-viewer runs on Windows only. Build with: ./build.sh")
	os.Exit(1)
}
