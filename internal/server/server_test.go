package server

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tylergoza/church-directory/internal/store"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newTestServer(t *testing.T) (*client, *store.Store) {
	t.Helper()
	return newTestServerWith(t, Config{})
}

func newTestServerWith(t *testing.T, cfg Config) (*client, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return startServer(t, cfg, st), st
}

// startServer runs a server on an existing store. Tests never read the
// real environment.
func startServer(t *testing.T, cfg Config, st *store.Store) *client {
	t.Helper()
	if cfg.Env == nil {
		cfg.Env = func(string) string { return "" }
	}
	srv, err := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: ts.URL, http: &http.Client{Jar: jar}}
}

func (c *client) get(path string, wantStatus int) string {
	c.t.Helper()
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		c.t.Fatalf("GET %s: status %d, want %d\n%s", path, resp.StatusCode, wantStatus, body)
	}
	return string(body)
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// post submits a form, pulling the CSRF token from a page first.
func (c *client) post(tokenPage, path string, form url.Values, wantStatus int) string {
	c.t.Helper()
	m := csrfRe.FindStringSubmatch(c.get(tokenPage, 200))
	if m == nil {
		c.t.Fatalf("no csrf token on %s", tokenPage)
	}
	form.Set("_csrf", m[1])
	resp, err := c.http.PostForm(c.base+path, form)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		c.t.Fatalf("POST %s: status %d, want %d\n%s", path, resp.StatusCode, wantStatus, body)
	}
	return string(body)
}

// as signs in as someone else in a browser of their own.
func (c *client) as(username string) *client {
	jar, _ := cookiejar.New(nil)
	other := &client{t: c.t, base: c.base, http: &http.Client{Jar: jar}}
	other.post("/login", "/login", url.Values{"username": {username}, "password": {"a long password"}}, 200)
	return other
}

func TestSetupLoginLogout(t *testing.T) {
	c, st := newTestServer(t)

	// No users yet: sign in leads to setup.
	if body := c.get("/login", 200); !strings.Contains(body, "Create the first administrator") {
		t.Fatal("expected setup page")
	}
	if body := c.post("/setup", "/setup", url.Values{
		"site_name": {"Grace Directory"}, "username": {"pat"}, "password": {"short"}, "password_confirm": {"short"},
	}, 422); !strings.Contains(body, "at least 10 characters") {
		t.Error("expected a password error")
	}
	body := c.post("/setup", "/setup", url.Values{
		"site_name": {"Grace Directory"}, "username": {"pat"}, "display_name": {"Pat"},
		"password": {"a long password"}, "password_confirm": {"a long password"},
	}, 200)
	if !strings.Contains(body, "Grace Directory") || !strings.Contains(body, "administrator account is ready") {
		t.Error("expected to land signed in with the new site name")
	}
	if u, _ := st.GetUser(1); !u.IsAdmin() {
		t.Error("the first user should be an admin")
	}
	// Setup is closed once someone exists.
	c.get("/setup", 200) // redirected to /login, which redirects home when signed in
	if !strings.Contains(c.get("/admin/settings", 200), "Grace Directory") {
		t.Error("settings should show the site name")
	}

	c.post("/", "/logout", url.Values{}, 200)
	if strings.Contains(c.get("/", 200), "Sign out") {
		t.Error("still signed in after signing out")
	}
	if body := c.post("/login", "/login", url.Values{"username": {"pat"}, "password": {"wrong password"}}, 401); !strings.Contains(body, "Incorrect") {
		t.Error("expected a sign-in error")
	}
	c.as("pat")
}

func TestRolesGateAdminPages(t *testing.T) {
	c, st := newTestServer(t)
	st.CreateUser("boss", "", "a long password", store.RoleAdmin)
	st.CreateUser("sam", "", "a long password", store.RoleEditor)
	st.CreateUser("mel", "", "a long password", store.RoleMember)

	admin := []string{"/admin/users", "/admin/users/new", "/admin/settings", "/admin/connections", "/admin/tokens", "/admin/backup"}
	// Staff (editor or admin) get /admin; members do not.
	c.as("sam").get("/admin", 200)
	c.as("mel").get("/admin", 403)
	for _, who := range []string{"sam", "mel"} {
		u := c.as(who)
		for _, p := range admin {
			u.get(p, 403)
		}
		u.post("/account", "/admin/connections/events", url.Values{"events_url": {"https://x.example"}}, 403)
		u.post("/account", "/admin/tokens", url.Values{"name": {"x"}}, 403)
		if strings.Contains(u.get("/", 200), "/admin/connections") {
			t.Errorf("%s sees the Connections link", who)
		}
	}
	boss := c.as("boss")
	for _, p := range admin {
		boss.get(p, 200)
	}
	// Signed out: admin pages go to sign in.
	if body := c.get("/admin/connections", 200); !strings.Contains(body, `action="/login"`) {
		t.Error("expected the sign-in form")
	}
}

