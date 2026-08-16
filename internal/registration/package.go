package registration

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/update"
	"gopkg.in/yaml.v3"
)

// PayloadVersion is the schema of the decrypted YAML. Version 1 described a
// world in which every school sat behind the operator's relay under
// {slug}.learningstack.online, so the operator could derive every address
// from the slug. Version 2 carries the address model explicitly, because a
// package no longer says by itself whether a tunnel has to be registered at
// all.
const PayloadVersion = 2

// Payload is the YAML content inside the age-encrypted registration package.
// The operator decrypts it and feeds the fields into register-tunnel + schulen add.
type Payload struct {
	PayloadVersion  int    `yaml:"payload_version"`
	Slug            string `yaml:"slug"`
	SchoolName      string `yaml:"school_name"`
	ContactEmail    string `yaml:"contact_email,omitempty"`
	CreatedAt       string `yaml:"created_at"`
	StackctlVersion string `yaml:"stackctl_version"`
	ServerDomain    string `yaml:"server_domain"`
	// Transport is "relay" or "direct" and tells the operator whether this
	// school needs a tunnel registration at all. A direct install needs only
	// the Dex client below — there is no SSH key to authorise and no
	// forwarding to allow.
	Transport string `yaml:"transport"`
	// BaseDomain is the parent of every public hostname of this school. For
	// a school on the operator's relay it is {slug}.learningstack.online; a
	// school with its own domain carries that instead, and the operator has
	// to allow it on the relay (sish runs with --verify-dns).
	BaseDomain string `yaml:"base_domain"`
	// SSHPublicKey authorises the reverse tunnel. Empty for direct installs,
	// which have no tunnel — sending a key the operator must not install
	// would only invite a pointless registration.
	SSHPublicKey    string `yaml:"ssh_public_key,omitempty"`
	DexClientID     string `yaml:"dex_client_id"`
	DexClientSecret string `yaml:"dex_client_secret"`
	DexRedirectURI  string `yaml:"dex_redirect_uri"`
}

// marshalPayload fills in the auto-fields, rejects an incomplete payload and
// returns the exact YAML that gets encrypted.
//
// It is separate from BuildAndEncrypt so tests can assert on those bytes: the
// package is encrypted to the operator's key, which nobody but the operator
// can decrypt, so this is the only point where the contents are still
// checkable. A test that re-marshals its own copy would prove nothing about
// what was actually written.
func marshalPayload(p Payload) ([]byte, error) {
	p.PayloadVersion = PayloadVersion
	if p.CreatedAt == "" {
		p.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if p.StackctlVersion == "" {
		p.StackctlVersion = update.CurrentVersion()
	}
	// The redirect URI is the one field the operator pastes verbatim into
	// `schulen add`, so it must come from the caller's config rather than be
	// guessed from the slug here — the school's address is no longer
	// derivable from its name.
	if p.DexRedirectURI == "" {
		return nil, errors.New("registration: dex_redirect_uri must be set")
	}
	// Without these two the operator cannot tell what to set up, and the
	// package would look like a v1 one that they may still derive addresses
	// from. Refusing here keeps a half-filled package from ever being sent.
	if p.Transport == "" {
		return nil, errors.New("registration: transport must be set")
	}
	if p.BaseDomain == "" {
		return nil, errors.New("registration: base_domain must be set")
	}

	data, err := yaml.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal registration payload: %w", err)
	}
	return data, nil
}

// BuildAndEncrypt creates the registration payload, encrypts it with the
// operator's public age key, and writes it to the standard path under
// config/registration-{slug}.age .
//
// It returns the file path on success. The file is ASCII-armored age.
func BuildAndEncrypt(p Payload) (string, error) {
	yamlData, err := marshalPayload(p)
	if err != nil {
		return "", err
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
