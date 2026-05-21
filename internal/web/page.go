package web

// PageData holds common data passed to all layout templates.
type PageData struct {
	NavActive    string // "dashboard", "apps", "settings", "tunnel", "llm"
	SchoolName   string
	SchoolSlug   string
	LLMInstalled bool // controls whether the "LLM" sidebar entry is rendered
}

// pageData creates a PageData with the current config values. LLMInstalled
// gates the LLM sidebar entry — wir zeigen den Menuepunkt nicht, solange
// die llmd-App nicht installiert ist (Eintrag waere sonst eine Sackgasse:
// alles wuerde scheitern, weil /opt/learningstack/llmd/config/ fehlt).
func (s *Server) pageData(navActive string) PageData {
	return PageData{
		NavActive:    navActive,
		SchoolName:   s.cfg.School.Name,
		SchoolSlug:   s.cfg.School.Slug,
		LLMInstalled: s.state.IsInstalled("llmd"),
	}
}
