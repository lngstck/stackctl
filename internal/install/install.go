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

	// --- 1. Secrets + global env defaults -----------------------------------

	for _, s := range def.Secrets {
		if _, ok := env.Get(s.Key); ok {
			continue // already exists (re-install preserves secrets)
		}
		val, err := generateSecret(s)
		if err != nil {
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

	// admin_password_env: inject STACKCTL_ADMIN_PASSWORD under the app's key.
	if def.AdminPasswordEnv != "" {
		adminPW, ok := env.Get("STACKCTL_ADMIN_PASSWORD")
		if ok && adminPW != "" {
			env.Set(def.ID, def.AdminPasswordEnv, adminPW)
			newEnvKeys = append(newEnvKeys, def.AdminPasswordEnv)
		}
	}

	// --- 2. Binaries --------------------------------------------------------

	// (Binary downloads are a Phase 2 concern; stubbed here for completeness.)

	// --- 3. Data directories + configs --------------------------------------

	if err := createDataDirs(def); err != nil {
		return fail(res, "data dirs: %v", err)
	}

	// --- 4. Postgres DB (if depends_on postgres) ----------------------------

	if dependsOn(def, "postgres") {
		dbKey := postgres.DBPasswordEnvKey(def.ID)
		if _, ok := env.Get(dbKey); !ok {
			pw, err := secrets.RandomPassword(0)
			if err != nil {
				return fail(res, "db password: %v", err)
			}
			env.Set(def.ID, dbKey, pw)
			newEnvKeys = append(newEnvKeys, dbKey)
		}

		dbPW, _ := env.Get(dbKey)
		if err := postgres.SetupAppDB(def.ID, dbPW); err != nil {
			return fail(res, "postgres setup: %v", err)
		}
	}

	// --- 5. OIDC client in Dex ----------------------------------------------

	if def.OIDC != nil {
		oidcSecretKey := strings.ToUpper(strings.ReplaceAll(def.ID, "-", "_")) + "_OIDC_SECRET"
		if _, ok := env.Get(oidcSecretKey); !ok {
			sec, err := secrets.RandomHex(20)
			if err != nil {
				return fail(res, "oidc secret: %v", err)
			}
			env.Set(def.ID, oidcSecretKey, sec)
			newEnvKeys = append(newEnvKeys, oidcSecretKey)
		}

		oidcSecret, _ := env.Get(oidcSecretKey)
		redirectURI := dex.BuildRedirectURI(
			def.OIDC.RedirectURITemplate,
			def.ID,
			cfg.School.Slug,
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
			return fail(res, "dex client: %v", err)
		}
	}

	// --- 6. Regenerate docker-compose.yml -----------------------------------

	composeDefs := collectComposeDefs(allDefs, def)
	if err := compose.Regenerate(composeDefs); err != nil {
		return fail(res, "compose: %v", err)
	}

	// --- 7. docker compose up -----------------------------------------------

	if err := docker.EnsureNetwork(); err != nil {
		return fail(res, "network: %v", err)
	}
	code, out := docker.ComposeUp(paths.ComposeFile(), compose.ServiceName(def.ID))
	if code != 0 {
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
		for _, key := range def.PostInstall.SecretsToShow {
			if val, ok := env.Get(key); ok {
				res.SecretsToShow[key] = val
			}
		}
		res.Messages = append(res.Messages, def.PostInstall.Messages...)
	}

	res.Success = true
	return res, dexClients, nil
}

// Remove stops and removes an app from the stack. Data directories under
// /opt/learningstack/{id}/ are kept (the admin can delete them manually).
func Remove(
	appID string,
	cfg *config.Config,
	state *config.State,
	env *envfile.File,
	dexClients []dex.Client,
	remainingDefs []*catalog.Definition,
) ([]dex.Client, error) {

	cs, ok := state.Containers[appID]
	if !ok {
		return dexClients, fmt.Errorf("app %q not installed", appID)
	}

	// Stop container.
	docker.ComposeStop(paths.ComposeFile(), compose.ServiceName(appID))

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

func createDataDirs(def *catalog.Definition) error {
	for _, v := range def.Volumes {
		if err := paths.EnsureDir(v.Host, 0o750); err != nil {
			return err
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
			if err := paths.AtomicWrite(c.Path, []byte(c.Content), mode); err != nil {
				return err
			}
		}
	}
	return nil
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
	if step.Wait > 0 {
		time.Sleep(time.Duration(step.Wait) * time.Second)
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
