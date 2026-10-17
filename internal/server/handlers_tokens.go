package server

import (
	"net/http"

	"github.com/tylergoza/church-directory/internal/store"
)

// API tokens (admin): app tokens for the other church apps. The routes
// that use them are in api.go.

// tokenForm is what was typed into the "make a token" form, kept when it
// comes back with errors.
type tokenForm struct {
	Name   string
	Errors []string
}

func (s *Server) renderTokens(w http.ResponseWriter, r *http.Request, status int, form tokenForm, newToken string) {
	tokens, err := s.store.ListTokens(store.TokenApp)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "tokens/index", map[string]any{
		"Title": "API tokens", "Tokens": tokens, "Form": form, "NewToken": newToken,
		"APIBase": s.baseURL(r) + "/api/v1",
	})
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	s.renderTokens(w, r, http.StatusOK, tokenForm{}, "")
}

// handleTokenCreate makes a token and shows it on the page, the one time it
// can be seen; it isn't put in a flash message or redirect.
func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	f := tokenForm{Name: formStr(r, "name")}
	if f.Name == "" {
		f.Errors = append(f.Errors, "Name what will use it, like Event planner.")
	}
	if len(f.Name) > 100 {
		f.Errors = append(f.Errors, "Keep the name under 100 characters.")
	}
	if f.Errors != nil {
		s.renderTokens(w, r, http.StatusUnprocessableEntity, f, "")
		return
	}
	token, err := s.store.CreateToken(f.Name, store.TokenApp, currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderTokens(w, r, http.StatusOK, tokenForm{}, token)
}

func (s *Server) handleTokenDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteToken(pathID(r)); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/tokens", "Token revoked. Anything using it can no longer reach the directory.")
}
