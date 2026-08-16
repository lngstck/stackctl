package web

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/lock"
	"github.com/lngstck/stackctl/internal/publish"
)

// bootID is unique to this process. /healthz returns it in the X-Stackctl-Boot
// header so the job page can detect a self-update restart by a *changed* id
// rather than by observing a down→up transition — the latter is missed when
// systemd brings the new process back faster than the page's poll interval.
var bootID = newBootID()

func newBootID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a constant; worst case the page uses its wasDown
		// fallback path instead of boot-id detection.
		return "0"
	}
	return hex.EncodeToString(b)
}

//go:embed static/*
var staticFS embed.FS

//go:embed templates/*
var templateFS embed.FS

// Server is the stackctl web UI server.
type Server struct {
	cfg       *config.Config
	state     *config.State
	sessions  *sessionStore
	limiter   *rateLimiter
	pages     map[string]*template.Template // page name → compiled template
	publisher publish.Publisher
	jobs      *jobStore
	stateMu   sync.Mutex // guards s.state access against background job commits
	devMode   bool
	devDir    string // path to internal/web/ for dev-mode FS reload
	mux       *http.ServeMux
}

// Option configures the server.
type Option func(*Server)

// WithPublisher attaches the publisher so the web UI can publish apps and
// display their status. Handlers talk to this interface only — they must not
// branch on the transport.
func WithPublisher(p publish.Publisher) Option {
	return func(s *Server) { s.publisher = p }
}

// WithDevMode enables filesystem-based template/asset loading for live reload.
func WithDevMode(webDir string) Option {
	return func(s *Server) {
		s.devMode = true
		s.devDir = webDir
	}
}

