// stackctl llm — CLI fuer die llmd-Konfiguration (Provider, Personas, API-Keys).
// Schreibt config.yaml in /opt/learningstack/llmd/config/ und schickt SIGHUP
// an ls-llmd.
//
// Schema v2: kein models[] mehr, Personas zeigen direkt auf (provider,
// upstream_id), Provider tragen api_key inline, kein api_key_env. Prompts
// sind inline am Persona-Objekt.
//
// Design-Hinweis: die meisten Admins arbeiten via Web-UI. Diese CLI deckt
// Day-0-Setup und SSH-Recovery ab (list/show/dump/revoke). Add/Edit-Pfade
// existieren fuer Skripting, sollen aber kein dauerhafter Daily-Driver
// werden — UI ist der Default-Workflow.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/lngstck/stackctl/internal/llm"
)

func cmdLLM(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return cmdLLMStatus(nil, stdout, stderr)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "status":
		return cmdLLMStatus(rest, stdout, stderr)
	case "reload":
		return cmdLLMReload(rest, stdout, stderr)

	case "provider":
		return dispatchProvider(rest, stdout, stderr)
	case "persona":
		return dispatchPersona(rest, stdout, stderr)
	case "key":
		return dispatchKey(rest, stdout, stderr)

	case "help", "-h", "--help":
		llmUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "stackctl llm: unknown subcommand %q\n\n", sub)
		llmUsage(stderr)
		return 2
	}
}

func llmUsage(w io.Writer) {
	fmt.Fprintln(w, "stackctl llm – verwalte den lokalen LLM-Gateway")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  stackctl llm <subcommand> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  status                                   Counts + readiness summary")
	fmt.Fprintln(w, "  reload                                   SIGHUP llmd")
	fmt.Fprintln(w, "  provider add|list|rm|show|set-key        Upstream-Provider inkl. API-Keys")
	fmt.Fprintln(w, "  persona  add|edit|list|rm|show|set       Schul-eigene Personas")
	fmt.Fprintln(w, "  key      create|list|revoke              API-Keys fuer Clients (Open WebUI etc.)")
}

// === status / reload =======================================================

func cmdLLMStatus(_ []string, stdout, stderr io.Writer) int {
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "llm: load: %v\n", err)
		return 1
	}
	missingKey := 0
	for _, p := range f.Providers {
		if p.APIKey == "" {
			missingKey++
		}
	}
	fmt.Fprintf(stdout, "providers:  %d", len(f.Providers))
	if missingKey > 0 {
		fmt.Fprintf(stdout, " (%d ohne api_key)", missingKey)
	}
	fmt.Fprintln(stdout)
	inactive := 0
	for _, p := range f.Personas {
		if p.Provider == "" || p.UpstreamID == "" {
			inactive++
		}
	}
	fmt.Fprintf(stdout, "personas:   %d", len(f.Personas))
	if inactive > 0 {
		fmt.Fprintf(stdout, " (%d inaktiv)", inactive)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "api_keys:   %d\n", len(f.APIKeys))
	return 0
}

