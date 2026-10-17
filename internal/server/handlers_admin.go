package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Settings & backup ------------------------------------------------------

// handleAdminHome is the staff landing page. Later steps add the people,
// households, groups and Needs action links to it.
func (s *Server) handleAdminHome(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "admin", map[string]any{"Title": "Admin"})
}

func (s *Server) settingsData(extra map[string]any) map[string]any {
	m := map[string]any{"Title": "Settings", "SiteNameValue": s.SiteName(), "SelfSignup": s.selfSignupOn(),
		"ChurchValue": s.store.Setting("church_name", ""), "Logo": s.store.Setting("logo", ""),
		"PrintWho": s.store.Setting("print_who", printEveryone)}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "settings", s.settingsData(nil))
}

func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	name := formStr(r, "site_name")
	selfSignup := ""
	if r.PostFormValue("self_signup") == "1" {
		selfSignup = "1"
	}
	who := printEveryone
	if formStr(r, "print_who") == printStaff {
		who = printStaff
	}
	again := func(msg string) {
		s.render(w, r, http.StatusUnprocessableEntity, "settings", s.settingsData(map[string]any{
			"Errors": []string{msg}, "SiteNameValue": name, "SelfSignup": selfSignup == "1",
			"ChurchValue": formStr(r, "church_name"), "PrintWho": who,
		}))
	}
	if name == "" {
		again("Site name is required.")
		return
	}
	if msg, err := s.saveLogo(r); err != nil {
		s.serverError(w, r, err)
		return
	} else if msg != "" {
		again(msg)
		return
	}
	for k, v := range map[string]string{"site_name": name, "church_name": formStr(r, "church_name"), "print_who": who, "self_signup": selfSignup} {
		if err := s.store.SetSetting(k, v); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.reloadSettings()
	s.redirect(w, r, "/admin/settings", "Settings saved.")
}

// handleBackup streams a consistent snapshot of the database. Restoring is
// just replacing the .db file on the target host while the app is stopped.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	dir, err := os.MkdirTemp("", "md-backup-")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer os.RemoveAll(dir)
	dest := filepath.Join(dir, "backup.db")
	if err := s.store.Backup(r.Context(), dest); err != nil {
		s.serverError(w, r, err)
		return
	}
	f, err := os.Open(dest)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer f.Close()
	name := "directory-" + time.Now().Format("2006-01-02-1504") + ".db"
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	io.Copy(w, f)
}
