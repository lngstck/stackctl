package web

// PageData holds common data passed to all layout templates.
type PageData struct {
	NavActive  string // "dashboard", "apps", "settings", "tunnel"
	SchoolName string
	SchoolSlug string
}

// pageData creates a PageData with the current config values.
func (s *Server) pageData(navActive string) PageData {
	return PageData{
		NavActive:  navActive,
		SchoolName: s.cfg.School.Name,
		SchoolSlug: s.cfg.School.Slug,
	}
}
