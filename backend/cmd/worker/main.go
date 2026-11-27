package main

import (
	"fmt"
	"os"

	"hello-world/internal/app"
)

func main() {
	if err := app.RunWorker(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
