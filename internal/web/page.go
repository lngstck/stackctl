package web

// PageData holds fields common to all authenticated pages.
type PageData struct {
	NavActive    string // "dashboard", "apps", "settings", "public", "backups", "llm"
	SchoolName   string
	SchoolSlug   string
	LLMInstalled bool   // controls whether the "KI" dock entry is rendered
	CSRFToken    string // per-session token, rendered into every form via {{csrfField}}
	// Search shows the app search in the top bar. Only pages whose content
	// it filters (data-ls-item) set it.
	Search bool
	// OpenSheet is the id of a <dialog> the page opens on load, so a link
	// such as /apps/{id} lands on the page with that app's sheet open.
	OpenSheet string
}

// pageData creates a PageData with the current config values. LLMInstalled
// gates the KI dock entry — wir zeigen den Eintrag nicht, solange die
// llmd-App nicht installiert ist (Eintrag waere sonst eine Sackgasse:
// alles wuerde scheitern, weil /opt/learningstack/llmd/config/ fehlt).
func (s *Server) pageData(navActive string) PageData {
	return PageData{
		NavActive:    navActive,
		SchoolName:   s.cfg.School.Name,
		SchoolSlug:   s.cfg.School.Slug,
		LLMInstalled: s.snapState().IsInstalled("llmd"),
		CSRFToken:    s.sessions.csrfToken(),
	}
}
