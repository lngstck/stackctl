// stackctl llm — CLI fuer die llmd-Konfiguration (Provider, Modelle,
// Personas, API-Keys). Schreibt config.yaml + prompts/ in
// /opt/learningstack/llmd/config/ und schickt SIGHUP an ls-llmd.
//
// Alle schreibenden Subbefehle sind flag-basiert (skript-freundlich).
// Einzige Ausnahme: `persona edit` oeffnet $EDITOR fuer den Prompt-Text —
// Multiline-Prompts via CLI-Flag sind nicht praktikabel.

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
	case "model":
		return dispatchModel(rest, stdout, stderr)
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
	fmt.Fprintln(w, "  status                             Show counts and readiness")
	fmt.Fprintln(w, "  reload                             Re-render + SIGHUP llmd")
	fmt.Fprintln(w, "  provider add|list|rm|show          Upstream provider config")
	fmt.Fprintln(w, "  model    add|list|rm|show          Model definitions")
	fmt.Fprintln(w, "  persona  add|edit|list|rm|show     Schul-eigene Personas")
	fmt.Fprintln(w, "  key      create|list|revoke        API-Keys fuer Clients")
}

// === status / reload =======================================================

func cmdLLMStatus(_ []string, stdout, stderr io.Writer) int {
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "llm: load: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "providers:  %d\n", len(f.Providers))
	fmt.Fprintf(stdout, "models:     %d\n", len(f.Models))
	fmt.Fprintf(stdout, "personas:   %d", len(f.Personas))
	inactive := 0
	for _, p := range f.Personas {
		if p.Model == "" {
			inactive++
		}
	}
	if inactive > 0 {
		fmt.Fprintf(stdout, " (%d ohne Modell)", inactive)
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
		fmt.Fprintln(stderr, "usage: stackctl llm provider <add|list|rm|show>")
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
	default:
		fmt.Fprintf(stderr, "unknown: provider %s\n", args[0])
		return 2
	}
}

func cmdProviderAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("provider add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "provider id (lowercase slug)")
	kind := fs.String("kind", "openai", "provider kind")
	baseURL := fs.String("base-url", "", "upstream base URL (without /v1)")
	apiKeyEnv := fs.String("api-key-env", "", "name of env var holding the upstream API key")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *baseURL == "" || *apiKeyEnv == "" {
		fmt.Fprintln(stderr, "provider add: --id, --base-url and --api-key-env are required")
		return 2
	}
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if err := f.AddProvider(llm.Provider{ID: *id, Kind: *kind, BaseURL: *baseURL, APIKeyEnv: *apiKeyEnv}); err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "added provider %s\n", *id)
	return 0
}

func cmdProviderList(_ []string, stdout, stderr io.Writer) int {
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tBASE_URL\tAPI_KEY_ENV")
	for _, p := range f.Providers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, p.Kind, p.BaseURL, p.APIKeyEnv)
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
	fmt.Fprintf(stdout, "id:           %s\n", p.ID)
	fmt.Fprintf(stdout, "kind:         %s\n", p.Kind)
	fmt.Fprintf(stdout, "base_url:     %s\n", p.BaseURL)
	fmt.Fprintf(stdout, "api_key_env:  %s", p.APIKeyEnv)
	if os.Getenv(p.APIKeyEnv) == "" {
		fmt.Fprintln(stdout, "  (NOT SET in environment)")
	} else {
		fmt.Fprintln(stdout, "  (set)")
	}
	return 0
}

// === model =================================================================

func dispatchModel(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: stackctl llm model <add|list|rm|show>")
		return 2
	}
	switch args[0] {
	case "add":
		return cmdModelAdd(args[1:], stdout, stderr)
	case "list":
		return cmdModelList(args[1:], stdout, stderr)
	case "rm", "remove":
		return cmdModelRm(args[1:], stdout, stderr)
	case "show":
		return cmdModelShow(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown: model %s\n", args[0])
		return 2
	}
}

func cmdModelAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("model add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "local model id")
	provider := fs.String("provider", "", "provider id")
	upstreamID := fs.String("upstream-id", "", "upstream model name (as provider expects it)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *provider == "" || *upstreamID == "" {
		fmt.Fprintln(stderr, "model add: --id, --provider and --upstream-id are required")
		return 2
	}
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if err := f.AddModel(llm.Model{ID: *id, Provider: *provider, UpstreamID: *upstreamID}); err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "added model %s\n", *id)
	return 0
}

func cmdModelList(_ []string, stdout, stderr io.Writer) int {
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPROVIDER\tUPSTREAM_ID")
	for _, m := range f.Models {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", m.ID, m.Provider, m.UpstreamID)
	}
	tw.Flush()
	return 0
}

