// Package webshell serves the embedded toolbox UI on the loopback interface.
// It is a presentation layer only: every operation is delegated to the daemon
// through the injected SearchFunc, never to PostgreSQL directly.
package webshell

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/ui"
)

//go:embed assets
var assetFS embed.FS

// SearchFunc performs the daemon-side search. The shell never builds a query
// of its own beyond the user's text.
type SearchFunc func(ctx context.Context, request contract.SearchRequest) (contract.SearchResponse, error)

// Server is a loopback-only web shell for one process lifetime.
type Server struct {
	token             string
	page              []byte
	assets            fs.FS
	search            SearchFunc
	detail            GetIssueFunc
	preventions       PreventionListFunc
	record            RecordSolutionFunc
	setStatus         SetStatusFunc
	setSolutionStatus SetSolutionStatusFunc
	deleteSolution    DeleteSolutionFunc
	stats             StatsFunc
	audit             AuditFunc
	settings          *ui.SettingsPage
}

// New builds the shell with a fresh per-launch token and search only.
func New(search SearchFunc) (*Server, error) {
	return NewWithDetail(search, nil)
}

// NewWithDetail also wires the detail read; nil keeps the search-only shell.
func NewWithDetail(search SearchFunc, detail GetIssueFunc) (*Server, error) {
	if search == nil {
		return nil, errors.New("webshell: a search function is required")
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("webshell: create the token: %w", err)
	}
	sub, err := fs.Sub(assetFS, "assets")
	if err != nil {
		return nil, fmt.Errorf("webshell: open the assets: %w", err)
	}
	page, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, fmt.Errorf("webshell: read the page: %w", err)
	}
	encoded := hex.EncodeToString(token)
	return &Server{
		token:  encoded,
		page:   []byte(strings.ReplaceAll(string(page), "__TOKEN__", encoded)),
		assets: sub,
		search: search,
		detail: detail,
	}, nil
}

// Token returns the per-launch token; it is only placed in the opened URL.
func (s *Server) Token() string { return s.token }

// Listen binds the shell to 127.0.0.1. Port 0 asks the system for a free port.
func (s *Server) Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

// URL is the address the browser must open.
func (s *Server) URL(listener net.Listener) string {
	return fmt.Sprintf("http://%s/?token=%s", listener.Addr().String(), s.token)
}

// Handler serves the page, the assets and the search endpoint.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.servePage)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(s.assets))))
	mux.HandleFunc("POST /api/search", s.serveSearch)
	mux.HandleFunc("GET /api/issues/{id}", s.serveIssue)
	mux.HandleFunc("GET /api/issues/{id}/preventions", s.servePreventions)
	mux.HandleFunc("GET /issues/{id}", s.servePage)
	mux.HandleFunc("GET /stats", s.servePage)
	mux.HandleFunc("GET /settings", s.servePage)
	mux.HandleFunc("POST /api/issues/{id}/versions", s.serveVersion)
	mux.HandleFunc("POST /api/issues/{id}/status", s.serveIssueStatus)
	mux.HandleFunc("POST /api/solutions/{id}/status", s.serveSolutionStatus)
	mux.HandleFunc("POST /api/solutions/{id}/delete", s.serveSolutionDelete)
	mux.HandleFunc("GET /api/stats", s.serveStats)
	mux.HandleFunc("GET /api/audit", s.serveAudit)
	mux.HandleFunc("GET /api/settings", s.serveSettings)
	mux.HandleFunc("POST /api/settings/connectors", s.serveConnectorToggle)
	mux.HandleFunc("POST /api/settings/capture", s.serveCaptureToggle)
	mux.HandleFunc("POST /api/settings/projects", s.serveProjectToggle)
	mux.HandleFunc("POST /api/settings/retention", s.serveRetention)
	mux.HandleFunc("POST /api/settings/retention/apply", s.serveRetentionApply)
	mux.HandleFunc("POST /api/settings/http", s.serveHTTPToggle)
	mux.HandleFunc("POST /api/deletions/preview", s.serveDeletionPreview)
	mux.HandleFunc("POST /api/deletions/confirm", s.serveDeletionConfirm)
	mux.HandleFunc("POST /api/deletions/cancel", s.serveDeletionCancel)
	return withHeaders(mux)
}

func (s *Server) servePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.page)
}

func (s *Server) serveSearch(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "the search body is not a valid request: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Query) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "a non-empty query is required")
		return
	}
	response, err := s.search(r.Context(), contract.SearchRequest{
		SchemaVersion: "v1",
		Query:         strings.TrimSpace(body.Query),
		Limit:         intPointer(3),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "unavailable", "the errkb core is not reachable")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) authorized(r *http.Request) bool {
	given := r.Header.Get("X-Errkb-Token")
	return subtle.ConstantTimeCompare([]byte(given), []byte(s.token)) == 1
}

func withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func intPointer(value int) *int { return &value }