// The directory is never public: apart from signing in, first-run setup
// and static files, every page needs a session.
func TestNothingIsPublic(t *testing.T) {
	c, st := newTestServer(t)
	st.CreateUser("mel", "", "a long password", store.RoleMember)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/", "/account", "/admin", "/admin/users", "/admin/settings", "/admin/backup", "/admin/tokens", "/people/1", "/photos/x.jpg", "/nope"} {
		resp, err := noRedirect.Get(c.base + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
			t.Errorf("signed out GET %s: status %d, location %q; want a redirect to /login", p, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	// The API answers a signed-out visitor 401 in JSON, never a login page.
	for _, p := range []string{"/api/v1/people", "/api/v1/groups", "/api/nope"} {
		if body := c.get(p, 401); !strings.Contains(body, `"code"`) {
			t.Errorf("GET %s: want a JSON error, got %s", p, body)
		}
	}
	// What visitors may open.
	c.get("/login", 200)
	c.get("/manifest.webmanifest", 200)
	c.get("/sw.js", 200)
	c.get("/static/js/application.js", 200)
	// Signed in, the directory and the account page open.
	mel := c.as("mel")
	if body := mel.get("/", 200); !strings.Contains(body, "Search by name") {
		t.Error("expected the directory page")
	}
	mel.get("/account", 200)
	mel.get("/nope", 404)
}

// The service worker caches static files only: never pages, photos or the
// API, and it drops anything it cached under another name.
func TestServiceWorkerCachesStaticOnly(t *testing.T) {
	c, _ := newTestServer(t)
	var code []string // the worker without its comments
	for _, line := range strings.Split(c.get("/sw.js", 200), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			code = append(code, line)
		}
	}
	sw := strings.Join(code, "\n")
	if strings.Contains(sw, "/photos") || strings.Contains(sw, "/api/") {
		// Those paths are never mentioned: they fall through to the network.
		t.Error("service worker should not special-case photos or the API; they must never be cached")
	}
	// The only cache.put is in cacheFirst, which only /static/ reaches.
	if n := strings.Count(sw, "cache.put("); n != 1 {
		t.Errorf("service worker has %d cache.put calls, want 1 (static files)", n)
	}
	if !strings.Contains(sw, `url.pathname.startsWith("/static/")`) || !strings.Contains(sw, "cacheFirst(request)") {
		t.Error("service worker should cache /static/ files only")
	}
	if strings.Contains(sw, "networkFirstPage") || strings.Contains(sw, "cache.addAll([") {
		t.Error("service worker should not keep pages")
	}
	// The precache list is /static/ files plus the generic offline page.
	for _, line := range strings.Split(sw, "\n") {
		if strings.HasPrefix(line, "const PRECACHE = ") {
			for _, u := range regexp.MustCompile(`"(/[^"]*)"`).FindAllStringSubmatch(line, -1) {
				if u[1] != "/offline" && !strings.HasPrefix(u[1], "/static/") {
					t.Errorf("precache holds %q, want only /static/ files and /offline", u[1])
				}
			}
		}
	}
}

func TestSecurityHeadersAndPWA(t *testing.T) {
	c, _ := newTestServer(t)
	resp, err := c.http.Get(c.base + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "img-src 'self' blob:") {
		t.Errorf("CSP = %q", csp)
	}
	for _, ck := range resp.Cookies() {
		if !strings.HasPrefix(ck.Name, "md_") {
			t.Errorf("cookie %q should start md_", ck.Name)
		}
	}
	if body := c.get("/manifest.webmanifest", 200); !strings.Contains(body, `"start_url":"/"`) {
		t.Error("manifest")
	}
	if body := c.get("/sw.js", 200); !strings.Contains(body, "md-static-") || strings.Contains(body, "__VERSION__") {
		t.Error("service worker")
	}
	c.get("/offline", 200)
	if c.get("/healthz", 200) != "ok" {
		t.Error("healthz")
	}
	c.get("/static/js/application.js", 200)
}

// No inline style attributes: the CSP is style-src 'self'.
func TestNoInlineStyles(t *testing.T) {
	err := filepath.WalkDir("../../web/templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := os.ReadFile(p)
		if strings.Contains(string(b), "style=") {
			t.Errorf("%s has an inline style attribute", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
