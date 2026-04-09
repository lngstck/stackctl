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
	"os"
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

	fmt.Fprintf(stdout, "stackctl %s: web stub\n", version)
	fmt.Fprintf(stdout, "  host=%s port=%d dev=%t\n", *host, *port, *dev)
	fmt.Fprintln(stdout, "  HTTP server not yet implemented (Anhang A Schritt 7).")
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
	fmt.Fprintln(w, "  web            Start the admin web UI (stub)")
	fmt.Fprintln(w, "  help           Show this help")
}
