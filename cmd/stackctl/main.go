// Command stackctl is the admin tool for self-hosted learningstack servers.
//
// This is the skeletal entrypoint from Anhang A Schritt 1. It only knows
// "version" and a stubbed "web" command. Real functionality lands in
// subsequent steps from stackctl/ARCHITECTURE.md Anhang A.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/tunnel"
	"github.com/lngstck/stackctl/internal/web"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "-v", "--version":
		fmt.Fprintf(stdout, "stackctl %s\n", version)
		return 0
	case "web":
		return cmdWeb(rest, stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "stackctl: unknown command %q\n\n", cmd)
		usage(stderr)
		return 2
	}
}

func cmdWeb(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", "0.0.0.0", "listen host")
	port := fs.Int("port", 8090, "listen port")
	dev := fs.Bool("dev", false, "development mode (templates from disk)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Load or create config.
	cfg, err := config.Load()
	if err != nil {
		log.Printf("No existing config, using defaults: %v", err)
		cfg = config.Default()
	}

	// Load state (empty if missing).
	state, err := config.LoadState()
	if err != nil {
		log.Printf("No existing state, using empty: %v", err)
		state = config.NewState()
	}

	// Tunnel manager.
	tunnelMgr := tunnel.New(cfg, state)
	if cfg.SetupState == config.SetupStateReady {
		if err := tunnel.EnsureKey(); err != nil {
			log.Printf("tunnel: ensure key: %v", err)
		}
		if err := tunnelMgr.EnsureDexTunnel(); err != nil {
			log.Printf("tunnel: dex tunnel: %v", err)
		}
		tunnelMgr.RestoreAppTunnels()
		tunnelMgr.StartMonitor()
	}

	// Build server options.
	var opts []web.Option
	opts = append(opts, web.WithTunnelManager(tunnelMgr))
	if *dev {
		// In dev mode, find the web package dir relative to the binary or CWD.
		webDir := filepath.Join(paths.StackctlDir(), "internal", "web")
		if _, err := os.Stat(webDir); os.IsNotExist(err) {
			// Fallback: try relative to CWD (for development).
			cwd, _ := os.Getwd()
			webDir = filepath.Join(cwd, "internal", "web")
		}
		opts = append(opts, web.WithDevMode(webDir))
		log.Printf("Dev mode: templates from %s", webDir)
	}

	srv, err := web.New(cfg, state, opts...)
	if err != nil {
		fmt.Fprintf(stderr, "stackctl web: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "stackctl %s\n", version)
	if err := srv.ListenAndServe(*host, *port); err != nil {
		fmt.Fprintf(stderr, "stackctl web: %v\n", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "stackctl – learningstack admin tool")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  stackctl <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  version        Print version and exit")
	fmt.Fprintln(w, "  web            Start the admin web UI")
	fmt.Fprintln(w, "  help           Show this help")
}
