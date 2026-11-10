package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	_ "time/tzdata"

	"github.com/obot-platform/cmd"
	"github.com/obot-platform/obot/pkg/cli"
	"github.com/obot-platform/obot/pkg/obotlogs"
)

func main() {
	// Don't shutdown on SIGTERM, only on SIGINT. SIGTERM is handled by the controller leader election
	cmd.ShutdownSignals = []os.Signal{os.Interrupt}
	root := cli.New()
	err := root.ExecuteContext(cmd.SetupSignalContext())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || cli.ErrorAlreadyReported(err) {
			obotlogs.Stop()
			os.Exit(1)
		}
		// Not stdlib log: its slog bridge would demote this to INFO with no
		// source, and this is the process's last word.
		slog.Error("exiting", "error", err)
	}
	// Flush output the server's log capture still holds before the process ends.
	obotlogs.Stop()
	if err != nil {
		os.Exit(1)
	}
}
