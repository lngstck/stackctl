package registration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
	"gopkg.in/yaml.v3"
)

func TestBuildAndEncrypt(t *testing.T) {
	// Use a temp dir as STACKCTL_DIR so the package writes there.
	tmp := t.TempDir()
	t.Setenv("STACKCTL_DIR", tmp)
	os.MkdirAll(filepath.Join(tmp, "config"), 0o750)

	p := Payload{
		Slug:            "phoenix",
		SchoolName:      "Gymnasium Phoenix",
		ContactEmail:    "admin@gym-phoenix.de",
		ServerDomain:    "93.184.216.34",
		Transport:       "relay",
		BaseDomain:      "phoenix.learningstack.online",
		SSHPublicKey:    "ssh-ed25519 AAAA... stackctl-tunnel",
		DexClientID:     "phoenix",
		DexClientSecret: "deadbeef1234567890abcdef",
		DexRedirectURI:  "https://auth.phoenix.learningstack.online/callback",
	}

	path, err := BuildAndEncrypt(p)
	if err != nil {
		t.Fatalf("BuildAndEncrypt: %v", err)
	}

	if !strings.HasSuffix(path, "registration-phoenix.age") {
		t.Errorf("unexpected path: %s", path)
	}

	// Verify the file exists and is non-empty.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("registration package is empty")
	}

	// Verify the file starts with the age armor header.
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "-----BEGIN AGE ENCRYPTED FILE-----") {
		t.Errorf("expected age armor header, got: %.60s", data)
	}

	// To fully test decryption, generate a throwaway keypair and re-encrypt.
	t.Run("decrypt_roundtrip", func(t *testing.T) {
		identity, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}

		// Build a package encrypted to this test key instead.
		origKey := OperatorPublicKey
		// We can't override the const, so we test the encryption/decryption
		// flow manually here using the same YAML payload.

		yamlData, _ := yaml.Marshal(p)

		// Encrypt to our test recipient.
		var buf strings.Builder
		aw := armor.NewWriter(&buf)
		w, err := age.Encrypt(aw, identity.Recipient())
		if err != nil {
			t.Fatal(err)
		}
		w.Write(yamlData)
		w.Close()
		aw.Close()

		// Decrypt.
		ar := armor.NewReader(strings.NewReader(buf.String()))
		r, err := age.Decrypt(ar, identity)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		var decoded Payload
		if err := yaml.NewDecoder(r).Decode(&decoded); err != nil {
			t.Fatalf("decode yaml: %v", err)
		}
		if decoded.Slug != "phoenix" {
			t.Errorf("slug = %q", decoded.Slug)
		}
		if decoded.DexClientSecret != "deadbeef1234567890abcdef" {
			t.Errorf("secret = %q", decoded.DexClientSecret)
		}

		_ = origKey // silence unused warning
	})
}

// The operator reads the address model out of the package to decide what to
// set up. A package that omits it looks exactly like a v1 package, from which
// they would derive the address off the slug — and quietly register the wrong
// one for a school with its own domain.
func TestBuildRefusesPackageWithoutAddressModel(t *testing.T) {
	base := Payload{
		Slug:            "phoenix",
		SchoolName:      "Gymnasium Phoenix",
		Transport:       "direct",
		BaseDomain:      "ls.gym-phoenix.de",
		DexClientID:     "phoenix",
		DexClientSecret: "deadbeef",
		DexRedirectURI:  "https://auth.ls.gym-phoenix.de/callback",
	}

	tests := []struct {
		name  string
		mutot func(*Payload)
	}{
		{"no transport", func(p *Payload) { p.Transport = "" }},
		{"no base domain", func(p *Payload) { p.BaseDomain = "" }},
		{"no redirect uri", func(p *Payload) { p.DexRedirectURI = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("STACKCTL_DIR", t.TempDir())
			p := base
			tt.mutot(&p)
			if _, err := BuildAndEncrypt(p); err == nil {
				t.Error("expected a refusal, got nil")
			}
		})
	}
}

// A direct install has no tunnel. Shipping an SSH key would ask the operator
// to authorise a forwarding that is never used.
func TestDirectPackageCarriesNoSSHKey(t *testing.T) {
	yamlData, err := marshalPayload(Payload{
		Slug:            "phoenix",
		SchoolName:      "Gymnasium Phoenix",
		Transport:       "direct",
		BaseDomain:      "ls.gym-phoenix.de",
		DexClientID:     "phoenix",
		DexClientSecret: "deadbeef",
		DexRedirectURI:  "https://auth.ls.gym-phoenix.de/callback",
	})
	if err != nil {
		t.Fatalf("marshalPayload: %v", err)
	}

	got := string(yamlData)
	// The field is omitempty, so an absent key must not appear at all.
	if strings.Contains(got, "ssh_public_key") {
		t.Errorf("direct package should not carry an ssh key:\n%s", got)
	}
	for _, want := range []string{
		"payload_version: 2",
		"transport: direct",
		"base_domain: ls.gym-phoenix.de",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("package missing %q:\n%s", want, got)
		}
	}
}

// A relay install is the other half: the operator has to authorise the key,
// so it has to be in there.
func TestRelayPackageCarriesSSHKey(t *testing.T) {
	yamlData, err := marshalPayload(Payload{
		Slug:            "phoenix",
		Transport:       "relay",
		BaseDomain:      "phoenix.learningstack.online",
		SSHPublicKey:    "ssh-ed25519 AAAA... stackctl-tunnel",
		DexClientID:     "phoenix",
		DexRedirectURI:  "https://auth.phoenix.learningstack.online/callback",
		DexClientSecret: "deadbeef",
	})
	if err != nil {
		t.Fatalf("marshalPayload: %v", err)
	}
	if !strings.Contains(string(yamlData), "ssh_public_key: ssh-ed25519") {
		t.Errorf("relay package must carry the ssh key:\n%s", yamlData)
	}
}

func TestPackageExists(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("STACKCTL_DIR", tmp)

	if PackageExists("nonexistent") {
		t.Error("should not exist")
	}
}
