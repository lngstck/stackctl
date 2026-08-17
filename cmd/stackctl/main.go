// Command stackctl is the admin tool for self-hosted learningstack servers.
//
// This is the skeletal entrypoint from Anhang A Schritt 1. It only knows
// "version" and a stubbed "web" command. Real functionality lands in
// subsequent steps from stackctl/ARCHITECTURE.md Anhang A.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/dex"
	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/install"
	"github.com/lngstck/stackctl/internal/lock"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/publish"
	"github.com/lngstck/stackctl/internal/secrets"
	"github.com/lngstck/stackctl/internal/update"
	"github.com/lngstck/stackctl/internal/web"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// Propagate to the update package so CurrentVersion() reflects the
	// actual running binary, not just whatever stackctl.version says.
	update.Version = version
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
	case "autoupdate":
		return cmdAutoupdate(rest, stdout, stderr)
	case "llm":
		return cmdLLM(rest, stdout, stderr)
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

	// Publisher — how this install is reachable from the internet. Which
	// implementation that is follows from config.public.transport; nothing
	// above this line branches on it.
	publisher := publish.For(cfg)
	if cfg.SetupState == config.SetupStateReady {
		publish.Bootstrap(publisher, state, catalog.ContainerPort, *port)
	}

	// Build server options.
	var opts []web.Option
	opts = append(opts, web.WithPublisher(publisher), web.WithListenPort(*port))
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
	fmt.Fprintln(w, "  autoupdate     Sync catalog and install non-breaking app updates")
	fmt.Fprintln(w, "  llm            Manage the local LLM gateway (providers, personas, keys)")
	fmt.Fprintln(w, "  help           Show this help")
}

// cmdAutoupdate runs a single auto-update cycle:
//  1. Syncs the catalog.
//  2. Computes available updates against state.
//  3. For each app: skips breaking + per-app opt-outs, otherwise install.Update.
//
// Exits 0 even with per-app failures so the systemd timer keeps running. The
// global on/off switch lives in cfg.AutoUpdate.Enabled and is checked first;
// the command is also safe to invoke manually (the global switch is bypassed
// with -force).
func cmdAutoupdate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("autoupdate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "ignore the global auto_update.enabled flag")
	dryRun := fs.Bool("dry-run", false, "only list available updates, do not install")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "autoupdate: load config: %v\n", err)
		return 1
	}
	state, err := config.LoadState()
	if err != nil {
		fmt.Fprintf(stderr, "autoupdate: load state: %v\n", err)
		return 1
	}

	if cfg.SetupState != config.SetupStateReady {
		fmt.Fprintln(stdout, "autoupdate: setup not complete, skipping")
		return 0
	}
	if !cfg.AutoUpdate.Enabled && !*force {
		fmt.Fprintln(stdout, "autoupdate: disabled in settings, skipping")
		return 0
	}

	// Serialise the mutating run against the web server. A dry-run only lists
	// updates (no state/.env/compose writes), so it does not take the lock.
	if !*dryRun {
		h, err := lock.Acquire()
		if err != nil {
			if errors.Is(err, lock.ErrBusy) {
				fmt.Fprintln(stdout, "autoupdate: another operation is running (web UI), skipping this cycle")
				return 0
			}
			fmt.Fprintf(stderr, "autoupdate: acquire lock: %v\n", err)
			return 1
		}
		defer h.Release()
	}

	if _, err := catalog.Sync(cfg.Catalog.URL); err != nil {
		// Refresh-Fehler einzelner Definitionen sind nicht fatal — Sync hat
		// den Index erfolgreich aktualisiert, wir laufen weiter mit dem, was
		// gecacht ist.
		fmt.Fprintf(stderr, "autoupdate: catalog sync warning: %v\n", err)
	}

	installed := map[string]string{}
	for id, cs := range state.Containers {
		installed[id] = cs.VersionInstalled
	}
	updates := catalog.AvailableUpdates(installed)
	if len(updates) == 0 {
		fmt.Fprintln(stdout, "autoupdate: no updates available")
		return 0
	}

	for _, u := range updates {
		cs := state.Containers[u.AppID]
		switch {
		case u.Breaking:
			fmt.Fprintf(stdout, "autoupdate: skip %s (%s → %s): breaking\n", u.AppID, u.From, u.To)
			continue
		case cs != nil && cs.AutoUpdateDisabled:
			fmt.Fprintf(stdout, "autoupdate: skip %s (%s → %s): opt-out\n", u.AppID, u.From, u.To)
			continue
		}
		if *dryRun {
			fmt.Fprintf(stdout, "autoupdate: would update %s (%s → %s)\n", u.AppID, u.From, u.To)
			continue
		}
		if err := runUpdate(cfg, state, u.AppID); err != nil {
			fmt.Fprintf(stderr, "autoupdate: %s: %v\n", u.AppID, err)
			continue
		}
		fmt.Fprintf(stdout, "autoupdate: updated %s (%s → %s)\n", u.AppID, u.From, u.To)
	}
	return 0
}

// runUpdate fuehrt das Update einer einzelnen App durch — analog zum
// Web-Handler handleAppUpdate, aber ohne HTTP-Drumherum.
func runUpdate(cfg *config.Config, state *config.State, appID string) error {
	def, err := catalog.FetchDefinition(cfg.Catalog.URL, appID)
	if err != nil {
		return fmt.Errorf("fetch definition: %w", err)
	}

	env, err := envfile.Load(paths.EnvFile())
	if err != nil {
		env = envfile.New()
	}

	var allDefs []*catalog.Definition
	for id := range state.Containers {
		if d, err := catalog.LoadDefinition(id); err == nil {
			allDefs = append(allDefs, d)
		}
	}

	// Bestehende OIDC-Clients aus allen installierten Apps rekonstruieren, damit
	// das Dex-Config-Regenerieren die anderen Clients nicht wegwirft.
	dexClients := install.ReconstructDexClients(allDefs, env, cfg)
	_, updatedClients, updateErr := install.Update(def, cfg, state, env, dexClients, allDefs, install.NopReporter{})

	envfile.ApplySystemEnv(env, cfg, "")
	if err := env.Save(paths.EnvFile()); err != nil {
		return fmt.Errorf("save env: %w", err)
	}
	if err := state.Save(); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	if updatedClients != nil {
		if err := dex.SaveConfig(cfg, updatedClients); err != nil {
			return fmt.Errorf("save dex config: %w", err)
		}
	}
	return updateErr
}
