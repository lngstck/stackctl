package compose

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lngstck/stackctl/internal/paths"
)

func TestBuildServiceBlockMinimal(t *testing.T) {
	def := &AppDefinition{
		ID:   "testing",
		Name: "Testing",
		Image: ImageSpec{
			Name: "registry.learningstack.online/testing",
			Tag:  "1.0.0",
		},
		Ports: []PortSpec{
			{Host: 8301, Container: 80, Description: "Web-UI"},
		},
	}

	svc := BuildServiceBlock(def, Options{})

	if svc["image"] != "registry.learningstack.online/testing:1.0.0" {
		t.Errorf("image = %v", svc["image"])
	}
	if svc["container_name"] != "ls-testing" {
		t.Errorf("container_name = %v", svc["container_name"])
	}
	if svc["restart"] != "unless-stopped" {
		t.Errorf("restart = %v", svc["restart"])
	}
	ports, ok := svc["ports"].([]string)
	if !ok || len(ports) != 1 || ports[0] != "0.0.0.0:8301:80" {
		t.Errorf("ports = %v", svc["ports"])
	}
}

func TestBuildServiceBlockFull(t *testing.T) {
	def := &AppDefinition{
		ID:   "langflow",
		Name: "Langflow",
		Image: ImageSpec{
			Name: "registry.learningstack.online/langflow",
			Tag:  "1.1.0",
		},
		Ports: []PortSpec{
			{Host: 8320, Container: 7860, Bind: "0.0.0.0"},
		},
		Volumes: []VolumeSpec{
			{Host: "/opt/learningstack/langflow/data", Container: "/app/data"},
			{Host: "/opt/learningstack/langflow/config", Container: "/app/config", ReadOnly: true},
		},
		Environment: []EnvVar{
			{Key: "LANGFLOW_DATABASE_URL", Value: "postgresql://langflow:${LANGFLOW_DB_PASSWORD}@ls-postgres:5432/langflow"},
			{Key: "LANGFLOW_OIDC_CLIENT_SECRET", Value: "${LANGFLOW_OIDC_SECRET}"},
		},
		DependsOn: []string{"postgres", "dex"},
	}

	svc := BuildServiceBlock(def, Options{})

	vols := svc["volumes"].([]string)
	if len(vols) != 2 {
		t.Errorf("volumes count = %d", len(vols))
	}
	if vols[1] != "/opt/learningstack/langflow/config:/app/config:ro" {
		t.Errorf("volumes[1] = %q", vols[1])
	}

	deps := svc["depends_on"].([]string)
	if len(deps) != 2 || deps[0] != "ls-postgres" || deps[1] != "ls-dex" {
		t.Errorf("depends_on = %v", deps)
	}

	env := svc["environment"].(map[string]string)
	if env["LANGFLOW_OIDC_CLIENT_SECRET"] != "${LANGFLOW_OIDC_SECRET}" {
		t.Errorf("OIDC secret = %q", env["LANGFLOW_OIDC_CLIENT_SECRET"])
	}
}

func TestBuildServiceBlockBindAddress(t *testing.T) {
	def := &AppDefinition{
		ID:    "postgres",
		Name:  "PostgreSQL",
		Image: ImageSpec{Name: "postgres", Tag: "16.2"},
		Ports: []PortSpec{
			{Host: 8100, Container: 5432, Bind: "127.0.0.1"},
		},
	}
	svc := BuildServiceBlock(def, Options{})
	ports := svc["ports"].([]string)
	if ports[0] != "127.0.0.1:8100:5432" {
		t.Errorf("bind port = %q", ports[0])
	}
}

func TestRegenerateCreatesValidYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(paths.EnvStackctlDir, dir)

	defs := []*AppDefinition{
		{
			ID:    "postgres",
			Name:  "PostgreSQL",
			Image: ImageSpec{Name: "postgres", Tag: "16.2"},
			Ports: []PortSpec{{Host: 8100, Container: 5432, Bind: "127.0.0.1"}},
			Volumes: []VolumeSpec{
				{Host: "/opt/learningstack/postgres/data", Container: "/var/lib/postgresql/data"},
			},
			Environment: []EnvVar{
				{Key: "POSTGRES_PASSWORD", Value: "${POSTGRES_PASSWORD}"},
			},
		},
		{
			ID:        "dex",
			Name:      "Dex",
			Image:     ImageSpec{Name: "ghcr.io/dexidp/dex", Tag: "v2.45.1"},
			Ports:     []PortSpec{{Host: 5556, Container: 5556, Bind: "0.0.0.0"}},
			DependsOn: []string{"postgres"},
		},
	}

	if err := Regenerate(defs, Options{}); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}

	raw, err := os.ReadFile(paths.ComposeFile())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(raw)
	if !strings.HasPrefix(content, Banner) {
		t.Error("banner missing")
	}

	// Verify it's valid YAML.
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, content)
	}

	// Network block present.
	networks, ok := doc["networks"]
	if !ok {
		t.Fatal("networks key missing")
	}
	net := networks.(map[string]any)["learningstack"].(map[string]any)
	if net["external"] != true {
		t.Errorf("network not external: %v", net)
	}

	// Services present and sorted (dex before postgres).
	services := doc["services"].(map[string]any)
	if _, ok := services["ls-postgres"]; !ok {
		t.Error("ls-postgres service missing")
	}
	if _, ok := services["ls-dex"]; !ok {
		t.Error("ls-dex service missing")
	}

	// dex depends on ls-postgres.
	dexSvc := services["ls-dex"].(map[string]any)
	deps := dexSvc["depends_on"].([]any)
	if len(deps) != 1 || deps[0] != "ls-postgres" {
		t.Errorf("dex depends_on = %v", deps)
	}
}

func TestRegenerateEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(paths.EnvStackctlDir, dir)

	if err := Regenerate(nil, Options{}); err != nil {
		t.Fatalf("Regenerate(nil, Options{}): %v", err)
	}
	raw, _ := os.ReadFile(paths.ComposeFile())
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid YAML from empty regen: %v", err)
	}
	if _, ok := doc["services"]; ok {
		t.Error("empty regen should have no services block")
	}
}

func TestImageSpecFullImage(t *testing.T) {
	cases := []struct {
		name, tag, want string
	}{
		{"postgres", "16.2", "postgres:16.2"},
		{"registry.learningstack.online/testing", "1.0.0", "registry.learningstack.online/testing:1.0.0"},
		{"ubuntu", "", "ubuntu"},
	}
	for _, tc := range cases {
		got := ImageSpec{Name: tc.name, Tag: tc.tag}.FullImage()
		if got != tc.want {
			t.Errorf("FullImage(%q,%q) = %q, want %q", tc.name, tc.tag, got, tc.want)
		}
	}
}

func TestAppIDFromContainer(t *testing.T) {
	id, ok := AppIDFromContainer("ls-langflow")
	if !ok || id != "langflow" {
		t.Errorf("got %q, %v", id, ok)
	}
	_, ok = AppIDFromContainer("nginx")
	if ok {
		t.Error("nginx should not match")
	}
}

func TestEnvRef(t *testing.T) {
	if got := EnvRef("POSTGRES_PASSWORD"); got != "${POSTGRES_PASSWORD}" {
		t.Errorf("EnvRef = %q", got)
	}
}

func TestServiceName(t *testing.T) {
	if got := ServiceName("langflow"); got != "ls-langflow" {
		t.Errorf("ServiceName = %q", got)
	}
}

// On a server that publishes itself, every app port must be bound to
// localhost. The catalog default of 0.0.0.0 would put each app on its own
// high port straight onto the public IP, unauthenticated and unencrypted,
// right next to the proxy that is meant to be the only way in.
func TestBuildServiceBlockBindLocalhost(t *testing.T) {
	def := &AppDefinition{
		ID:    "pylearn",
		Image: ImageSpec{Name: "ghcr.io/lngstck/pylearn", Tag: "0.8.0"},
		Ports: []PortSpec{
			{Host: 8330, Container: 8000},
			{Host: 8331, Container: 8001, Bind: "0.0.0.0"},
		},
	}

	svc := BuildServiceBlock(def, Options{BindLocalhost: true})
	ports, ok := svc["ports"].([]string)
	if !ok {
		t.Fatalf("ports = %T, want []string", svc["ports"])
	}
	want := []string{"127.0.0.1:8330:8000", "127.0.0.1:8331:8001"}
	for i, w := range want {
		if ports[i] != w {
			t.Errorf("ports[%d] = %q, want %q", i, ports[i], w)
		}
	}

	// A relay install keeps the LAN reachable — that is a feature there.
	svc = BuildServiceBlock(def, Options{})
	ports, _ = svc["ports"].([]string)
	if ports[0] != "0.0.0.0:8330:8000" {
		t.Errorf("relay ports[0] = %q, want 0.0.0.0:8330:8000", ports[0])
	}
}

// The reverse proxy is the exception to the localhost rule: it IS the way in.
// Confining it would take every published app offline instead of protecting
// anything.
func TestBuildServiceBlockPublicEntrypointStaysExposed(t *testing.T) {
	proxy := &AppDefinition{
		ID:               "caddy",
		Image:            ImageSpec{Name: "caddy", Tag: "2-alpine"},
		PublicEntrypoint: true,
		Ports: []PortSpec{
			{Host: 80, Container: 80},
			{Host: 443, Container: 443},
		},
	}

	svc := BuildServiceBlock(proxy, Options{BindLocalhost: true})
	ports, _ := svc["ports"].([]string)
	for i, want := range []string{"0.0.0.0:80:80", "0.0.0.0:443:443"} {
		if ports[i] != want {
			t.Errorf("ports[%d] = %q, want %q", i, ports[i], want)
		}
	}
}