// New creates a new Server with the given config and state.
func New(cfg *config.Config, state *config.State, opts ...Option) (*Server, error) {
	s := &Server{
		cfg:      cfg,
		state:    state,
		sessions: &sessionStore{},
		limiter:  newRateLimiter(),
		jobs:     newJobStore(),
		mux:      http.NewServeMux(),
	}

	for _, o := range opts {
		o(s)
	}

	if err := s.loadTemplates(); err != nil {
		return nil, fmt.Errorf("web: load templates: %w", err)
	}

	s.routes()
	return s, nil
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe(host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	log.Printf("stackctl web UI: http://%s", addr)
	return http.ListenAndServe(addr, s.mux)
}

// routes registers all HTTP routes.
func (s *Server) routes() {
	// Static assets.
	s.mux.Handle("GET /static/", s.staticHandler())

	// Health check.
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Stackctl-Boot", bootID)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	// Setup (needs_setup state).
	s.mux.HandleFunc("GET /setup", s.handleSetup)
	s.mux.HandleFunc("POST /setup", s.handleSetupPost)

	// Registration (awaiting_registration state).
	s.mux.HandleFunc("GET /setup/register", s.handleRegister)
	s.mux.HandleFunc("GET /setup/register/download", s.handleRegisterDownload)
	s.mux.HandleFunc("GET /setup/status", s.handleSetupStatus)
	s.mux.HandleFunc("GET /setup/preflight", s.handleSetupPreflight)
	s.mux.HandleFunc("POST /setup/register/skip", s.handleRegisterSkip)

	// Login/Logout (ready state).
	s.mux.HandleFunc("GET /login", s.handleLogin)
	s.mux.HandleFunc("POST /login", s.handleLoginPost)
	s.mux.HandleFunc("GET /logout", s.handleLogout)

	// Dashboard (ready + auth).
	s.mux.HandleFunc("GET /{$}", s.requireAuth(s.handleDashboard))

	// Apps (ready + auth).
	s.mux.HandleFunc("GET /apps", s.requireAuth(s.handleApps))
	s.mux.HandleFunc("GET /apps/{id}", s.requireAuth(s.handleAppDetail))
	s.mux.HandleFunc("GET /apps/{id}/install", s.requireAuth(s.handleAppInstallForm))
	// Install/Update run asynchronously and manage the op-lock themselves
	// (handed to the worker goroutine), so they use authPost, not
	// authPostLocked — wrapping them in withOpLock would double-acquire.
	s.mux.HandleFunc("POST /apps/{id}/install", s.authPost(s.handleAppInstallPost))
	s.mux.HandleFunc("POST /apps/{id}/update", s.authPost(s.handleAppUpdate))
	s.mux.HandleFunc("POST /apps/{id}/autoupdate", s.authPostLocked(s.handleAppAutoUpdateToggle))
	s.mux.HandleFunc("POST /apps/{id}/remove", s.authPostLocked(s.handleAppRemove))
	s.mux.HandleFunc("POST /apps/{id}/start", s.authPostLocked(s.handleAppStart))
	s.mux.HandleFunc("POST /apps/{id}/stop", s.authPostLocked(s.handleAppStop))

	// Settings (ready + auth).
	s.mux.HandleFunc("GET /settings", s.requireAuth(s.handleSettings))
	s.mux.HandleFunc("POST /settings", s.authPostLocked(s.handleSettingsPost))

	// Backup & Restore (ready + auth). Create runs asynchronously and manages
	// the op-lock itself (handed to the worker goroutine), so it uses authPost,
	// not authPostLocked — like install/update. Delete is a quick mutation and
	// takes the lock via authPostLocked.
	s.mux.HandleFunc("GET /backups", s.requireAuth(s.handleBackupsPage))
	s.mux.HandleFunc("POST /backups/create", s.authPost(s.handleBackupCreate))
	s.mux.HandleFunc("POST /backups/upload", s.authPost(s.handleBackupUpload))
	s.mux.HandleFunc("GET /backups/{name}/download", s.requireAuth(s.handleBackupDownload))
	s.mux.HandleFunc("POST /backups/{name}/delete", s.authPostLocked(s.handleBackupDelete))
	// Restore runs asynchronously and manages the op-lock itself (handed to the
	// worker), so it uses authPost like install — not authPostLocked.
	s.mux.HandleFunc("POST /backups/{name}/restore", s.authPost(s.handleBackupRestore))

	// Tunnel (ready + auth).
	s.mux.HandleFunc("GET /tunnel", s.requireAuth(s.handleTunnel))
	s.mux.HandleFunc("POST /tunnel/test", s.authPost(s.handleTunnelTest))
	s.mux.HandleFunc("POST /tunnel/dex/start", s.authPostLocked(s.handleDexTunnelStart))
	s.mux.HandleFunc("POST /tunnel/dex/stop", s.authPostLocked(s.handleDexTunnelStop))
	s.mux.HandleFunc("POST /apps/{id}/tunnel/enable", s.authPostLocked(s.handleAppTunnelEnable))
	s.mux.HandleFunc("POST /apps/{id}/tunnel/disable", s.authPostLocked(s.handleAppTunnelDisable))

	// LLM-Admin (ready + auth). UI sitzt unter /llm mit Tabs (Provider,
	// Personas, API-Keys); POST-Endpunkte mutieren die config.yaml und
	// schicken SIGHUP an ls-llmd. Modelle-Endpoint liefert JSON fuer den
	// Persona-Tab-Dropdown. Diese POSTs mutieren nur die llmd-Config (nicht
	// state/.env/compose) — sie brauchen CSRF, aber keinen Op-Lock.
	s.mux.HandleFunc("GET /llm", s.requireAuth(s.handleLLM))
	s.mux.HandleFunc("POST /llm/providers", s.authPost(s.handleLLMProviderCreate))
	s.mux.HandleFunc("POST /llm/providers/{id}/key", s.authPost(s.handleLLMProviderSetKey))
	s.mux.HandleFunc("POST /llm/providers/{id}/delete", s.authPost(s.handleLLMProviderDelete))
	s.mux.HandleFunc("GET /llm/providers/{id}/models", s.requireAuth(s.handleLLMProviderModels))
	s.mux.HandleFunc("POST /llm/personas", s.authPost(s.handleLLMPersonaCreate))
	s.mux.HandleFunc("POST /llm/personas/{id}/update", s.authPost(s.handleLLMPersonaUpdate))
	s.mux.HandleFunc("POST /llm/personas/{id}/deactivate", s.authPost(s.handleLLMPersonaDeactivate))
	s.mux.HandleFunc("POST /llm/personas/{id}/delete", s.authPost(s.handleLLMPersonaDelete))
	s.mux.HandleFunc("POST /llm/keys", s.authPost(s.handleLLMKeyCreate))
	s.mux.HandleFunc("POST /llm/keys/{id}/delete", s.authPost(s.handleLLMKeyDelete))

	// System POST-Endpunkte (ready + auth). Die /system-GET-Seite wurde in
	// /settings integriert (Issue #4); die POST-Routen bleiben, damit das
	// apps.html.tmpl + die System-Tab in settings.html.tmpl unveraendert
	// posten koennen. /system selbst gibt jetzt 404.
	s.mux.HandleFunc("POST /system/update", s.authPost(s.handleSystemUpdate))
	s.mux.HandleFunc("POST /system/catalog/sync", s.authPost(s.handleCatalogSync))

	// Job progress (ready + auth). The page polls the status endpoint while a
	// long-running install/update/self-update runs in the background (issue #1).
	s.mux.HandleFunc("GET /jobs/{id}", s.requireAuth(s.handleJobPage))
	s.mux.HandleFunc("GET /jobs/{id}/status", s.requireAuth(s.handleJobStatus))

	// Server IP detection (no auth — used in setup).
	s.mux.HandleFunc("GET /api/server-ip", s.handleServerIP)
}

// staticHandler returns a handler for /static/ assets.
func (s *Server) staticHandler() http.Handler {
	if s.devMode {
		dir := filepath.Join(s.devDir, "static")
		return http.StripPrefix("/static/", http.FileServer(http.Dir(dir)))
	}
	sub, _ := fs.Sub(staticFS, "static")
	return http.StripPrefix("/static/", http.FileServerFS(sub))
}

// templateFuncs returns the shared FuncMap for all templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"activeNav": func(current, page string) template.HTMLAttr {
			if current == page {
				return `aria-current="page"`
			}
			return ""
		},
		"csrfField": func(token string) template.HTML {
			// token is a hex string from crypto/rand; escape defensively anyway.
			return template.HTML(`<input type="hidden" name="csrf_token" value="` +
				template.HTMLEscapeString(token) + `">`)
		},
		"jsString": func(s string) template.JS {
			// Escape for safe embedding inside a JS string literal.
			r := strings.NewReplacer(
				`\`, `\\`,
				`'`, `\'`,
				`"`, `\"`,
				"\n", `\n`,
				"\r", ``,
			)
			return template.JS(r.Replace(s))
		},
	}
}

