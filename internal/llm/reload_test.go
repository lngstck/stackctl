package llm

import (
	"testing"
)

func TestReloadIdempotentWithoutContainer(t *testing.T) {
	// docker.IsRunning("ls-llmd") liefert false (kein Docker im Test-Env
	// vorhanden oder Container nicht aktiv) → Reload soll ohne Fehler
	// durchlaufen.
	if err := Reload(); err != nil {
		t.Errorf("expected nil when container not running, got %v", err)
	}
}
