package webshell

import (
	"context"
	"net/http"
	"strings"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// PreventionListFunc performs the daemon-side prevention read. The shell does
// not connect to PostgreSQL or reproduce artifact filtering and state rules.
type PreventionListFunc func(ctx context.Context, request contract.PreventionListRequest) (contract.PreventionListResponse, error)

// EnablePreventions adds the optional read-only prevention projection without
// changing NewWithDetail or enabling any verification, activation or writes.
func (s *Server) EnablePreventions(list PreventionListFunc) {
	s.preventions = list
}

// servePreventions returns only artifacts associated with the issue in the
// route. Query parameters cannot broaden that scope or supply repository data.
func (s *Server) servePreventions(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	issueID := strings.TrimSpace(r.PathValue("id"))
	if !issueIDPattern.MatchString(issueID) {
		writeError(w, http.StatusNotFound, "not_found", "no issue has that id")
		return
	}
	if s.preventions == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the prevention view is not available in this launch")
		return
	}
	response, err := s.preventions(r.Context(), contract.PreventionListRequest{
		SchemaVersion: contract.PreventionSchemaVersion,
		IssueID:       issueID,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "unavailable", "the errkb core is not reachable")
		return
	}
	writeJSON(w, http.StatusOK, response)
}
