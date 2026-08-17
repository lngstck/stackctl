package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/lngstck/stackctl/internal/llm"
)

// llmData ist der Template-Context fuer llm.html.tmpl. Die Seite zeigt
// alle drei Aspekte (Provider, Personas, API-Keys) in einer Tab-UI, die
// Daten kommen aus genau einem llm.Load() — kein N+1, kein Caching.
type llmData struct {
	PageData
	Providers []llm.Provider
	Personas  []llm.Persona
	APIKeys   []llm.APIKey

	// Flash-States via Query-Param. NewKey wird *einmalig* nach
	// Key-Erstellung angezeigt; ein Reload entfernt ihn, weil er nur in
	// der URL lebt und nirgends gespeichert wird.
	Message   string
	Error     string
	NewKey    string // plaintext, wird nur direkt nach create gezeigt
	NewKeyID  string
	ActiveTab string // "providers" (default), "personas", "keys"
}

// llmPageData baut llmData aus dem aktuellen llm.File + Query-Flash.
func (s *Server) llmPageData(r *http.Request) (llmData, error) {
	f, err := llm.Load()
	if err != nil {
		return llmData{}, err
	}
	q := r.URL.Query()
	tab := q.Get("tab")
	switch tab {
	case "personas", "keys", "providers":
		// ok
	default:
		tab = "providers"
	}
	data := llmData{
		PageData:  s.pageData("llm"),
		Providers: f.Providers,
		Personas:  f.Personas,
		APIKeys:   f.APIKeys,
		Message:   q.Get("msg"),
		Error:     q.Get("err"),
		NewKey:    q.Get("new_key"),
		NewKeyID:  q.Get("new_key_id"),
		ActiveTab: tab,
	}
	return data, nil
}

// redirectLLM fuegt Query-Params zusammen und redirected nach /llm. tab
// steuert den initial sichtbaren Tab nach dem POST — der Hinweis bleibt
// dann thematisch beim Formular, das ausgeloest wurde.
func redirectLLM(w http.ResponseWriter, r *http.Request, tab, msg, errMsg string, extras url.Values) {
	q := url.Values{}
	if tab != "" {
		q.Set("tab", tab)
	}
	if msg != "" {
		q.Set("msg", msg)
	}
	if errMsg != "" {
		q.Set("err", errMsg)
	}
	for k, vs := range extras {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	target := "/llm"
	if encoded := q.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleLLM(w http.ResponseWriter, r *http.Request) {
	data, err := s.llmPageData(r)
	if err != nil {
		log.Printf("web: llm load: %v", err)
		http.Error(w, "llm: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "llm.html.tmpl", data)
}

// === Providers =============================================================

func (s *Server) handleLLMProviderCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirectLLM(w, r, "providers", "", "Ungueltige Formulardaten.", nil)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	kind := strings.TrimSpace(r.FormValue("kind"))
	if kind == "" {
		kind = "openai"
	}
	baseURL := strings.TrimSpace(r.FormValue("base_url"))
	apiKey := strings.TrimSpace(r.FormValue("api_key"))

	f, err := llm.Load()
	if err != nil {
		redirectLLM(w, r, "providers", "", "Konfiguration konnte nicht geladen werden.", nil)
		return
	}
	if err := f.AddProvider(llm.Provider{ID: id, Kind: kind, BaseURL: baseURL, APIKey: apiKey}); err != nil {
		redirectLLM(w, r, "providers", "", err.Error(), nil)
		return
	}
	if err := llm.SaveAndReload(f); err != nil {
		log.Printf("web: llm save: %v", err)
		redirectLLM(w, r, "providers", "", "Speichern fehlgeschlagen: "+err.Error(), nil)
		return
	}
	redirectLLM(w, r, "providers", "Provider "+id+" angelegt.", "", nil)
}

func (s *Server) handleLLMProviderSetKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		redirectLLM(w, r, "providers", "", "Ungueltige Formulardaten.", nil)
		return
	}
	key := strings.TrimSpace(r.FormValue("api_key"))
	clear := r.FormValue("clear") == "1"
	if clear {
		key = ""
	} else if key == "" {
		redirectLLM(w, r, "providers", "", "Bitte API-Key eingeben (oder 'Loeschen' verwenden).", nil)
		return
	}

	f, err := llm.Load()
	if err != nil {
		redirectLLM(w, r, "providers", "", "Konfiguration konnte nicht geladen werden.", nil)
		return
	}
	if err := f.SetProviderKey(id, key); err != nil {
		redirectLLM(w, r, "providers", "", err.Error(), nil)
		return
	}
	if err := llm.SaveAndReload(f); err != nil {
		log.Printf("web: llm save: %v", err)
		redirectLLM(w, r, "providers", "", "Speichern fehlgeschlagen: "+err.Error(), nil)
		return
	}
	if clear {
		redirectLLM(w, r, "providers", "API-Key fuer "+id+" entfernt.", "", nil)
	} else {
		redirectLLM(w, r, "providers", "API-Key fuer "+id+" aktualisiert.", "", nil)
	}
}

func (s *Server) handleLLMProviderDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := llm.Load()
	if err != nil {
		redirectLLM(w, r, "providers", "", "Konfiguration konnte nicht geladen werden.", nil)
		return
	}
	if err := f.RemoveProvider(id); err != nil {
		// "still referenced by persona"-Fehler aus der CRUD-Schicht
		// landet hier — sinnvoll als UI-Hinweis.
		redirectLLM(w, r, "providers", "", err.Error(), nil)
		return
	}
	if err := llm.SaveAndReload(f); err != nil {
		log.Printf("web: llm save: %v", err)
		redirectLLM(w, r, "providers", "", "Speichern fehlgeschlagen: "+err.Error(), nil)
		return
	}
	redirectLLM(w, r, "providers", "Provider "+id+" geloescht.", "", nil)
}

