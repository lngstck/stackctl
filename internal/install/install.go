// Package install orchestrates the full app-install flow described in
// ARCHITECTURE.md §11.2. It coordinates secrets, catalog, compose, dex,
// postgres, and envfile into a single Install() call.
//
// The flow (condensed):
//  1. ensure_secrets + apply_global_env_defaults
//  2. download_binaries (if any)
//  3. create_data_directories + write configs[]
//  4. setup_postgres_db (if depends_on postgres)
//  5. register_oidc_client (if oidc block → dex-config + restart)
//  6. regenerate docker-compose.yml
//  7. docker compose up -d ls-{id}
//  8. run post_install scripts
//  9. update state.yaml + .env
package install

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/compose"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/dex"
	"github.com/lngstck/stackctl/internal/docker"
	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/postgres"
	"github.com/lngstck/stackctl/internal/secrets"
)

// Result bundles what the caller (web UI) needs to show after install.
type Result struct {
	AppID         string
	AppName       string
	Success       bool
	Error         string
	SecretsToShow map[string]string // key → value (shown once, then masked)
	Messages      []string
}

// Install runs the full install flow for a single app. The caller must have
// already resolved prompts and put their values into promptValues.
//
// cfg and state are loaded and saved by the caller; Install mutates both
// (registers the app in state, may update GlobalEnv in cfg) but does NOT
// call Save — the caller does that after updating .env.
func Install(
	def *catalog.Definition,
	cfg *config.Config,
	state *config.State,
	env *envfile.File,
	dexClients []dex.Client,
	allDefs []*catalog.Definition,
	promptValues map[string]string,
) (*Result, []dex.Client, error) {

	res := &Result{
		AppID:   def.ID,
		AppName: def.Name,
	}
	var newEnvKeys []string

	// On failure, roll back any .env keys we added in this run. Without this,
	// a partial install (e.g. postgres setup crashes) leaves orphaned secrets
	// in .env that Remove can't reach, because the app was never registered
	// in state.Containers. The caller re-saves .env after Install returns, so
	// rolling back here is enough — no need to write to disk ourselves.
	rollback := func() {
		if len(newEnvKeys) > 0 {
			env.DeleteKeys(newEnvKeys)
		}
	}

	// --- 0. Pre-flight: referenced system env vars must be populated -------

	if err := checkSystemEnvDeps(def, env); err != nil {
		return fail(res, "%v", err)
	}

	// --- 1. Secrets + global env defaults -----------------------------------

	for _, s := range def.Secrets {
		if _, ok := env.Get(s.Key); ok {
			continue // already exists (re-install preserves secrets)
		}
		val, err := generateSecret(s)
		if err != nil {
			rollback()
			return fail(res, "generate secret %s: %v", s.Key, err)
		}
		env.Set(def.ID, s.Key, val)
		newEnvKeys = append(newEnvKeys, s.Key)
	}

	for _, g := range def.GlobalEnv {
		if _, ok := env.Get(g.Key); !ok && g.Default != "" {
			env.Set(envfile.GlobalSection, g.Key, g.Default)
		}
	}

	// Prompt values go into the app's env section.
	for key, val := range promptValues {
		env.Set(def.ID, key, val)
		newEnvKeys = append(newEnvKeys, key)
	}

	// --- 2. Binaries --------------------------------------------------------

	// (Binary downloads are a Phase 2 concern; stubbed here for completeness.)

	// --- 3. Data directories + configs --------------------------------------

	if err := createDataDirs(def, env); err != nil {
		rollback()
		return fail(res, "data dirs: %v", err)
	}

	// Dex itself has no oidc: block, so installing it wouldn't otherwise
	// trigger a dex-config write. Do it explicitly here so the container
	// finds /etc/dex/config.yaml when it starts.
	if def.ID == "dex" {
		if err := dex.SaveConfig(cfg, dexClients); err != nil {
			rollback()
			return fail(res, "dex initial config: %v", err)
		}
	}

	// llmd hat eine eigene config.yaml unter /opt/learningstack/llmd/config/
	// — beim Erstinstall seedet stackctl Beispiel-Personas und generiert
	// den Schul-Default-API-Key (LLM_API_KEY). Bei Re-Installs idempotent.
	if def.ID == "llmd" {
		added, err := seedLLMConfig(env)
		if err != nil {
			rollback()
			return fail(res, "llmd seed: %v", err)
		}
		newEnvKeys = append(newEnvKeys, added...)
	}

	// --- 4. Postgres DB (if depends_on postgres) ----------------------------

	if dependsOn(def, "postgres") {
		dbKey := postgres.DBPasswordEnvKey(def.ID)
		if _, ok := env.Get(dbKey); !ok {
			pw, err := secrets.RandomPassword(0)
			if err != nil {
				rollback()
				return fail(res, "db password: %v", err)
			}
			env.Set(def.ID, dbKey, pw)
			newEnvKeys = append(newEnvKeys, dbKey)
		}

		dbPW, _ := env.Get(dbKey)
		if err := postgres.SetupAppDB(def.ID, dbPW); err != nil {
			rollback()
			return fail(res, "postgres setup: %v", err)
		}
	}

	// --- 5. OIDC client in Dex ----------------------------------------------

	if def.OIDC != nil {
		oidcSecretKey := strings.ToUpper(strings.ReplaceAll(def.ID, "-", "_")) + "_OIDC_SECRET"
		if _, ok := env.Get(oidcSecretKey); !ok {
			sec, err := secrets.RandomHex(20)
			if err != nil {
				rollback()
				return fail(res, "oidc secret: %v", err)
			}
			env.Set(def.ID, oidcSecretKey, sec)
			newEnvKeys = append(newEnvKeys, oidcSecretKey)
		}

		oidcSecret, _ := env.Get(oidcSecretKey)

		// Build redirect URI. For now, always use the tunneled (public) URL
		// because OIDC apps must be tunneled for the issuer URL to match.
		firstPort := 0
		if len(def.Ports) > 0 {
			firstPort = def.Ports[0].Host
		}
		redirectURI := dex.BuildRedirectURI(
			def.OIDC.RedirectPath,
			def.ID,
			cfg.School.Slug,
			cfg.School.ServerDomain,
			firstPort,
			true, // tunneled — OIDC requires public URL
		)

		client := dex.Client{
			ID:           def.OIDC.ClientID,
			Secret:       oidcSecret,
			Name:         def.Name,
			RedirectURIs: []string{redirectURI},
		}

		var err error
		dexClients, err = dex.AddClient(client, cfg, dexClients)
		if err != nil {
			rollback()
			return fail(res, "dex client: %v", err)
		}
	}

	// --- 6. Regenerate docker-compose.yml -----------------------------------

	composeDefs := collectComposeDefs(allDefs, def)
	if err := compose.Regenerate(composeDefs); err != nil {
		rollback()
		return fail(res, "compose: %v", err)
	}

	// --- 7. docker compose up -----------------------------------------------

	// Persist .env BEFORE `docker compose up` so the newly generated secrets
	// (POSTGRES_PASSWORD, {APP}_DB_PASSWORD, {APP}_OIDC_SECRET, prompt values)
	// are actually available to the container. Caller saves again afterwards
	// to pick up ApplySystemEnv updates — this extra save is the one that
	// counts for container startup.
	envfile.ApplySystemEnv(env, cfg, "")
	if err := env.Save(paths.EnvFile()); err != nil {
		rollback()
		return fail(res, "save env before compose up: %v", err)
	}

	if err := docker.EnsureNetwork(); err != nil {
		rollback()
		return fail(res, "network: %v", err)
	}
	code, out := docker.ComposeUp(paths.ComposeFile(), compose.ServiceName(def.ID))
	if code != 0 {
		rollback()
		return fail(res, "docker up: %s", out)
	}

	// --- 8. Post-install scripts --------------------------------------------

	if def.Scripts != nil {
		for _, step := range def.Scripts.PostInstall {
			if err := runScript(step); err != nil {
				// Non-fatal: log but don't fail the install.
				res.Messages = append(res.Messages,
					fmt.Sprintf("⚠ Post-install-Script fehlgeschlagen: %v", err))
			}
		}
	}

	// --- 9. Update state ----------------------------------------------------

	hostPorts := make([]int, len(def.Ports))
	for i, p := range def.Ports {
		hostPorts[i] = p.Host
	}

	cs := &config.ContainerState{
		ID:               def.ID,
		Name:             def.Name,
		VersionInstalled: def.Version,
		Ports:            hostPorts,
		EnvKeys:          newEnvKeys,
		InstalledAt:      time.Now().UTC().Format(time.RFC3339),
		TunnelEnabled:    false,
	}
	state.Containers[def.ID] = cs
	for _, p := range hostPorts {
		state.Ports[p] = def.ID
	}

	// Collect secrets to show.
	res.SecretsToShow = map[string]string{}
	if def.PostInstall != nil {
		for _, s := range def.PostInstall.SecretsToShow {
			if val, ok := env.Get(s.Key); ok {
				res.SecretsToShow[s.Key] = val
			}
		}
		for _, m := range def.PostInstall.Messages {
			res.Messages = append(res.Messages, expandEnvVars(m, env))
		}
	}

	res.Success = true
	return res, dexClients, nil
}

