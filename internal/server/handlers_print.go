package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tylergoza/church-directory/internal/photos"
)

// Printing (step 10). /print builds the directory as one page for the
// browser to print or save as a PDF. It is built from the access view
// models, so it shows what the person printing could see on screen, and
// it never calls the notes code.

const (
	printEveryone = "everyone"
	printStaff    = "staff"
)

// mayPrint reports whether the signed-in user may print, by the
// "Who may print" setting.
func (s *Server) mayPrint(r *http.Request) bool {
	u := currentUser(r)
	if u == nil {
		return false
	}
	if s.store.Setting("print_who", printEveryone) == printStaff {
		return isStaff(u)
	}
	return true
}

// ChurchName is the name on the cover and in the footer.
func (s *Server) churchName() string {
	if n := s.store.Setting("church_name", ""); n != "" {
		return n
	}
	return s.SiteName()
}

type printOptions struct {
	Group   int64
	Photos  bool
	Gutter  bool
	Paper   string // "letter" or "half"
	Class   string // the named page the print CSS uses
	Booklet bool
}

func printOpts(r *http.Request) printOptions {
	q := r.URL.Query()
	o := printOptions{Group: queryInt(r, "group"), Photos: q.Get("photos") != "off",
		Gutter: q.Get("gutter") == "on", Paper: "letter"}
	if q.Get("paper") == "half" {
		o.Paper, o.Booklet = "half", true
	}
	o.Class = "p-" + o.Paper
	if o.Gutter {
		o.Class += "-g"
	}
	return o
}

func (s *Server) handlePrint(w http.ResponseWriter, r *http.Request) {
	if !s.mayPrint(r) {
		s.render(w, r, http.StatusForbidden, "error", map[string]any{
			"Title": "Printing is limited", "Message": "Only staff may print the directory.",
		})
		return
	}
	d, err := s.directory(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	opts := printOpts(r)
	book, ok := d.Print(opts.Group)
	if !ok {
		s.notFound(w, r)
		return
	}
	logo := s.store.Setting("logo", "")
	if !photos.ValidName(logo) {
		logo = ""
	}
	s.render(w, r, http.StatusOK, "print/index", map[string]any{
		"Title": "Print", "PrintPage": true, "Opts": opts, "Book": book, "Groups": d.Groups(),
		"Church": s.churchName(), "Logo": logo, "AsOf": time.Now().Format("January 2, 2006"),
	})
}

// cssString quotes s as a CSS string.
func cssString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\' || r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\%x ", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// handlePrintFooter serves the page footers as CSS. The footer holds the
// church name, which a static file can't; it is a same-origin stylesheet,
// so the CSP (style-src 'self') allows it.
func (s *Server) handlePrintFooter(w http.ResponseWriter, r *http.Request) {
	if !s.mayPrint(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	text := cssString("For members of " + s.churchName() + ". Please don't share.")
	var b strings.Builder
	for _, name := range []string{"letter", "letter-g", "half", "half-g"} {
		fmt.Fprintf(&b, "@page %s {\n  @bottom-left { content: %s; font: 8pt sans-serif; color: #444; }\n  @bottom-right { content: counter(page); font: 8pt sans-serif; color: #444; }\n}\n", name, text)
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write([]byte(b.String()))
}

// handleLogo serves the church logo to signed-in users.
func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !photos.ValidName(name) || name != s.store.Setting("logo", "") {
		http.NotFound(w, r)
		return
	}
	s.servePhotoFile(w, r, name, false)
}

// saveLogo handles the logo part of the settings form: a new file, or the
// remove tick. msg is for the admin when the picture was refused.
func (s *Server) saveLogo(r *http.Request) (msg string, err error) {
	old := s.store.Setting("logo", "")
	if r.PostFormValue("remove_logo") != "" {
		if err := s.store.SetSetting("logo", ""); err != nil {
			return "", err
		}
		s.removePhoto(old)
		return "", nil
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File[photoField]) == 0 {
		return "", nil // nothing chosen
	}
	full, thumb, msg, err := s.readUpload(r)
	if msg != "" || err != nil {
		return msg, err
	}
	name, err := s.cfg.Photos.Save(full, thumb)
	if err != nil {
		return "", err
	}
	if err := s.store.SetSetting("logo", name); err != nil {
		s.cfg.Photos.Remove(name)
		return "", err
	}
	s.removePhoto(old)
	return "", nil
}
