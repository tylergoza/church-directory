package server

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/tylergoza/church-directory/internal/store"
)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Static assets & PWA plumbing
	mux.Handle("GET /static/", s.staticHandler())
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker)
	mux.HandleFunc("GET /manifest.webmanifest", s.handleManifest)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.asset("icons/icon.svg"), http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /offline", s.handleOffline)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.DB.PingContext(r.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})

	// Open to visitors: only signing in and first-run setup. The directory
	// is never public; everything else needs a session.
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /setup", s.handleSetupForm)
	mux.HandleFunc("POST /setup", s.handleSetup)
	mux.HandleFunc("GET /signed-out", s.handleSignedOut)

	// Single sign-on (sso.go): start, and where User Management sends
	// people back. Both are no-ops (404) while SSO is off.
	mux.HandleFunc("GET /auth/start", s.handleSSOStart)
	mux.HandleFunc("GET /auth/callback", s.handleSSOCallback)

	// Signed-in members.
	auth := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireUser(h)) }
	auth("GET /{$}", s.handleDirectory)
	auth("GET /households/{id}", s.handleFamily)
	auth("GET /people/{id}", s.handlePersonPage)
	auth("GET /photos/{name}", s.handlePhoto)
	auth("POST /people/{id}/notes", s.handleNoteAdd)
	auth("POST /people/{id}/notes/{nid}", s.handleNoteUpdate)
	auth("POST /people/{id}/notes/{nid}/delete", s.handleNoteDelete)
	auth("GET /birthdays", s.handleBirthdays)
	auth("GET /groups", s.handleGroups)
	auth("GET /groups/{id}", s.handleGroupPage)
	auth("GET /print", s.handlePrint)
	auth("GET /print/footer.css", s.handlePrintFooter)
	auth("GET /logo/{name}", s.handleLogo)
	auth("GET /account", s.handleAccount)
	auth("POST /account/password", s.handleAccountPassword)
	auth("GET /me", s.handleMe)
	auth("POST /me/profile", s.handleMeProfile)
	auth("POST /me/household", s.handleMeHousehold)
	auth("POST /me/household/{id}/people", s.handleMeHouseholdPerson)
	auth("GET /me/claim/{code}", s.handleClaimForm)
	auth("POST /me/claim/{code}", s.handleClaim)
	// Heads of household (step 5). Each handler asks access.CanEditPerson or
	// access.CanHeadEditHousehold; there is no password route here on purpose.
	auth("POST /me/heads-may-edit", s.handleMeHeadsMayEdit)
	auth("POST /me/contact-households", s.handleMeContactHousehold)
	auth("POST /me/requests/{id}/accept", s.handleMeRequestAnswer(true))
	auth("POST /me/requests/{id}/decline", s.handleMeRequestAnswer(false))
	auth("GET /me/people/{id}", s.handleMePerson)
	auth("POST /me/people/{id}", s.handleMePersonUpdate)
	auth("POST /me/people/{id}/visibility", s.handleMePersonVisibility)
	auth("POST /me/people/{id}/photo", s.handleMePersonPhoto)
	auth("POST /me/people/{id}/photo/remove", s.handleMePersonPhotoRemove)
	auth("POST /me/people/{id}/login", s.handleMePersonLogin)
	auth("POST /me/people/{id}/notes", s.handleNoteAdd)
	auth("POST /me/people/{id}/notes/{nid}", s.handleNoteUpdate)
	auth("POST /me/people/{id}/notes/{nid}/delete", s.handleNoteDelete)
	auth("GET /me/people/{id}/family", s.handleMeFamily)
	auth("POST /me/people/{id}/relationships", s.handleMeLinkAdd)
	auth("POST /me/people/{id}/relationships/remove", s.handleMeLinkRemove)
	auth("POST /me/people/{id}/own-household", s.handleMeOwnHousehold)
	auth("POST /me/people/{id}/multi-household", s.handleMeMulti)
	auth("POST /me/people/{id}/primary-household", s.handleMePrimary)
	auth("POST /me/people/{id}/second-household", s.handleMeSecondHousehold)
	auth("GET /me/households/{id}", s.handleMeHouseholdPage)
	auth("POST /me/households/{id}", s.handleMeHouseholdUpdate)
	auth("POST /me/households/{id}/photo", s.handleMeHouseholdPhoto)
	auth("POST /me/households/{id}/photo/remove", s.handleMeHouseholdPhotoRemove)
	auth("POST /me/households/{id}/photo-visibility", s.handleMeHouseholdPhotoVisibility)
	auth("POST /me/households/{id}/addresses", s.handleMeAddressAdd)
	auth("POST /me/households/{id}/addresses/{aid}", s.handleMeAddressUpdate)
	auth("POST /me/households/{id}/addresses/{aid}/delete", s.handleMeAddressDelete)
	auth("POST /me/households/{id}/addresses/{aid}/visibility", s.handleMeAddressVisibility)
	auth("POST /me/households/{id}/people", s.handleMeAddPerson)
	auth("POST /me/households/{id}/children", s.handleMeAddChild)
	auth("POST /me/households/{id}/members/{pid}/address", s.handleMeMemberAddress)
	auth("POST /me/households/{id}/members/{pid}/head", s.handleMeMakeHead)
	auth("POST /me/households/{id}/members/{pid}/remove", s.handleMeRemoveMember)
	auth("POST /me/households/{id}/leave", s.handleMeLeave)
	auth("POST /me/households/{id}/family-links", s.handleMeFamilyLinksApply)

	// Staff (editors and admins)
	staff := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireStaff(h)) }
	staff("GET /admin", s.handleAdminHome)
	staff("GET /admin/people", s.handlePeople)
	staff("GET /admin/people/new", s.handlePersonNew)
	staff("POST /admin/people", s.handlePersonCreate)
	staff("GET /admin/people/{id}", s.handlePersonShow)
	staff("POST /admin/people/{id}", s.handlePersonUpdate)
	staff("POST /admin/people/{id}/photo", s.handlePersonPhotoSave)
	staff("POST /admin/people/{id}/photo/remove", s.handlePersonPhotoRemove)
	staff("POST /admin/people/{id}/archive", s.handlePersonArchive)
	staff("POST /admin/people/{id}/visibility", s.handlePersonVisibility)
	staff("GET /admin/people/{id}/delete", s.handlePersonDeleteConfirm)
	staff("POST /admin/people/{id}/delete", s.handlePersonDelete)
	staff("GET /admin/people/{id}/family", s.handlePersonFamily)
	staff("POST /admin/people/{id}/relationships", s.handlePersonLinkAdd)
	staff("POST /admin/people/{id}/relationships/remove", s.handlePersonLinkRemove)
	staff("POST /admin/people/{id}/own-household", s.handlePersonOwnHousehold)
	staff("POST /admin/households/{id}/family-links", s.handleFamilyLinksApply)
	staff("POST /admin/people/{id}/link", s.handlePersonLink)
	staff("POST /admin/people/{id}/unlink", s.handlePersonUnlink)
	staff("POST /admin/people/{id}/login", s.handlePersonLogin)
	staff("POST /admin/people/{id}/heads-may-edit", s.handleAdminHeadsMayEdit)
	staff("POST /admin/people/{id}/multi-household", s.handleAdminMulti)
	staff("POST /admin/people/{id}/primary-household", s.handleAdminPrimary)
	staff("POST /admin/households/{id}/convert-contact", s.handleAdminConvertContact)
	staff("POST /admin/needs-action/requests/{id}/accept", s.handleNARequest(true))
	staff("POST /admin/needs-action/requests/{id}/decline", s.handleNARequest(false))
	staff("GET /admin/needs-action", s.handleNeedsAction)
	staff("POST /admin/needs-action/people/{id}/approve", s.handleNAApprove)
	staff("POST /admin/needs-action/people/{id}/turn-down", s.handleNATurnDown)
	staff("POST /admin/needs-action/people/{id}/same", s.handleNASame)
	staff("POST /admin/needs-action/people/{id}/different", s.handleNADifferent)
	staff("POST /admin/needs-action/households/{id}/approve", s.handleNAHouseholdApprove)
	staff("POST /admin/needs-action/households/{id}/turn-down", s.handleNAHouseholdTurnDown)
	staff("POST /admin/needs-action/renames/{id}/approve", s.handleNARenameApprove)
	staff("POST /admin/needs-action/renames/{id}/turn-down", s.handleNARenameTurnDown)
	staff("POST /admin/needs-action/under13/{id}/suspend", s.handleNAUnder13Suspend)
	staff("POST /admin/needs-action/under13/{id}/done", s.handleNAUnder13Done)
	staff("POST /admin/people/{id}/notes", s.handleNoteAdd)
	staff("POST /admin/people/{id}/notes/{nid}", s.handleNoteUpdate)
	staff("POST /admin/people/{id}/notes/{nid}/delete", s.handleNoteDelete)
	staff("GET /admin/households", s.handleHouseholds)
	staff("GET /admin/households/new", s.handleHouseholdNew)
	staff("POST /admin/households", s.handleHouseholdCreate)
	staff("GET /admin/households/{id}", s.handleHouseholdShow)
	staff("POST /admin/households/{id}", s.handleHouseholdUpdate)
	staff("POST /admin/households/{id}/photo", s.handleHouseholdPhotoSave)
	staff("POST /admin/households/{id}/photo/remove", s.handleHouseholdPhotoRemove)
	staff("POST /admin/households/{id}/archive", s.handleHouseholdArchive)
	staff("GET /admin/households/{id}/delete", s.handleHouseholdDeleteConfirm)
	staff("POST /admin/households/{id}/delete", s.handleHouseholdDelete)
	staff("POST /admin/households/{id}/addresses", s.handleAddressAdd)
	staff("POST /admin/households/{id}/addresses/{aid}", s.handleAddressUpdate)
	staff("POST /admin/households/{id}/addresses/{aid}/delete", s.handleAddressDelete)
	staff("POST /admin/households/{id}/addresses/{aid}/visibility", s.handleAddressVisibility)
	staff("POST /admin/households/{id}/photo-visibility", s.handleHouseholdPhotoVisibility)
	staff("POST /admin/households/{id}/members", s.handleMemberPlace)
	staff("POST /admin/households/{id}/members/{pid}", s.handleMemberPlace)
	staff("POST /admin/households/{id}/members/{pid}/remove", s.handleMemberRemove)
	staff("GET /admin/import", s.handleImportForm)
	staff("POST /admin/import/preview", s.handleImportPreview)
	staff("POST /admin/import/run", s.handleImportRun)
	staff("GET /admin/groups", s.handleAdminGroups)
	staff("GET /admin/groups/new", s.handleAdminGroupNew)
	staff("POST /admin/groups", s.handleAdminGroupCreate)
	staff("GET /admin/groups/{id}", s.handleAdminGroupShow)
	staff("POST /admin/groups/{id}", s.handleAdminGroupUpdate)
	staff("POST /admin/groups/{id}/archive", s.handleAdminGroupArchive)
	staff("POST /admin/groups/{id}/members", s.handleAdminGroupMemberAdd)
	staff("POST /admin/groups/{id}/members/{pid}", s.handleAdminGroupMemberUpdate)
	staff("POST /admin/groups/{id}/members/{pid}/move", s.handleAdminGroupMemberMove)
	staff("POST /admin/groups/{id}/members/{pid}/remove", s.handleAdminGroupMemberRemove)

	// Admins
	admin := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireAdmin(h)) }
	admin("GET /admin/defaults", s.handleDefaults)
	admin("POST /admin/defaults", s.handleDefaultsUpdate)
	admin("GET /admin/users", s.handleUsers)
	admin("GET /admin/users/new", s.handleUserNew)
	admin("POST /admin/users", s.handleUserCreate)
	admin("GET /admin/users/{id}/edit", s.handleUserEdit)
	admin("POST /admin/users/{id}", s.handleUserUpdate)
	admin("POST /admin/users/{id}/delete", s.handleUserDelete)
	admin("GET /admin/settings", s.handleSettings)
	admin("POST /admin/settings", s.handleSettingsUpdate)
	admin("GET /admin/backup", s.handleBackup)
	admin("GET /admin/connections", s.handleConnections)
	// Single sign-on saves as pending and is tested by a real sign-in
	// (handlers_sso_pending.go); save, test and clear on
	// /admin/connections/sso are sent there too.
	admin("POST /admin/connections/sso/signin-test", s.handleSSOTestSignIn)
	admin("POST /admin/connections/sso/discard", s.handleSSODiscard)
	admin("POST /admin/connections/{id}", s.handleConnectionSave)
	admin("POST /admin/connections/{id}/test", s.handleConnectionTest)
	admin("POST /admin/connections/{id}/keep", s.handleConnectionKeep)
	admin("POST /admin/connections/{id}/clear", s.handleConnectionClear)
	admin("GET /admin/tokens", s.handleTokens)
	admin("POST /admin/tokens", s.handleTokenCreate)
	admin("POST /admin/tokens/{id}/delete", s.handleTokenDelete)

	// The API (api.go): app tokens only, no cookies.
	api := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireToken(h)) }
	api("GET /api/v1/people/by-user/{sub}", s.handleAPIByUser)
	api("GET /api/v1/people", s.handleAPIPeople)
	api("GET /api/v1/groups", s.handleAPIGroups)
	api("GET /api/v1/groups/{id}", s.handleAPIGroup)
	api("/api/", s.apiNotFound)

	// Unknown paths: signed-out visitors go to sign in, like everything else.
	mux.Handle("/", s.requireUser(s.notFound))

	pages := s.loadSession(s.csrf(mux))
	return s.logRequests(s.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isCookieFree(r.URL.Path) {
			// The API ignores sessions and CSRF: no session is looked up
			// and no cookie is set.
			mux.ServeHTTP(w, r)
			return
		}
		pages.ServeHTTP(w, r)
	})))
}