// Update refreshes an already-installed app against the current catalog
// definition: regenerates secrets that the new definition added, rewrites
// configs, re-registers the OIDC client, regenerates docker-compose.yml,
// pulls a fresh image, and recreates the container. Existing prompt values,
// tunnel state, and InstalledAt timestamp are preserved.
//
// The caller must have already (force-)refetched the catalog definition into
// the local cache so `def` reflects the latest YAML.
func Update(
	def *catalog.Definition,
	cfg *config.Config,
	state *config.State,
	env *envfile.File,
	dexClients []dex.Client,
	allDefs []*catalog.Definition,
) (*Result, []dex.Client, error) {

	res := &Result{AppID: def.ID, AppName: def.Name}

	cs, ok := state.Containers[def.ID]
	if !ok {
		return fail(res, "app %q is not installed", def.ID)
	}

	if err := checkSystemEnvDeps(def, env); err != nil {
		return fail(res, "%v", err)
	}

	// New secrets only — preserve everything already in .env so re-runs are
	// safe and an admin's manually-tuned values survive.
	for _, s := range def.Secrets {
		if _, ok := env.Get(s.Key); ok {
			continue
		}
		val, err := generateSecret(s)
		if err != nil {
			return fail(res, "generate secret %s: %v", s.Key, err)
		}
		env.Set(def.ID, s.Key, val)
		cs.EnvKeys = appendUnique(cs.EnvKeys, s.Key)
	}
	for _, g := range def.GlobalEnv {
		if _, ok := env.Get(g.Key); !ok && g.Default != "" {
			env.Set(envfile.GlobalSection, g.Key, g.Default)
		}
	}

	if err := createDataDirs(def, env); err != nil {
		return fail(res, "data dirs: %v", err)
	}

	if def.ID == "dex" {
		if err := dex.SaveConfig(cfg, dexClients); err != nil {
			return fail(res, "dex config: %v", err)
		}
	}

	if def.OIDC != nil {
		oidcSecretKey := strings.ToUpper(strings.ReplaceAll(def.ID, "-", "_")) + "_OIDC_SECRET"
		if _, ok := env.Get(oidcSecretKey); !ok {
			sec, err := secrets.RandomHex(20)
			if err != nil {
				return fail(res, "oidc secret: %v", err)
			}
			env.Set(def.ID, oidcSecretKey, sec)
			cs.EnvKeys = appendUnique(cs.EnvKeys, oidcSecretKey)
		}
		oidcSecret, _ := env.Get(oidcSecretKey)
		firstPort := 0
		if len(def.Ports) > 0 {
			firstPort = def.Ports[0].Host
		}
		redirectURI := dex.BuildRedirectURI(
			def.OIDC.RedirectPath, def.ID, cfg.School.Slug,
			cfg.School.ServerDomain, firstPort, true,
		)
		client := dex.Client{
			ID: def.OIDC.ClientID, Secret: oidcSecret, Name: def.Name,
			RedirectURIs: []string{redirectURI},
		}
		var err error
		dexClients, err = dex.AddClient(client, cfg, dexClients)
		if err != nil {
			return fail(res, "dex client: %v", err)
		}
	}

	composeDefs := collectComposeDefs(allDefs, def)
	if err := compose.Regenerate(composeDefs); err != nil {
		return fail(res, "compose: %v", err)
	}

	envfile.ApplySystemEnv(env, cfg, "")
	if err := env.Save(paths.EnvFile()); err != nil {
		return fail(res, "save env: %v", err)
	}

	// Pull AHEAD of compose up so a tag like :latest or :main actually picks
	// up a newer image — docker compose itself never re-pulls a tag it has
	// cached. We continue on pull failure (offline / registry hiccup) so the
	// recreate still happens against whatever image is on disk.
	if _, pullOut := docker.ComposePull(paths.ComposeFile(), compose.ServiceName(def.ID)); pullOut != "" {
		// Combined output includes both progress lines and errors; only surface
		// it when something looked off. We can't reliably parse the exit code
		// here because compose returns 0 even on partial pull failures.
	}

	if code, out := docker.ComposeUp(paths.ComposeFile(), compose.ServiceName(def.ID)); code != 0 {
		return fail(res, "compose up: %s", out)
	}

	cs.VersionInstalled = def.Version

	res.SecretsToShow = map[string]string{}
	if def.PostInstall != nil {
		for _, s := range def.PostInstall.SecretsToShow {
			if val, ok := env.Get(s.Key); ok {
				res.SecretsToShow[s.Key] = val
			}
		}
		for _, m := range def.PostInstall.Messages {
			res.Messages = append(res.Messages, expandEnvVars(m, env))
		}
	}
	res.Success = true
	return res, dexClients, nil
}