// handleLLMProviderModels liefert die Upstream-Modellliste als JSON. Wird
// von Persona-Tab-JS via fetch() gerufen, sobald der Admin einen Provider
// im Dropdown waehlt — Result populiert das <datalist> fuer Upstream-IDs.
//
// Response-Schema:
//
//	200 {"models":["gpt-4o","gpt-4o-mini",...]}
//	200 {"models":[],"error":"GET .../v1/models: HTTP 401: ..."}
//
// Wir benutzen IMMER 200, damit Fehler den HTTP-Status nicht ueberladen —
// das Frontend zeigt den Fehler als Hint neben dem Freitext-Eingabefeld.
func (s *Server) handleLLMProviderModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := llm.Load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"models": []string{}, "error": err.Error()})
		return
	}
	p := f.GetProvider(id)
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"models": []string{}, "error": "Provider nicht gefunden."})
		return
	}
	models, err := llm.FetchUpstreamModels(*p)
	if err != nil {
		log.Printf("web: llm fetch upstream models for %s: %v", id, err)
		writeJSON(w, http.StatusOK, map[string]any{"models": []string{}, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// === Personas ==============================================================

func (s *Server) handleLLMPersonaCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirectLLM(w, r, "personas", "", "Ungueltige Formulardaten.", nil)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	provider := strings.TrimSpace(r.FormValue("provider"))
	upstreamID := strings.TrimSpace(r.FormValue("upstream_id"))
	// Leeres Prompt-Feld = Passthrough. Keine separate Checkbox mehr —
	// das war redundant und widerspruechlich (Checkbox + "Leer = Passthrough").
	prompt := strings.TrimSpace(r.FormValue("prompt"))

	f, err := llm.Load()
	if err != nil {
		redirectLLM(w, r, "personas", "", "Konfiguration konnte nicht geladen werden.", nil)
		return
	}
	p := llm.Persona{
		ID:         id,
		Provider:   provider,
		UpstreamID: upstreamID,
		Prompt:     prompt,
	}
	if err := f.AddPersona(p); err != nil {
		redirectLLM(w, r, "personas", "", err.Error(), nil)
		return
	}
	if err := llm.SaveAndReload(f); err != nil {
		log.Printf("web: llm save: %v", err)
		redirectLLM(w, r, "personas", "", "Speichern fehlgeschlagen: "+err.Error(), nil)
		return
	}
	redirectLLM(w, r, "personas", "Persona "+id+" angelegt.", "", nil)
}

func (s *Server) handleLLMPersonaUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		redirectLLM(w, r, "personas", "", "Ungueltige Formulardaten.", nil)
		return
	}
	provider := strings.TrimSpace(r.FormValue("provider"))
	upstreamID := strings.TrimSpace(r.FormValue("upstream_id"))
	// Leeres Prompt-Feld = Passthrough (kein System-Prompt). Keine Checkbox.
	prompt := strings.TrimSpace(r.FormValue("prompt"))

	f, err := llm.Load()
	if err != nil {
		redirectLLM(w, r, "personas", "", "Konfiguration konnte nicht geladen werden.", nil)
		return
	}
	// Beide Aspekte koennen sich aendern — Upstream-Zuordnung und Prompt
	// in einem Submit. Bei Fehler im ersten Schritt bricht alles ab,
	// nichts wird gespeichert (in-memory mutation).
	if err := f.SetPersonaUpstream(id, provider, upstreamID); err != nil {
		redirectLLM(w, r, "personas", "", err.Error(), nil)
		return
	}
	if err := f.SetPersonaPrompt(id, prompt); err != nil {
		redirectLLM(w, r, "personas", "", err.Error(), nil)
		return
	}
	if err := llm.SaveAndReload(f); err != nil {
		log.Printf("web: llm save: %v", err)
		redirectLLM(w, r, "personas", "", "Speichern fehlgeschlagen: "+err.Error(), nil)
		return
	}
	redirectLLM(w, r, "personas", "Persona "+id+" aktualisiert.", "", nil)
}