// loadTemplates builds a map of page templates. Each page template is
// parsed together with layout.html.tmpl so that layout defines (head, foot,
// sidebar-layout-start/end) are available. Because each page is a separate
// *template.Template, there are no define-name collisions.
func (s *Server) loadTemplates() error {
	readFile := func(name string) (string, error) {
		if s.devMode {
			data, err := os.ReadFile(filepath.Join(s.devDir, "templates", name))
			return string(data), err
		}
		data, err := templateFS.ReadFile("templates/" + name)
		return string(data), err
	}

	listPages := func() ([]string, error) {
		if s.devMode {
			entries, err := os.ReadDir(filepath.Join(s.devDir, "templates"))
			if err != nil {
				return nil, err
			}
			var names []string
			for _, e := range entries {
				if !e.IsDir() && e.Name() != "layout.html.tmpl" {
					names = append(names, e.Name())
				}
			}
			return names, nil
		}
		entries, err := templateFS.ReadDir("templates")
		if err != nil {
			return nil, err
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && e.Name() != "layout.html.tmpl" {
				names = append(names, e.Name())
			}
		}
		return names, nil
	}

	layoutContent, err := readFile("layout.html.tmpl")
	if err != nil {
		return fmt.Errorf("read layout: %w", err)
	}

	pageNames, err := listPages()
	if err != nil {
		return err
	}

	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		pageContent, err := readFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		t, err := template.New(name).Funcs(templateFuncs()).Parse(layoutContent)
		if err != nil {
			return fmt.Errorf("parse layout for %s: %w", name, err)
		}
		if _, err := t.New("page").Parse(pageContent); err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}
		pages[name] = t
	}

	s.pages = pages
	return nil
}

// render executes a page template with the given data. The page template
// is looked up by name (e.g. "dashboard.html.tmpl") and executed starting
// from the "page" tree.
func (s *Server) render(w http.ResponseWriter, tmpl string, data any) {
	// In dev mode, re-parse on every request for live reload.
	if s.devMode {
		if err := s.loadTemplates(); err != nil {
			http.Error(w, "template error: "+err.Error(), 500)
			return
		}
	}

	t, ok := s.pages[tmpl]
	if !ok {
		log.Printf("web: template %q not found", tmpl)
		http.Error(w, "Template nicht gefunden", 500)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "page", data); err != nil {
		log.Printf("web: render %s: %v", tmpl, err)
		http.Error(w, "Rendering-Fehler", 500)
	}
}

// requireAuth wraps a handler to redirect unauthenticated users to /login.
// It also enforces setup_state gating.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// State gating.
		switch s.cfg.SetupState {
		case config.SetupStateNeedsSetup:
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		case config.SetupStateAwaitingRegistration:
			http.Redirect(w, r, "/setup/register", http.StatusSeeOther)
			return
		}

		token := getSessionToken(r)
		if !s.sessions.valid(token) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// withOpLock serialises an infrastructure-mutating handler against the nightly
// `stackctl autoupdate` process and against a second concurrent web request
// (e.g. a double-click on "Installieren"). Without it both writers race on
// state.yaml, the compose .env and docker-compose.yml. On contention it does
// not queue — it redirects back with a friendly notice, because a duplicate
// install/update is never what the admin wanted. Wrap inside requireAuth so the
// lock is only taken for authenticated requests.
func (s *Server) withOpLock(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := s.tryLock(w, r)
		if !ok {
			return
		}
		defer h.Release()
		next(w, r)
	}
}

