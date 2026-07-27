package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ahmedelgabri/ccpeek/internal/cmd"
)

func main() {
	// SIGINT/SIGTERM cancel the command context instead of hard-killing
	// the process: the HTTP server shuts down gracefully, and an index
	// pass stops at its next checkpoint keeping every source it has
	// already committed. This is the binary's ONLY signal registration —
	// commands derive their context from cmd.Context().
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, restore the default disposition so a second
	// one kills a shutdown that is taking too long — NotifyContext keeps
	// swallowing signals otherwise.
	go func() {
		<-ctx.Done()
		stop()
	}()
	cmd.ExecuteContext(ctx)
}
