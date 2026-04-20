// Command stackctl is the admin tool for self-hosted learningstack servers.
//
// This is the skeletal entrypoint from Anhang A Schritt 1. It only knows
// "version" and a stubbed "web" command. Real functionality lands in
// subsequent steps from stackctl/ARCHITECTURE.md Anhang A.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/secrets"
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
	case "hashpw":
		return cmdHashpw(rest, stdout, stderr)
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

// cmdHashpw reads a password from -p, argv, or stdin and prints a bcrypt
// hash suitable for config.yaml's admin.password_hash field. Intended as a
// recovery tool when the admin has lost web-UI access.
func cmdHashpw(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hashpw", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pwFlag := fs.String("p", "", "password (reads stdin if empty)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: stackctl hashpw [-p password]")
		fmt.Fprintln(stderr, "Without -p, the password is read from stdin (one line).")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	pw := *pwFlag
	if pw == "" {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintf(stderr, "stackctl hashpw: read stdin: %v\n", err)
			return 1
		}
		pw = strings.TrimRight(line, "\r\n")
	}
	if pw == "" {
		fmt.Fprintln(stderr, "stackctl hashpw: empty password")
		return 1
	}

	hash, err := secrets.HashPassword(pw)
	if err != nil {
		fmt.Fprintf(stderr, "stackctl hashpw: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, hash)
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
	fmt.Fprintln(w, "  hashpw         Print a bcrypt hash for admin.password_hash")
	fmt.Fprintln(w, "  help           Show this help")
}
