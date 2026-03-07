package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
)

// Renderer holds one compiled *template.Template per page name.
// Each entry is parsed from base.html + <page>.html in isolation so that
// {{define}} blocks from one page never bleed into another (the ParseGlob bug).
type Renderer struct {
	templates map[string]*template.Template
}

const (
	tmplBase = "web/templates/base.html"
	tmplDir  = "web/templates"
)

// NewRenderer parses every page template paired with the base layout.
// Call once at startup; fails fast if any template has a syntax error.
func NewRenderer() (*Renderer, error) {
	pages := []string{
		"app.html",
		"login.html",
		"mfa.html",
		"admin.html",
	}

	r := &Renderer{templates: make(map[string]*template.Template, len(pages))}

	for _, page := range pages {
		pagePath := filepath.Join(tmplDir, page)
		// New instance per page — base FIRST, then the page file.
		// This means the page's {{define}} blocks override the base's {{block}} defaults
		// without any other page's definitions being present in the same pool.
		t, err := template.New("base").ParseFiles(tmplBase, pagePath)
		if err != nil {
			return nil, fmt.Errorf("renderer: parse %s: %w", page, err)
		}
		name := page[:len(page)-5] // strip ".html"
		r.templates[name] = t
		log.Printf("renderer: loaded template %q", name)
	}

	return r, nil
}

// Render executes the named page template and writes it to w.
// Renders into a buffer first so a mid-execution error doesn't send partial HTML.
func (r *Renderer) Render(w http.ResponseWriter, name string, data interface{}) {
	t, ok := r.templates[name]
	if !ok {
		log.Printf("renderer: unknown template %q", name)
		http.Error(w, "page not found", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	// Always execute "base" — it pulls in the page's block overrides automatically.
	if err := t.ExecuteTemplate(&buf, "base", data); err != nil {
		log.Printf("renderer: execute %q: %v", name, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}
