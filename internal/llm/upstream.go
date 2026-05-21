package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// upstreamFetchTimeout bricht /v1/models-Calls nach 5s ab. Die UI wartet
// synchron auf das Ergebnis (XHR aus dem Persona-Tab) — laenger fuehlt sich
// kaputt an, und wer in 5s nicht antwortet, ist sowieso nicht brauchbar
// als Live-Backend.
const upstreamFetchTimeout = 5 * time.Second

// modelsListResponse spiegelt das OpenAI-kompatible Schema von /v1/models.
// Wir lesen nur was wir brauchen — alle anderen Felder ignorieren wir, das
// haelt uns resilient gegen Provider-Erweiterungen.
type modelsListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// FetchUpstreamModels fragt {BaseURL}/v1/models ab und liefert die Liste
// der Modell-IDs sortiert zurueck.
//
// Auth: Bearer-Token, wenn p.APIKey gesetzt ist. Provider, die keinen Key
// brauchen (lokales Ollama etc.), bleiben Key-frei.
//
// Fehlerverhalten: jeder Fehler — Network, HTTP-Status != 2xx, Malformed
// JSON, fehlendes data[]-Feld — fuehrt zu einem aussagekraeftigen error.
// Die UI faellt dann auf Freitext-Eingabe zurueck.
func FetchUpstreamModels(p Provider) ([]string, error) {
	if p.BaseURL == "" {
		return nil, fmt.Errorf("provider %q: base_url is empty", p.ID)
	}
	url := strings.TrimRight(p.BaseURL, "/") + "/v1/models"

	ctx, cancel := context.WithTimeout(context.Background(), upstreamFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Beim Auslesen den Body cappen — manche Provider antworten mit
		// HTML-Errorseiten, das blaeht die error-Message sonst auf.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var parsed modelsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response from %s: %w", url, err)
	}

	ids := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
