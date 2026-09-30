package web

import (
	"bytes"
	"net/http"
	"strconv"

	"github.com/doesitomarchy/doesitomarchy/internal/search"
)

// maxQuery bounds the query length; longer input is cut, never an error.
const maxQuery = 200

func queryParam(r *http.Request) string {
	q := r.URL.Query().Get("q")
	if rs := []rune(q); len(rs) > maxQuery {
		q = string(rs[:maxQuery])
	}
	return q
}

// isHTMX reports an HTMX request, which gets the partial instead of the page.
func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

type searchData struct {
	Q    string
	Resp search.Response
}

// search serves GET /search?q=: a full page, or the results partial for HTMX.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := queryParam(r)
	d := searchData{Q: q, Resp: s.index.Search(q)}
	w.Header().Set("Vary", "HX-Request")
	if isHTMX(r) {
		s.renderPartial(w, r, "search", "results", d)
		return
	}
	title := "Search"
	if q != "" {
		title = q + " · search"
	}
	s.render(w, r, http.StatusOK, "search", title, d)
}

// suggest serves GET /search/suggest?q=&cursor=: an HTML list of completions.
// Each entry is a plain link, so it also works without JavaScript.
func (s *Server) suggest(w http.ResponseWriter, r *http.Request) {
	q := queryParam(r)
	cursor := -1
	if c, err := strconv.Atoi(r.URL.Query().Get("cursor")); err == nil {
		cursor = c
	}
	s.renderPartial(w, r, "suggest", "suggestions", s.index.Suggest(q, cursor))
}

// renderPartial executes one named template from a page's set, without the layout.
func (s *Server) renderPartial(w http.ResponseWriter, r *http.Request, page, name string, data any) {
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("render", "partial", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		buf.WriteTo(w)
	}
}
