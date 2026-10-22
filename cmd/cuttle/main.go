package main

import (
	"context"
	"github.com/a1knowhow/cuttle/internal/cli"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
