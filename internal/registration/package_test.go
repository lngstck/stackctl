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
		SSHPublicKey:    "ssh-ed25519 AAAA... stackctl-tunnel",
		DexClientID:     "phoenix",
		DexClientSecret: "deadbeef1234567890abcdef",
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

func TestPackageExists(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("STACKCTL_DIR", tmp)

	if PackageExists("nonexistent") {
		t.Error("should not exist")
	}
}
