package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/adapter/drydock"
	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/edge"
	"github.com/codeswhat/portwing/internal/generic"
	applog "github.com/codeswhat/portwing/internal/log"
	"github.com/codeswhat/portwing/internal/protocol"
	"github.com/codeswhat/portwing/internal/server"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "hash-token" {
		runHashToken()
		return
	}

	if len(os.Args) >= 2 && os.Args[1] == "keygen" {
		runKeygen(os.Args[2:])
		return
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	applog.SetupLogger(cfg.LogLevel)

	slog.Info("starting portwing", "version", protocol.AgentVersion, "mode", modeString(cfg))

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
		auditor, auditClose, err := audit.New(cfg.AuditLog)
		if err != nil {
			slog.Error("failed to open audit log", "error", err)
			os.Exit(1)
		}
		defer auditClose()
		edgeClient := edge.NewClient(cfg, dockerClient, a, auditor)
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
		srv, err := server.NewServer(cfg, dockerClient, a)
		if err != nil {
			slog.Error("failed to create server", "error", err)
			os.Exit(1)
		}
		go func() {
			<-sigCh
			slog.Info("shutting down...")
			cancel()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shutdownCancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				slog.Warn("server shutdown error", "error", err)
			}
		}()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}

	slog.Info("portwing stopped")
}

func selectAdapter(cfg *config.Config, dockerClient *docker.Client) adapter.Adapter {
	switch cfg.Adapter {
	case "generic":
		return generic.New(dockerClient, cfg.AgentName)
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

// runKeygen generates an Ed25519 keypair and prints the private key in PKCS#8
// PEM format and the authorized_keys line to stdout. Prompts are written to
// stderr so stdout output is unambiguous and pipe-friendly.
//
// Usage:
//
//	portwing keygen [-comment <text>]
//	portwing keygen -pub-from <private.pem> [-comment <text>]
func runKeygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	comment := fs.String("comment", "", "Comment to embed in the authorized_keys line (optional)")
	pubFrom := fs.String("pub-from", "", "Re-derive the authorized_keys line from an existing private key PEM file")

	// Print usage to stderr.
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: portwing keygen [-comment <text>]")
		fmt.Fprintln(os.Stderr, "       portwing keygen -pub-from <private.pem> [-comment <text>]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Generates an Ed25519 keypair for use with AUTHORIZED_KEYS authentication.")
		fmt.Fprintln(os.Stderr, "The private key (PEM PKCS#8) and authorized_keys line are written to stdout.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if *pubFrom != "" {
		// Re-derive the authorized_keys line from an existing private key.
		priv, err := auth.LoadPrivateKey(*pubFrom)
		if err != nil {
			fmt.Fprintf(os.Stderr, "keygen: %v\n", err)
			os.Exit(1)
		}
		pub := priv.Public().(ed25519.PublicKey)
		line := auth.AuthorizedKeyLine(pub, *comment)
		fmt.Fprintln(os.Stderr, "# authorized_keys line (add to AUTHORIZED_KEYS file on agent host):")
		fmt.Println(line)
		return
	}

	// Generate a new keypair.
	fmt.Fprintln(os.Stderr, "Generating Ed25519 keypair...")
	privPEM, authKeyLine, err := auth.GenerateKeyPair(*comment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "keygen: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr, "# Private key (PKCS#8 PEM) — store securely; set as PRIVATE_KEY_FILE on the client:")
	fmt.Print(string(privPEM))
	fmt.Fprintln(os.Stderr, "# authorized_keys line — add to AUTHORIZED_KEYS file on agent host:")
	fmt.Println(authKeyLine)
}

// runHashToken reads a token from stdin, hashes it with Argon2id, and prints
// the resulting PHC string. The result can be stored as TOKEN_HASH.
func runHashToken() {
	fmt.Fprint(os.Stderr, "Enter token: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "error reading input: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "no input provided")
		}
		os.Exit(1)
	}
	token := strings.TrimSpace(scanner.Text())
	if token == "" {
		fmt.Fprintln(os.Stderr, "token must not be empty")
		os.Exit(1)
	}
	phc, err := server.HashToken(token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash-token: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(phc)
}