func cmdLLMReload(_ []string, stdout, stderr io.Writer) int {
	if err := llm.Reload(); err != nil {
		fmt.Fprintf(stderr, "llm reload: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}

// === provider ==============================================================

func dispatchProvider(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: stackctl llm provider <add|list|rm|show|set-key>")
		return 2
	}
	switch args[0] {
	case "add":
		return cmdProviderAdd(args[1:], stdout, stderr)
	case "list":
		return cmdProviderList(args[1:], stdout, stderr)
	case "rm", "remove":
		return cmdProviderRm(args[1:], stdout, stderr)
	case "show":
		return cmdProviderShow(args[1:], stdout, stderr)
	case "set-key":
		return cmdProviderSetKey(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown: provider %s\n", args[0])
		return 2
	}
}

// readAPIKey loest die drei moeglichen Quellen auf: --api-key flag, stdin
// (wenn --api-key-stdin gesetzt), oder leer (Provider wird ohne Key
// angelegt). Liefert (key, error). Shell-History-safe wenn --api-key-stdin.
func readAPIKey(flagValue string, fromStdin bool, stderr io.Writer) (string, error) {
	if fromStdin && flagValue != "" {
		return "", fmt.Errorf("--api-key and --api-key-stdin are mutually exclusive")
	}
	if fromStdin {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	return flagValue, nil
}

// maskKey gibt einen sicheren Display-String fuer API-Keys zurueck.
// "" -> "(not set)", sonst erstes 4 Zeichen + "..." + letzte 4.
func maskKey(k string) string {
	if k == "" {
		return "(not set)"
	}
	if len(k) <= 8 {
		return strings.Repeat("*", len(k))
	}
	return k[:4] + "..." + k[len(k)-4:]
}

func cmdProviderAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("provider add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "provider id (lowercase slug)")
	kind := fs.String("kind", "openai", "provider kind")
	baseURL := fs.String("base-url", "", "upstream base URL (without /v1)")
	apiKey := fs.String("api-key", "", "upstream API key (use --api-key-stdin to avoid shell history)")
	apiKeyStdin := fs.Bool("api-key-stdin", false, "read API key from stdin instead of --api-key")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *baseURL == "" {
		fmt.Fprintln(stderr, "provider add: --id and --base-url are required")
		return 2
	}
	key, err := readAPIKey(*apiKey, *apiKeyStdin, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "api-key: %v\n", err)
		return 2
	}

	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if err := f.AddProvider(llm.Provider{ID: *id, Kind: *kind, BaseURL: *baseURL, APIKey: key}); err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "added provider %s\n", *id)
	if key == "" {
		fmt.Fprintln(stdout, "  (no api_key set — set later with 'stackctl llm provider set-key')")
	}
	return 0
}

func cmdProviderSetKey(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("provider set-key", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiKey := fs.String("api-key", "", "new API key (use --api-key-stdin to avoid shell history)")
	apiKeyStdin := fs.Bool("api-key-stdin", false, "read API key from stdin")
	clear := fs.Bool("clear", false, "remove the key (sets it to empty)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm provider set-key <id> [--api-key X | --api-key-stdin | --clear]")
		return 2
	}
	id := fs.Arg(0)

	var key string
	if !*clear {
		var err error
		key, err = readAPIKey(*apiKey, *apiKeyStdin, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "api-key: %v\n", err)
			return 2
		}
		if key == "" {
			fmt.Fprintln(stderr, "provider set-key: provide --api-key, --api-key-stdin, or --clear")
			return 2
		}
	}

	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if err := f.SetProviderKey(id, key); err != nil {
		fmt.Fprintf(stderr, "set-key: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	if key == "" {
		fmt.Fprintf(stdout, "%s: api_key cleared\n", id)
	} else {
		fmt.Fprintf(stdout, "%s: api_key updated (%s)\n", id, maskKey(key))
	}
	return 0
}

func cmdProviderList(_ []string, stdout, stderr io.Writer) int {
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tBASE_URL\tAPI_KEY")
	for _, p := range f.Providers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, p.Kind, p.BaseURL, maskKey(p.APIKey))
	}
	tw.Flush()
	return 0
}

func cmdProviderRm(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm provider rm <id>")
		return 2
	}
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if err := f.RemoveProvider(args[0]); err != nil {
		fmt.Fprintf(stderr, "rm: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "removed provider %s\n", args[0])
	return 0
}

func cmdProviderShow(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm provider show <id>")
		return 2
	}
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	p := f.GetProvider(args[0])
	if p == nil {
		fmt.Fprintf(stderr, "provider %q not found\n", args[0])
		return 1
	}
	fmt.Fprintf(stdout, "id:        %s\n", p.ID)
	fmt.Fprintf(stdout, "kind:      %s\n", p.Kind)
	fmt.Fprintf(stdout, "base_url:  %s\n", p.BaseURL)
	fmt.Fprintf(stdout, "api_key:   %s\n", maskKey(p.APIKey))
	return 0
}

// === persona ===============================================================

func dispatchPersona(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: stackctl llm persona <add|edit|list|rm|show|set>")
		return 2
	}
	switch args[0] {
	case "add":
		return cmdPersonaAdd(args[1:], stdout, stderr)
	case "edit":
		return cmdPersonaEdit(args[1:], stdout, stderr)
	case "list":
		return cmdPersonaList(args[1:], stdout, stderr)
	case "rm", "remove":
		return cmdPersonaRm(args[1:], stdout, stderr)
	case "show":
		return cmdPersonaShow(args[1:], stdout, stderr)
	case "set":
		return cmdPersonaSet(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown: persona %s\n", args[0])
		return 2
	}
}

// readPrompt loest die drei Prompt-Quellen auf: --prompt-file <path>,
// --prompt-stdin, oder $EDITOR (mit optionalem Initial-Text). Leer = leerer
// Prompt (Passthrough-Persona).
func readPrompt(promptFile string, fromStdin bool, editorInitial string) (string, error) {
	if promptFile != "" && fromStdin {
		return "", fmt.Errorf("--prompt-file and --prompt-stdin are mutually exclusive")
	}
	if promptFile != "" {
		raw, err := os.ReadFile(promptFile)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", promptFile, err)
		}
		return string(raw), nil
	}
	if fromStdin {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(raw), nil
	}
	// kein Quellen-Flag => $EDITOR
	c, err := openInEditor(editorInitial)
	if err != nil {
		return "", err
	}
	return stripEditorComments(c), nil
}

func cmdPersonaAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("persona add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "persona id (lowercase slug)")
	provider := fs.String("provider", "", "provider id (leave empty to add as inactive)")
	upstreamID := fs.String("upstream-id", "", "upstream model id (required if --provider is set)")
	temperature := fs.Float64("temperature", -1, "default temperature (negative = unset)")
	maxTokens := fs.Int("max-tokens", -1, "default max_tokens (negative = unset)")
	promptFile := fs.String("prompt-file", "", "read system prompt from file (empty = $EDITOR or --prompt-stdin)")
	promptStdin := fs.Bool("prompt-stdin", false, "read system prompt from stdin")
	noPrompt := fs.Bool("no-prompt", false, "create persona without any system prompt (passthrough)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" {
		fmt.Fprintln(stderr, "persona add: --id is required")
		return 2
	}

	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}

	var prompt string
	if !*noPrompt {
		prompt, err = readPrompt(*promptFile, *promptStdin,
			"# System-Prompt fuer Persona "+*id+"\n# Kommentarzeilen mit # werden entfernt. Leer lassen = Passthrough.\n")
		if err != nil {
			fmt.Fprintf(stderr, "prompt: %v\n", err)
			return 1
		}
	}

	p := llm.Persona{
		ID:         *id,
		Provider:   *provider,
		UpstreamID: *upstreamID,
		Prompt:     strings.TrimSpace(prompt),
	}
	params := map[string]any{}
	if *temperature >= 0 {
		params["temperature"] = *temperature
	}
	if *maxTokens > 0 {
		params["max_tokens"] = *maxTokens
	}
	if len(params) > 0 {
		p.Params = params
	}
	if err := f.AddPersona(p); err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}

	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "added persona %s\n", *id)
	if *provider == "" {
		fmt.Fprintln(stdout, "  (no provider assigned — persona is inactive)")
	}
	return 0
}

