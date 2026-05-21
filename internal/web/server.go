package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/tunnel"
)

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
	tunnelMgr *tunnel.Manager
	devMode   bool
	devDir    string // path to internal/web/ for dev-mode FS reload
	mux       *http.ServeMux
}

// Option configures the server.
type Option func(*Server)

// WithTunnelManager attaches a tunnel.Manager so the web UI can start/stop
// tunnels and display their status.
func WithTunnelManager(mgr *tunnel.Manager) Option {
	return func(s *Server) { s.tunnelMgr = mgr }
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
	s.mux.HandleFunc("POST /apps/{id}/install", s.requireAuth(s.handleAppInstallPost))
	s.mux.HandleFunc("POST /apps/{id}/update", s.requireAuth(s.handleAppUpdate))
	s.mux.HandleFunc("POST /apps/{id}/autoupdate", s.requireAuth(s.handleAppAutoUpdateToggle))
	s.mux.HandleFunc("POST /apps/{id}/remove", s.requireAuth(s.handleAppRemove))
	s.mux.HandleFunc("POST /apps/{id}/start", s.requireAuth(s.handleAppStart))
	s.mux.HandleFunc("POST /apps/{id}/stop", s.requireAuth(s.handleAppStop))

	// Settings (ready + auth).
	s.mux.HandleFunc("GET /settings", s.requireAuth(s.handleSettings))
	s.mux.HandleFunc("POST /settings", s.requireAuth(s.handleSettingsPost))

	// Tunnel (ready + auth).
	s.mux.HandleFunc("GET /tunnel", s.requireAuth(s.handleTunnel))
	s.mux.HandleFunc("POST /tunnel/test", s.requireAuth(s.handleTunnelTest))
	s.mux.HandleFunc("POST /apps/{id}/tunnel/enable", s.requireAuth(s.handleAppTunnelEnable))
	s.mux.HandleFunc("POST /apps/{id}/tunnel/disable", s.requireAuth(s.handleAppTunnelDisable))

	// System POST-Endpunkte (ready + auth). Die /system-GET-Seite wurde in
	// /settings integriert (Issue #4); die POST-Routen bleiben, damit das
	// apps.html.tmpl + die System-Tab in settings.html.tmpl unveraendert
	// posten koennen. /system selbst gibt jetzt 404.
	s.mux.HandleFunc("POST /system/update", s.requireAuth(s.handleSystemUpdate))
	s.mux.HandleFunc("POST /system/catalog/sync", s.requireAuth(s.handleCatalogSync))

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
