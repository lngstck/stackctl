package install

import (
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/compose"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/envfile"
)

// caddyLikeDef references the deSEC token the way the catalog's caddy
// definition does.
func caddyLikeDef() *catalog.Definition {
	def := &catalog.Definition{}
	def.ID = "caddy"
	def.Environment = []compose.EnvVar{{Key: "DESEC_TOKEN", Value: "${DESEC_TOKEN}"}}
	return def
}

func modeConfig(transport, token string) *config.Config {
	cfg := config.Default()
	cfg.School.Slug = "phoenix"
	cfg.Public.Transport = transport
	cfg.Public.BaseDomain = "ls.gym-phoenix.de"
	if transport == config.TransportLocal {
		cfg.Public.Local.DNSToken = token
	}
	return cfg
}

// TestCheckSystemEnvDepsDeSECToken: The proxy references DESEC_TOKEN in both
// modes. In the direct mode it is empty by design (HTTP-01) and must not stop
// the install — that was the one thing keeping a server on the internet from
// getting its proxy at all. In the school network an empty token still stops
// it, as before.
func TestCheckSystemEnvDepsDeSECToken(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *config.Config
		wantErr bool
	}{
		{"direkter Betrieb ohne Token", modeConfig(config.TransportDirect, ""), false},
		{"Schulnetz mit Token", modeConfig(config.TransportLocal, "tok-123"), false},
		{"Schulnetz ohne Token", modeConfig(config.TransportLocal, ""), true},
		{"Betriebsart unbekannt", modeConfig("", ""), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := envfile.New()
			envfile.ApplySystemEnv(env, tc.cfg, "geheim123")
			err := checkSystemEnvDeps(caddyLikeDef(), tc.cfg, env)
			if tc.wantErr && (err == nil || !strings.Contains(err.Error(), "DESEC_TOKEN")) {
				t.Fatalf("err = %v, want a complaint about DESEC_TOKEN", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("err = %v, want none", err)
			}
		})
	}

	t.Run("ohne Config", func(t *testing.T) {
		env := envfile.New()
		env.Set(envfile.GlobalSection, "DESEC_TOKEN", "")
		if err := checkSystemEnvDeps(caddyLikeDef(), nil, env); err == nil {
			t.Fatal("empty token without a config must still count as missing")
		}
	})
}

// TestCheckSystemEnvDepsDirectStillChecksOthers: the exception covers the
// deSEC token only. An empty admin password stays an error in the direct mode.
func TestCheckSystemEnvDepsDirectStillChecksOthers(t *testing.T) {
	def := &catalog.Definition{}
	def.ID = "open-webui"
	def.Environment = []compose.EnvVar{{Key: "WEBUI_ADMIN_PASSWORD", Value: "${ADMIN_PASSWORD}"}}

	cfg := modeConfig(config.TransportDirect, "")
	env := envfile.New()
	envfile.ApplySystemEnv(env, cfg, "")
	env.Set(envfile.GlobalSection, "ADMIN_PASSWORD", "")

	err := checkSystemEnvDeps(def, cfg, env)
	if err == nil || !strings.Contains(err.Error(), "ADMIN_PASSWORD") {
		t.Fatalf("err = %v, want a complaint about ADMIN_PASSWORD", err)
	}
}