// handleLLMPersonaDeactivate clears a persona's provider + upstream in one
// click, leaving the prompt intact. "Inaktiv" in llmd simply means the persona
// has no upstream to route to — so deactivating is just unlinking. Reactivating
// needs a provider + upstream_id, which the user picks via the edit form.
func (s *Server) handleLLMPersonaDeactivate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := llm.Load()
	if err != nil {
		redirectLLM(w, r, "personas", "", "Konfiguration konnte nicht geladen werden.", nil)
		return
	}
	if err := f.SetPersonaUpstream(id, "", ""); err != nil {
		redirectLLM(w, r, "personas", "", err.Error(), nil)
		return
	}
	if err := llm.SaveAndReload(f); err != nil {
		log.Printf("web: llm save: %v", err)
		redirectLLM(w, r, "personas", "", "Speichern fehlgeschlagen: "+err.Error(), nil)
		return
	}
	redirectLLM(w, r, "personas", "Persona "+id+" deaktiviert (Upstream entfernt, Prompt bleibt).", "", nil)
}

func (s *Server) handleLLMPersonaDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := llm.Load()
	if err != nil {
		redirectLLM(w, r, "personas", "", "Konfiguration konnte nicht geladen werden.", nil)
		return
	}
	if err := f.RemovePersona(id); err != nil {
		redirectLLM(w, r, "personas", "", err.Error(), nil)
		return
	}
	if err := llm.SaveAndReload(f); err != nil {
		log.Printf("web: llm save: %v", err)
		redirectLLM(w, r, "personas", "", "Speichern fehlgeschlagen: "+err.Error(), nil)
		return
	}
	redirectLLM(w, r, "personas", "Persona "+id+" geloescht.", "", nil)
}

// === API Keys ==============================================================

func (s *Server) handleLLMKeyCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirectLLM(w, r, "keys", "", "Ungueltige Formulardaten.", nil)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	// Multi-Select-Form-Field "personas" — leer = "(alle Personas)".
	allowed := r.Form["personas"]
	cleaned := make([]string, 0, len(allowed))
	for _, p := range allowed {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}

	f, err := llm.Load()
	if err != nil {
		redirectLLM(w, r, "keys", "", "Konfiguration konnte nicht geladen werden.", nil)
		return
	}
	plaintext, prefix, hash, err := llm.GenerateAPIKey()
	if err != nil {
		log.Printf("web: llm keygen: %v", err)
		redirectLLM(w, r, "keys", "", "Key-Generierung fehlgeschlagen.", nil)
		return
	}
	if err := f.AddAPIKey(llm.APIKey{ID: id, Prefix: prefix, Hash: hash, AllowedPersonas: cleaned}); err != nil {
		redirectLLM(w, r, "keys", "", err.Error(), nil)
		return
	}
	if err := llm.SaveAndReload(f); err != nil {
		log.Printf("web: llm save: %v", err)
		redirectLLM(w, r, "keys", "", "Speichern fehlgeschlagen: "+err.Error(), nil)
		return
	}
	// Klartext-Key landet in der URL und wird vom Template einmalig
	// gerendert. Sobald der Admin die Seite verlaesst, ist der Key weg —
	// genauso wie beim CLI-Weg (cmdKeyCreate druckt ihn nur einmal).
	extras := url.Values{}
	extras.Set("new_key", plaintext)
	extras.Set("new_key_id", id)
	redirectLLM(w, r, "keys", fmt.Sprintf("API-Key %s erstellt.", id), "", extras)
}

func (s *Server) handleLLMKeyDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := llm.Load()
	if err != nil {
		redirectLLM(w, r, "keys", "", "Konfiguration konnte nicht geladen werden.", nil)
		return
	}
	if err := f.RemoveAPIKey(id); err != nil {
		redirectLLM(w, r, "keys", "", err.Error(), nil)
		return
	}
	if err := llm.SaveAndReload(f); err != nil {
		log.Printf("web: llm save: %v", err)
		redirectLLM(w, r, "keys", "", "Speichern fehlgeschlagen: "+err.Error(), nil)
		return
	}
	redirectLLM(w, r, "keys", "API-Key "+id+" geloescht.", "", nil)
}