func cmdPersonaEdit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("persona edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	promptFile := fs.String("prompt-file", "", "replace prompt from file (otherwise opens $EDITOR)")
	promptStdin := fs.Bool("prompt-stdin", false, "replace prompt from stdin")
	noPrompt := fs.Bool("no-prompt", false, "clear the system prompt")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm persona edit <id> [--prompt-file F | --prompt-stdin | --no-prompt]")
		return 2
	}
	id := fs.Arg(0)
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	persona := f.GetPersona(id)
	if persona == nil {
		fmt.Fprintf(stderr, "persona %q not found\n", id)
		return 1
	}

	var prompt string
	if !*noPrompt {
		prompt, err = readPrompt(*promptFile, *promptStdin, persona.Prompt)
		if err != nil {
			fmt.Fprintf(stderr, "prompt: %v\n", err)
			return 1
		}
		prompt = strings.TrimSpace(prompt)
		if prompt == strings.TrimSpace(persona.Prompt) {
			fmt.Fprintln(stdout, "no change")
			return 0
		}
	}
	if err := f.SetPersonaPrompt(id, prompt); err != nil {
		fmt.Fprintf(stderr, "set prompt: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "updated prompt for %s\n", id)
	return 0
}

func cmdPersonaList(_ []string, stdout, stderr io.Writer) int {
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPROVIDER\tUPSTREAM\tPROMPT\tSTATUS")
	for _, p := range f.Personas {
		status := "active"
		switch {
		case p.Provider == "":
			status = "inactive (no provider)"
		case p.UpstreamID == "":
			status = "inactive (no upstream_id)"
		}
		hasPrompt := "-"
		if strings.TrimSpace(p.Prompt) != "" {
			hasPrompt = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.ID, coalesce(p.Provider, "-"), coalesce(p.UpstreamID, "-"), hasPrompt, status)
	}
	tw.Flush()
	return 0
}

func cmdPersonaRm(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm persona rm <id>")
		return 2
	}
	id := args[0]
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if err := f.RemovePersona(id); err != nil {
		fmt.Fprintf(stderr, "rm: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "removed persona %s\n", id)
	return 0
}

func cmdPersonaShow(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm persona show <id>")
		return 2
	}
	id := args[0]
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	p := f.GetPersona(id)
	if p == nil {
		fmt.Fprintf(stderr, "persona %q not found\n", id)
		return 1
	}
	fmt.Fprintf(stdout, "id:           %s\n", p.ID)
	fmt.Fprintf(stdout, "provider:     %s\n", coalesce(p.Provider, "(none)"))
	fmt.Fprintf(stdout, "upstream_id:  %s\n", coalesce(p.UpstreamID, "(none)"))
	if len(p.Params) > 0 {
		fmt.Fprintf(stdout, "params:       ")
		first := true
		for k, v := range p.Params {
			if !first {
				fmt.Fprintf(stdout, ", ")
			}
			fmt.Fprintf(stdout, "%s=%v", k, v)
			first = false
		}
		fmt.Fprintln(stdout)
	}
	if strings.TrimSpace(p.Prompt) != "" {
		fmt.Fprintln(stdout, "---")
		fmt.Fprint(stdout, p.Prompt)
		if !strings.HasSuffix(p.Prompt, "\n") {
			fmt.Fprintln(stdout)
		}
	}
	return 0
}

// cmdPersonaSet weist (provider, upstream-id) zu, oder leert beides.
func cmdPersonaSet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("persona set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	provider := fs.String("provider", "", "provider id (or empty + --clear to deactivate)")
	upstreamID := fs.String("upstream-id", "", "upstream model id (required when --provider is set)")
	clear := fs.Bool("clear", false, "deactivate persona (clears provider + upstream_id)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm persona set <id> --provider X --upstream-id Y | --clear")
		return 2
	}
	id := fs.Arg(0)

	if *clear {
		*provider = ""
		*upstreamID = ""
	} else if *provider == "" || *upstreamID == "" {
		fmt.Fprintln(stderr, "persona set: provide both --provider and --upstream-id (or use --clear)")
		return 2
	}

	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if err := f.SetPersonaUpstream(id, *provider, *upstreamID); err != nil {
		fmt.Fprintf(stderr, "set: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	if *provider == "" {
		fmt.Fprintf(stdout, "%s: cleared (persona inactive)\n", id)
	} else {
		fmt.Fprintf(stdout, "%s: provider=%s, upstream_id=%s\n", id, *provider, *upstreamID)
	}
	return 0
}

// === key ===================================================================

func dispatchKey(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: stackctl llm key <create|list|revoke>")
		return 2
	}
	switch args[0] {
	case "create":
		return cmdKeyCreate(args[1:], stdout, stderr)
	case "list":
		return cmdKeyList(args[1:], stdout, stderr)
	case "revoke", "rm":
		return cmdKeyRevoke(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown: key %s\n", args[0])
		return 2
	}
}

func cmdKeyCreate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("key create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "key id (e.g. 'open-webui')")
	personas := fs.String("personas", "", "comma-separated persona ids (empty = all)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" {
		fmt.Fprintln(stderr, "key create: --id is required")
		return 2
	}

	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	plaintext, prefix, hash, err := llm.GenerateAPIKey()
	if err != nil {
		fmt.Fprintf(stderr, "generate: %v\n", err)
		return 1
	}
	allowed := []string{}
	if *personas != "" {
		for _, p := range strings.Split(*personas, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if f.GetPersona(p) == nil {
				fmt.Fprintf(stderr, "warning: persona %q does not exist (yet?)\n", p)
			}
			allowed = append(allowed, p)
		}
	}
	if err := f.AddAPIKey(llm.APIKey{ID: *id, Prefix: prefix, Hash: hash, AllowedPersonas: allowed}); err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created key %s\n", *id)
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "  "+plaintext)
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "This is the only time the plaintext key is shown. Store it now.")
	return 0
}

func cmdKeyList(_ []string, stdout, stderr io.Writer) int {
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPREFIX\tALLOWED_PERSONAS")
	for _, k := range f.APIKeys {
		personas := "(all)"
		if len(k.AllowedPersonas) > 0 {
			personas = strings.Join(k.AllowedPersonas, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", k.ID, k.Prefix, personas)
	}
	tw.Flush()
	return 0
}

func cmdKeyRevoke(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm key revoke <id>")
		return 2
	}
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if err := f.RemoveAPIKey(args[0]); err != nil {
		fmt.Fprintf(stderr, "revoke: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "revoked key %s\n", args[0])
	return 0
}

// === helpers ===============================================================

func coalesce(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

// openInEditor schreibt initial in eine temp-Datei, oeffnet $EDITOR (oder
// vi als Fallback), liest den geaenderten Inhalt zurueck.
func openInEditor(initial string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	tmp, err := os.CreateTemp("", "stackctl-llm-prompt-*.md")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(initial); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	cmd := exec.Command(editor, tmpName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor %s %s: %w", editor, filepath.Base(tmpName), err)
	}
	raw, err := os.ReadFile(tmpName)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// stripEditorComments entfernt Zeilen, die mit "#" beginnen — Komfort
// fuer den $EDITOR-Flow bei persona add, damit der Header-Kommentar nicht
// ins Prompt landet. Achtung: betrifft nur fuehrendes "#" zeilenweise.
func stripEditorComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
