package cli

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/obot-platform/obot/logger"
	"github.com/obot-platform/obot/pkg/obotlogs"
	"github.com/obot-platform/obot/pkg/server"
	"github.com/obot-platform/obot/pkg/services"
	"github.com/spf13/cobra"
)

type Server struct {
	services.Config
}

func (s *Server) Customize(cmd *cobra.Command) {
	cmd.Hidden = true
}

// Pre preserves an explicitly empty proxy environment variable. The command
// library normally ignores empty environment values when applying defaults.
func (s *Server) Pre(cmd *cobra.Command, _ []string) error {
	if value, present := os.LookupEnv("OBOT_SERVER_MODEL_PROXY_URL"); present && value == "" && !cmd.Flags().Changed("model-proxy-url") {
		s.ModelProxyURL = ""
	}

	return nil
}

func (s *Server) Run(cmd *cobra.Command, _ []string) error {
	// Keep recent output in memory for the Infrastructure page and Obot AI. Do it before
	// anything logs, so the buffer sees every line.
	var captureErr error
	if os.Getenv("OBOT_SERVER_DISABLE_LOG_CAPTURE") != "true" {
		captureErr = obotlogs.Capture()
	}

	// Replaces the interactive handler the root command installed. The
	// bridges must follow: they capture the handler installed here.
	logger.Setup(logger.JSON)
	services.SetupLogBridges()
	if captureErr != nil {
		slog.Warn("failed to capture Obot's log output; recent logs won't be available in Obot", "error", captureErr)
	}

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, os.Kill, syscall.SIGTERM)
	defer cancel()

	return server.Run(ctx, s.Config)
}
