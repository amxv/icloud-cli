package main

import (
	"context"
	"os"

	"github.com/amxv/icloud-cli/internal/app"
)

func main() {
	os.Exit(app.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