// tryLock acquires the op-lock or, on contention, writes a friendly busy
// redirect and returns ok=false. Async handlers (install/update/self-update)
// use it directly and hand the returned handle to their worker goroutine,
// which releases it when the job finishes — the lock must outlive the request.
func (s *Server) tryLock(w http.ResponseWriter, r *http.Request) (*lock.Handle, bool) {
	h, err := lock.Acquire()
	if err != nil {
		if errors.Is(err, lock.ErrBusy) {
			http.Redirect(w, r,
				"/apps?msg=Gerade+laeuft+eine+andere+Aktion+(z.B.+das+naechtliche+Auto-Update).+Bitte+in+einem+Moment+erneut+versuchen.&err=1",
				http.StatusSeeOther)
			return nil, false
		}
		log.Printf("web: acquire op lock: %v", err)
		http.Error(w, "Interner Fehler beim Sperren der Operation.", http.StatusInternalServerError)
		return nil, false
	}
	return h, true
}

// snapState returns a deep copy of the current state for read-only use. A
// background install/update job mutates a clone and commits it via
// commitState; readers must therefore work from a snapshot taken under the
// lock, never iterate s.state directly, or they race the commit (a concurrent
// map access crashes the process).
func (s *Server) snapState() *config.State {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.state.Clone()
}

// commitState publishes a mutated state clone back as the live state and saves
// it. The struct's map fields are reassigned in place so the shared pointer
// held by the tunnel manager stays coherent. Callers hold the op-lock, so two
// commits never overlap.
func (s *Server) commitState(working *config.State) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.state.Version = working.Version
	s.state.Containers = working.Containers
	s.state.Ports = working.Ports
	return s.state.Save()
}

// requireCSRF validates the csrf_token form field against the current session's
// token (ARCHITECTURE.md §16). It guards every authenticated, state-changing
// POST. Pre-auth POSTs (/login, /setup, /setup/register/skip) cannot carry a
// session-bound token and are intentionally not wrapped — they rely on the
// SameSite=Lax cookie and the setup-state machine instead.
//
// It parses the form here so the token is available; r.ParseForm caches, so
// handlers calling it again are unaffected. All stackctl forms are urlencoded.
func (s *Server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Ungueltige Formulardaten", http.StatusBadRequest)
			return
		}
		if !s.sessions.validCSRF(r.PostFormValue("csrf_token")) {
			log.Printf("web: CSRF token mismatch on %s %s from %s", r.Method, r.URL.Path, clientIP(r))
			http.Error(w, "Sicherheitspruefung fehlgeschlagen (CSRF). Bitte Seite neu laden und erneut versuchen.", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// authPost wraps an authenticated, state-changing POST handler with auth and
// CSRF validation. Use authPostLocked for handlers that also mutate
// state.yaml/.env/compose and must serialise against the autoupdate timer.
func (s *Server) authPost(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(s.requireCSRF(next))
}

func (s *Server) authPostLocked(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(s.requireCSRF(s.withOpLock(next)))
}

// clientIP extracts the remote IP, stripping the port.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// handleServerIP returns the server's LAN IP as JSON.
func (s *Server) handleServerIP(w http.ResponseWriter, r *http.Request) {
	ip := detectLANIP()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ip":%q}`, ip)
}

// detectLANIP finds the primary LAN IP via UDP dial (no actual packet sent).
func detectLANIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		if h, err := os.Hostname(); err == nil {
			return h
		}
		return "localhost"
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String()
}

// slugify converts a school name to a URL-safe slug.
func slugify(name string) string {
	name = strings.ToLower(name)
	replacer := strings.NewReplacer(
		"ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss",
		" ", "-", "_", "-",
	)
	name = replacer.Replace(name)
	var b strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		}
	}
	result := b.String()
	// Collapse multiple dashes.
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	result = strings.Trim(result, "-")
	return result
}

// bootstrapPublisher brings the public access up: the login first, then every
// app that was public before, then background supervision.
//
// It runs twice in a stackctl lifetime — once at startup for an install that
// is already set up, and once the moment registration completes, so the admin
// does not have to restart the service to get a login. Both paths must do the
// same thing, which is why they share this method rather than each keeping
// their own sequence.
func (s *Server) bootstrapPublisher() {
	if s.publisher == nil {
		return
	}
	if err := s.publisher.EnsureAuth(); err != nil {
		log.Printf("web: publish login: %v", err)
	}
	s.publisher.Restore(publish.AppsFrom(s.snapState(), catalog.ContainerPort))
	s.publisher.StartMonitor()
}
