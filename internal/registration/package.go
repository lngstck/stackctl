package registration

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/update"
	"gopkg.in/yaml.v3"
)

// Payload is the YAML content inside the age-encrypted registration package.
// The operator decrypts it and feeds the fields into register-tunnel + schulen add.
type Payload struct {
	Slug            string `yaml:"slug"`
	SchoolName      string `yaml:"school_name"`
	ContactEmail    string `yaml:"contact_email,omitempty"`
	CreatedAt       string `yaml:"created_at"`
	StackctlVersion string `yaml:"stackctl_version"`
	ServerDomain    string `yaml:"server_domain"`
	SSHPublicKey    string `yaml:"ssh_public_key"`
	DexClientID     string `yaml:"dex_client_id"`
	DexClientSecret string `yaml:"dex_client_secret"`
	DexRedirectURI  string `yaml:"dex_redirect_uri"`
}

// BuildAndEncrypt creates the registration payload, encrypts it with the
// operator's public age key, and writes it to the standard path under
// config/registration-{slug}.age .
//
// It returns the file path on success. The file is ASCII-armored age.
func BuildAndEncrypt(p Payload) (string, error) {
	// Fill in auto-fields.
	if p.CreatedAt == "" {
		p.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if p.StackctlVersion == "" {
		p.StackctlVersion = update.CurrentVersion()
	}
	if p.DexRedirectURI == "" {
		p.DexRedirectURI = fmt.Sprintf(
			"https://auth.%s.learningstack.online/callback", p.Slug)
	}

	// Marshal payload to YAML.
	yamlData, err := yaml.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal registration payload: %w", err)
	}

	// Parse operator recipient.
	recipient, err := age.ParseX25519Recipient(OperatorPublicKey)
	if err != nil {
		return "", fmt.Errorf("parse operator pubkey: %w", err)
	}

	// Encrypt with ASCII armor.
	var buf bytes.Buffer
	armorWriter := armor.NewWriter(&buf)
	encWriter, err := age.Encrypt(armorWriter, recipient)
	if err != nil {
		return "", fmt.Errorf("age encrypt init: %w", err)
	}
	if _, err := encWriter.Write(yamlData); err != nil {
		return "", fmt.Errorf("age encrypt write: %w", err)
	}
	if err := encWriter.Close(); err != nil {
		return "", fmt.Errorf("age encrypt close: %w", err)
	}
	if err := armorWriter.Close(); err != nil {
		return "", fmt.Errorf("age armor close: %w", err)
	}

	// Write to file atomically.
	outPath := paths.RegistrationPackageFile(p.Slug)
	if err := paths.AtomicWrite(outPath, buf.Bytes(), 0o640); err != nil {
		return "", fmt.Errorf("write registration package: %w", err)
	}
	return outPath, nil
}

// PackageExists reports whether a registration package has been built.
func PackageExists(slug string) bool {
	_, err := os.Stat(paths.RegistrationPackageFile(slug))
	return err == nil
}

// PackagePath returns the path if it exists, empty string otherwise.
func PackagePath(slug string) string {
	p := paths.RegistrationPackageFile(slug)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}