// isCookieFree paths ignore sessions and CSRF: the API (step 14).
func isCookieFree(path string) bool {
	return strings.HasPrefix(path, "/api/")
}

// Middleware -------------------------------------------------------------

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		defer func() {
			if err := recover(); err != nil {
				s.log.Error("panic", "path", r.URL.Path, "err", err)
				http.Error(rec, "internal error", http.StatusInternalServerError)
			}
			if !strings.HasPrefix(r.URL.Path, "/static/") {
				s.log.Info("request", "method", r.Method, "path", logPath(r.URL.Path), "status", rec.status,
					"dur", time.Since(start).Round(time.Millisecond), "ip", s.clientIP(r))
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach Flush and write deadlines.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", s.contentSecurityPolicy(r))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if s.isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

const sessionCookie = "md_session"

func (s *Server) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			sess, err := s.store.GetSession(c.Value)
			var user *store.User
			if err == nil {
				// SSO sessions are re-checked with User Management every
				// few minutes; nil means that sign-in has ended.
				user = s.checkGrant(sess)
			}
			if user != nil {
				r = withValue(r, ctxSession, sess)
				r = withValue(r, ctxUser, user)
			} else {
				http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
			}
		}
		next.ServeHTTP(w, r)
	})
}

// csrf implements the double-submit cookie pattern: every visitor gets a
// random token cookie and every POST must echo it in the _csrf field (or
// X-CSRF-Token header). Combined with SameSite=Lax cookies this blocks
// cross-site form posts.
const csrfCookie = "md_csrf"