// checkSystemEnvDeps verifies that the system-owned env vars referenced by
// the app are present and non-empty in .env. Specifically guards against the
// silent-failure path where ${ADMIN_PASSWORD} expands to "" and an app's
// WEBUI_ADMIN_PASSWORD seed is rejected without any error in compose. Only
// envfile.SystemEnvKeys are checked — app secrets / DB passwords get
// generated later in the install flow, so flagging them here would be wrong.
func checkSystemEnvDeps(def *catalog.Definition, env *envfile.File) error {
	systemKey := map[string]bool{}
	for _, k := range envfile.SystemEnvKeys {
		systemKey[k] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, e := range def.Environment {
		for _, key := range extractEnvRefs(e.Value) {
			if !systemKey[key] || seen[key] {
				continue
			}
			seen[key] = true
			val, ok := env.Get(key)
			if !ok || val == "" {
				missing = append(missing, key)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	hint := ""
	for _, k := range missing {
		if k == "ADMIN_PASSWORD" {
			hint = " (Admin-Passwort in den Einstellungen neu setzen, damit es in die .env geschrieben wird)"
			break
		}
	}
	return fmt.Errorf("system-env-Variablen leer in .env: %s%s", strings.Join(missing, ", "), hint)
}

// extractEnvRefs returns the names of all ${VAR} references in s. Plain $VAR
// is intentionally ignored — stackctl always writes ${VAR} in catalog YAMLs.
func extractEnvRefs(s string) []string {
	var refs []string
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			return refs
		}
		s = s[i+2:]
		j := strings.Index(s, "}")
		if j < 0 {
			return refs
		}
		refs = append(refs, s[:j])
		s = s[j+1:]
	}
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// Remove stops and removes an app from the stack. The image is deleted as
// well, unless another installed app references it. If wipeData is true,
// the host data directory under /opt/learningstack/{id}/ is wiped AND, for
// apps with depends_on: postgres, the per-app database+role is dropped.
// Otherwise data is preserved for a future reinstall.
func Remove(
	appID string,
	cfg *config.Config,
	state *config.State,
	env *envfile.File,
	dexClients []dex.Client,
	remainingDefs []*catalog.Definition,
	wipeData bool,
) ([]dex.Client, error) {

	cs, ok := state.Containers[appID]
	if !ok {
		return dexClients, fmt.Errorf("app %q not installed", appID)
	}

	// Capture image + depends_on BEFORE we drop the cached definition /
	// regenerate compose — we need them for cleanup below.
	var (
		removedImage string
		usesPostgres bool
	)
	if def, err := catalog.LoadDefinition(appID); err == nil {
		removedImage = def.Image.FullImage()
		usesPostgres = dependsOn(def, "postgres")
	}

	// Stop AND remove the container. A plain stop would leave the container
	// lingering in `docker ps -a` under the same name and block reinstall.
	docker.ComposeRm(paths.ComposeFile(), compose.ServiceName(appID))

	// Remove OIDC client if it was registered.
	for _, c := range dexClients {
		if c.ID == appID {
			var err error
			dexClients, err = dex.RemoveClient(appID, cfg, dexClients)
			if err != nil {
				return dexClients, fmt.Errorf("dex remove client: %w", err)
			}
			break
		}
	}

	// Remove env keys.
	env.DeleteKeys(cs.EnvKeys)

	// Free ports.
	for _, p := range cs.Ports {
		delete(state.Ports, p)
	}
	delete(state.Containers, appID)

	// Regenerate compose without this app.
	composeDefs := make([]*compose.AppDefinition, 0, len(remainingDefs))
	for _, d := range remainingDefs {
		if d.ID != appID {
			composeDefs = append(composeDefs, d.ToCompose())
		}
	}
	if err := compose.Regenerate(composeDefs); err != nil {
		return dexClients, fmt.Errorf("compose regen: %w", err)
	}

	// Image-Cleanup: nur loeschen, wenn keine andere installierte App
	// dasselbe Image referenziert (z.B. zwei Apps auf demselben Base-Image).
	if removedImage != "" {
		stillUsed := false
		for _, d := range remainingDefs {
			if d.Image.FullImage() == removedImage {
				stillUsed = true
				break
			}
		}
		if !stillUsed {
			if err := docker.RemoveImage(removedImage); err != nil {
				// Nicht fatal: Container ist weg, Image-Rest darf hier kein
				// Roll-back ausloesen. Caller loggt das uebliche Save-Ergebnis.
				_ = err
			}
		}
	}

	// Daten-Cleanup nur wenn explizit angefordert. Nicht-fatal: Drop kann an
	// einem Lock haengen (z.B. wenn ein anderer Container die DB noch offen
	// hat), Host-Pfad kann auf einem ReadOnly-FS liegen. In beiden Faellen
	// ist der Container schon weg — wir loggen, blocken aber nicht.
	if wipeData {
		if usesPostgres {
			if err := postgres.DropAppDB(appID); err != nil {
				_ = err
			}
		}
		// Konvention: alle App-Daten liegen unter /opt/learningstack/{id}/.
		// RemoveHostPath weigert sich, ausserhalb davon zu loeschen.
		if err := docker.RemoveHostPath("/opt/learningstack/" + appID); err != nil {
			_ = err
		}
	}

	return dexClients, nil
}

// -- helpers ----------------------------------------------------------------

func fail(res *Result, format string, args ...any) (*Result, []dex.Client, error) {
	msg := fmt.Sprintf(format, args...)
	res.Error = msg
	return res, nil, fmt.Errorf("install %s: %s", res.AppID, msg)
}

func dependsOn(def *catalog.Definition, depID string) bool {
	for _, d := range def.DependsOn {
		if d == depID {
			return true
		}
	}
	return false
}

func generateSecret(spec catalog.SecretSpec) (string, error) {
	switch spec.Generate {
	case "password":
		return secrets.RandomPassword(0)
	case "api_key":
		prefix := spec.Prefix
		if prefix == "" {
			prefix = "sk"
		}
		return secrets.RandomAPIKey(prefix)
	default: // "secret" or empty
		return secrets.RandomHex(20)
	}
}

func createDataDirs(def *catalog.Definition, env *envfile.File) error {
	for _, v := range def.Volumes {
		if err := paths.EnsureDir(v.Host, 0o750); err != nil {
			return err
		}
		// Falls der Container als Non-Root-User laeuft, das Host-Dir
		// entsprechend chownen. stackctl selbst laeuft ohne root, aber
		// docker tut's.
		if v.Owner != "" {
			if err := docker.ChownHostPath(v.Host, v.Owner); err != nil {
				return fmt.Errorf("chown %s: %w", v.Host, err)
			}
		}
	}
	for _, c := range def.Configs {
		dir := c.Path[:strings.LastIndex(c.Path, "/")]
		if err := paths.EnsureDir(dir, 0o750); err != nil {
			return err
		}
		if c.Content != "" {
			mode := os.FileMode(0o644)
			if c.Mode != "" {
				if m, err := strconv.ParseUint(c.Mode, 8, 32); err == nil {
					mode = os.FileMode(m)
				}
			}
			// Template substitution: ${VAR} resolved from .env.
			content := expandEnvVars(c.Content, env)
			if err := paths.AtomicWrite(c.Path, []byte(content), mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// expandEnvVars replaces ${VAR} placeholders in s with values from env.
// Unknown variables are left as-is.
func expandEnvVars(s string, env *envfile.File) string {
	return os.Expand(s, func(key string) string {
		if val, ok := env.Get(key); ok {
			return val
		}
		return "${" + key + "}"
	})
}

func collectComposeDefs(allDefs []*catalog.Definition, newDef *catalog.Definition) []*compose.AppDefinition {
	seen := map[string]bool{}
	var out []*compose.AppDefinition
	for _, d := range allDefs {
		seen[d.ID] = true
		out = append(out, d.ToCompose())
	}
	if !seen[newDef.ID] {
		out = append(out, newDef.ToCompose())
	}
	return out
}

func runScript(step catalog.ScriptStep) error {
	switch step.Wait {
	case "", "started":
		// No delay.
	case "healthy":
		// TODO: poll docker health status before running.
	default:
		if secs, err := strconv.Atoi(step.Wait); err == nil && secs > 0 {
			time.Sleep(time.Duration(secs) * time.Second)
		}
	}
	switch step.Type {
	case "docker-exec":
		container := step.Container
		if container == "" {
			return fmt.Errorf("docker-exec script missing container")
		}
		code, out, err := docker.Exec(container, []string{"sh", "-c", step.Command})
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("exit %d: %s", code, out)
		}
	case "host":
		// Host scripts are not supported in Phase 1 for security reasons.
		return fmt.Errorf("host scripts not supported")
	default:
		return fmt.Errorf("unknown script type %q", step.Type)
	}
	return nil
}
