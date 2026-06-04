package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codeswhat/lookout/internal/adapter"
	"github.com/codeswhat/lookout/internal/adapter/drydock"
	"github.com/codeswhat/lookout/internal/config"
	"github.com/codeswhat/lookout/internal/docker"
	"github.com/codeswhat/lookout/internal/edge"
	"github.com/codeswhat/lookout/internal/generic"
	applog "github.com/codeswhat/lookout/internal/log"
	"github.com/codeswhat/lookout/internal/protocol"
	"github.com/codeswhat/lookout/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	applog.SetupLogger(cfg.LogLevel)

	slog.Info("starting lookout", "version", protocol.AgentVersion, "mode", modeString(cfg))

	dockerClient, err := docker.NewClient(cfg.DockerSocket, cfg.RequestTimeout)
	if err != nil {
		slog.Error("failed to create docker client", "error", err)
		os.Exit(1)
	}

	version, err := dockerClient.GetVersion(context.Background())
	if err != nil {
		slog.Error("failed to connect to docker", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to docker", "version", version)

	// Select adapter.
	a := selectAdapter(cfg, dockerClient)
	slog.Info("adapter selected", "adapter", a.Name())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if cfg.IsEdgeMode() {
		slog.Info("starting in edge mode", "url", cfg.DrydockURL)
		edgeClient := edge.NewClient(cfg, dockerClient, a)
		go func() {
			<-sigCh
			slog.Info("shutting down...")
			cancel()
		}()
		if err := edgeClient.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("edge client error", "error", err)
			os.Exit(1)
		}
	} else {
		slog.Info("starting in standard mode", "address", cfg.BindAddress+":"+cfg.Port)
		srv := server.NewServer(cfg, dockerClient, a)
		go func() {
			<-sigCh
			slog.Info("shutting down...")
			cancel()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shutdownCancel()
			srv.Shutdown(shutdownCtx)
		}()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}

	slog.Info("lookout stopped")
}

func selectAdapter(cfg *config.Config, dockerClient *docker.Client) adapter.Adapter {
	switch cfg.Adapter {
	case "generic":
		return generic.New()
	case "drydock":
		return drydock.NewAdapter(dockerClient, cfg.AgentName)
	default:
		slog.Warn("unknown adapter, falling back to drydock", "adapter", cfg.Adapter)
		return drydock.NewAdapter(dockerClient, cfg.AgentName)
	}
}

func modeString(cfg *config.Config) string {
	if cfg.IsEdgeMode() {
		return "edge"
	}
	return "standard"
}