func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		token := ""
		if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 32 {
			token = c.Value
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete || r.Method == http.MethodPatch {
			sent := r.Header.Get("X-CSRF-Token")
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
				// Photo uploads: cap the body before it is parsed.
				r.Body = http.MaxBytesReader(w, r.Body, uploadMax)
				if err := r.ParseMultipartForm(1 << 20); isTooBig(err) {
					s.render(w, r, http.StatusRequestEntityTooLarge, "error", map[string]any{
						"Title": "Too large", "Message": photoMessage(err),
					})
					return
				}
			}
			if sent == "" {
				sent = r.PostFormValue("_csrf")
			}
			if token == "" || subtle.ConstantTimeCompare([]byte(sent), []byte(token)) != 1 {
				s.render(w, r, http.StatusForbidden, "error", map[string]any{
					"Title":   "Form expired",
					"Message": "Your form session expired. Please go back, refresh the page, and try again.",
				})
				return
			}
		}
		if token == "" {
			token = randomToken()
			http.SetCookie(w, &http.Cookie{
				Name: csrfCookie, Value: token, Path: "/", HttpOnly: true,
				SameSite: http.SameSiteLaxMode, Secure: s.isHTTPS(r), MaxAge: 365 * 24 * 3600,
			})
		}
		next.ServeHTTP(w, withValue(r, ctxCSRF, token))
	})
}

func (s *Server) requireUser(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r) == nil {
			dest := r.URL.RequestURI()
			if r.Method != http.MethodGet {
				dest = "/"
			}
			http.Redirect(w, r, "/login?next="+urlEscape(dest), http.StatusSeeOther)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		next(w, r)
	})
}

// isStaff reports whether the signed-in user is staff (an editor or an
// admin). Nobody signed in is not staff.
func isStaff(u *store.User) bool { return u.IsStaff() }

// requireStaff lets editors and admins through.
func (s *Server) requireStaff(next http.HandlerFunc) http.Handler {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if !isStaff(currentUser(r)) {
			s.render(w, r, http.StatusForbidden, "error", map[string]any{
				"Title": "Staff only", "Message": "You need to be an editor or administrator to do that.",
			})
			return
		}
		next(w, r)
	})
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.Handler {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if !currentUser(r).IsAdmin() {
			s.render(w, r, http.StatusForbidden, "error", map[string]any{
				"Title": "Admins only", "Message": "You need administrator access for that page.",
			})
			return
		}
		next(w, r)
	})
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// logPath keeps one-time claim codes out of the request log.
func logPath(p string) string {
	if strings.HasPrefix(p, "/me/claim/") {
		return "/me/claim/…"
	}
	return p
}