func cmdModelRm(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm model rm <id>")
		return 2
	}
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if err := f.RemoveModel(args[0]); err != nil {
		fmt.Fprintf(stderr, "rm: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "removed model %s\n", args[0])
	return 0
}

func cmdModelShow(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm model show <id>")
		return 2
	}
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	m := f.GetModel(args[0])
	if m == nil {
		fmt.Fprintf(stderr, "model %q not found\n", args[0])
		return 1
	}
	fmt.Fprintf(stdout, "id:           %s\n", m.ID)
	fmt.Fprintf(stdout, "provider:     %s\n", m.Provider)
	fmt.Fprintf(stdout, "upstream_id:  %s\n", m.UpstreamID)
	return 0
}

// === persona ===============================================================

func dispatchPersona(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: stackctl llm persona <add|edit|list|rm|show>")
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
	case "set-model":
		return cmdPersonaSetModel(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown: persona %s\n", args[0])
		return 2
	}
}

func cmdPersonaAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("persona add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "persona id (lowercase slug)")
	model := fs.String("model", "", "model id (leave empty to add as inactive)")
	temperature := fs.Float64("temperature", -1, "default temperature (negative = unset)")
	maxTokens := fs.Int("max-tokens", -1, "default max_tokens (negative = unset)")
	promptFromFile := fs.String("prompt-file", "", "read system prompt from this file (otherwise opens $EDITOR after creation)")
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

	p := llm.Persona{ID: *id, Model: *model}
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

	// Prompt entweder aus Datei, oder $EDITOR
	var promptContent string
	if *promptFromFile != "" {
		raw, err := os.ReadFile(*promptFromFile)
		if err != nil {
			fmt.Fprintf(stderr, "read prompt-file: %v\n", err)
			return 1
		}
		promptContent = string(raw)
	} else {
		c, err := openInEditor("# System-Prompt fuer Persona " + *id + "\n# Speichern und schliessen, um zu uebernehmen. Leer lassen, um spaeter zu editieren.\n")
		if err != nil {
			fmt.Fprintf(stderr, "editor: %v\n", err)
			return 1
		}
		promptContent = stripEditorComments(c)
	}
	if strings.TrimSpace(promptContent) != "" {
		if err := llm.SavePrompt(*id, promptContent); err != nil {
			fmt.Fprintf(stderr, "save prompt: %v\n", err)
			return 1
		}
	}

	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "added persona %s\n", *id)
	if *model == "" {
		fmt.Fprintln(stdout, "  (no model assigned — persona is inactive)")
	}
	return 0
}

func cmdPersonaEdit(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: stackctl llm persona edit <id>")
		return 2
	}
	id := args[0]
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if f.GetPersona(id) == nil {
		fmt.Fprintf(stderr, "persona %q not found\n", id)
		return 1
	}
	existing, err := llm.LoadPrompt(id)
	if err != nil {
		fmt.Fprintf(stderr, "load prompt: %v\n", err)
		return 1
	}
	edited, err := openInEditor(existing)
	if err != nil {
		fmt.Fprintf(stderr, "editor: %v\n", err)
		return 1
	}
	if edited == existing {
		fmt.Fprintln(stdout, "no change")
		return 0
	}
	if err := llm.SavePrompt(id, edited); err != nil {
		fmt.Fprintf(stderr, "save prompt: %v\n", err)
		return 1
	}
	if err := llm.Reload(); err != nil {
		fmt.Fprintf(stderr, "reload: %v\n", err)
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
	fmt.Fprintln(tw, "ID\tMODEL\tSTATUS")
	for _, p := range f.Personas {
		status := "active"
		if p.Model == "" {
			status = "inactive (no model)"
		}
		model := p.Model
		if model == "" {
			model = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", p.ID, model, status)
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
	if err := llm.RemovePrompt(id); err != nil {
		fmt.Fprintf(stderr, "remove prompt: %v\n", err)
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
	fmt.Fprintf(stdout, "model:        %s\n", coalesce(p.Model, "(none)"))
	fmt.Fprintf(stdout, "prompt_file:  %s\n", p.PromptFile)
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
	prompt, _ := llm.LoadPrompt(id)
	if prompt != "" {
		fmt.Fprintln(stdout, "---")
		fmt.Fprint(stdout, prompt)
		if !strings.HasSuffix(prompt, "\n") {
			fmt.Fprintln(stdout)
		}
	}
	return 0
}

func cmdPersonaSetModel(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: stackctl llm persona set-model <persona-id> <model-id|->")
		fmt.Fprintln(stderr, "  use '-' as model-id to deactivate")
		return 2
	}
	id := args[0]
	model := args[1]
	if model == "-" {
		model = ""
	}
	f, err := llm.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	if err := f.SetPersonaModel(id, model); err != nil {
		fmt.Fprintf(stderr, "set-model: %v\n", err)
		return 1
	}
	if err := llm.SaveAndReload(f); err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	if model == "" {
		fmt.Fprintf(stdout, "%s: model cleared (persona inactive)\n", id)
	} else {
		fmt.Fprintf(stdout, "%s: model set to %s\n", id, model)
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
// vi als Fallback), liest den geaenderten Inhalt zurueck. Wird fuer
// Prompt-Editing genutzt.
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

