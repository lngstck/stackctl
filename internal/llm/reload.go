package llm

import (
	"github.com/lngstck/stackctl/internal/docker"
)

// ContainerName ist der Docker-Container-Name der llmd-Instanz.
const ContainerName = "ls-llmd"

// Reload schickt SIGHUP an den llmd-Container, damit er die config.yaml +
// prompts/*.md neu einliest. Idempotent: wenn der Container nicht laeuft
// (z.B. weil llmd noch nicht installiert ist), passiert nichts. Der naechste
// Start nimmt die neue Config automatisch mit auf.
func Reload() error {
	if !docker.IsRunning(ContainerName) {
		return nil
	}
	return docker.SendSignal(ContainerName, "HUP")
}

// SaveAndReload schreibt die Config und schickt anschliessend SIGHUP.
// Convenience-Wrapper fuer die haeufigste Operation in den CLI/Web-Handlern.
func SaveAndReload(f *File) error {
	if err := Save(f); err != nil {
		return err
	}
	return Reload()
}
