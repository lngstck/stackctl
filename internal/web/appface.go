package web

import (
	"hash/fnv"
	"strings"
	"unicode"
)

// appFace is how an app looks on its tile: our own apps carry their hue from
// the design system (data-app), catalog apps a hue of their own on the tile
// only. Without an icon the tile shows a two-letter abbreviation.
type appFace struct {
	ID   string
	Own  bool
	Hue  int
	Icon string // sprite id in /static/ls/icons/icons.svg, empty for Abbr
	Abbr string
}

// appTile is one rendering of a tile: size and the three marks the design
// allows (alert, update, internet). Nothing else goes on a tile.
type appTile struct {
	Face     appFace
	Size     string // "", "s", "m", "xl"
	Off      bool
	Alert    bool
	Update   bool
	Internet bool
}

// ownApps have a hue and an icon in the design system (css/tokens.css).
var ownApps = map[string]bool{
	"pylearn": true, "handheld": true, "sponsorenlauf": true, "leihgeraete": true,
}

// knownFaces gives the basic services and a few catalog apps an icon from
// the sprite until the catalog can carry icons of its own.
var knownFaces = map[string]appFace{
	"postgres":   {Hue: 210, Icon: "database"},
	"dex":        {Hue: 260, Icon: "key"},
	"caddy":      {Hue: 190, Icon: "globe"},
	"llmd":       {Hue: 300, Icon: "spark"},
	"open-webui": {Hue: 175, Icon: "app-chat"},
}

// faceFor returns the tile face for an app.
func faceFor(id, name string) appFace {
	if ownApps[id] {
		return appFace{ID: id, Own: true, Icon: "app-" + id}
	}
	if f, ok := knownFaces[id]; ok {
		f.ID = id
		return f
	}
	return appFace{ID: id, Hue: hueFor(id), Abbr: abbreviate(name, id)}
}

// hueFor derives a stable hue from the app id, so an app keeps its colour
// across pages and restarts.
func hueFor(id string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return int(h.Sum32() % 360)
}

// abbreviate shortens a name to two letters: the initials of the first two
// words ("Uptime Kuma" → "UK", "CryptPad" → "CP"), else the first two
// letters of the only word ("Kiwix" → "Ki").
func abbreviate(name, fallback string) string {
	if strings.TrimSpace(name) == "" {
		name = fallback
	}
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}
	prevLower := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if unicode.IsUpper(r) && prevLower {
				flush()
			}
			cur = append(cur, r)
			prevLower = unicode.IsLower(r)
		default:
			flush()
			prevLower = false
		}
	}
	flush()
	switch {
	case len(words) >= 2:
		a := []rune(words[0])[0]
		b := []rune(words[1])[0]
		return strings.ToUpper(string(a)) + strings.ToUpper(string(b))
	case len(words) == 1:
		r := []rune(words[0])
		if len(r) == 1 {
			return strings.ToUpper(string(r))
		}
		return strings.ToUpper(string(r[0])) + strings.ToLower(string(r[1]))
	}
	return "?"
}

// categoryLabels translates the catalog's categories for the shelves.
var categoryLabels = map[string]string{
	"education":      "Unterricht",
	"teaching":       "Unterricht",
	"ai":             "KI",
	"collaboration":  "Zusammenarbeit",
	"tools":          "Werkzeuge",
	"organisation":   "Organisation",
	"organization":   "Organisation",
	"infrastructure": "Grundversorgung",
}

// categoryOrder puts the shelves in a fixed order; unknown ones follow.
var categoryOrder = []string{"Unterricht", "KI", "Zusammenarbeit", "Werkzeuge", "Organisation"}

func categoryLabel(c string) string {
	if l, ok := categoryLabels[strings.ToLower(c)]; ok {
		return l
	}
	if c == "" {
		return "Weitere"
	}
	r := []rune(c)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// isInfrastructure reports whether an app belongs to the basic services
// rather than the shelves: the mandatory services plus anything the catalog
// files under infrastructure (the AI gateway).
func isInfrastructure(id, category string) bool {
	return isMandatoryApp(id) || strings.EqualFold(category, "infrastructure")
}

// infraShortNames label the basic services in the row under the shelves.
var infraShortNames = map[string]string{
	"postgres": "Datenbank",
	"dex":      "Anmeldung",
	"caddy":    "Webzugang",
	"llmd":     "KI-Dienst",
}
