package main

import (
	"context"
	"os"

	"github.com/amxv/icloud-cli/internal/app"
)

func main() {
	os.Exit(app.RunWithInput(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
