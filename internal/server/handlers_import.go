package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tylergoza/church-directory/internal/store"
)

// CSV import for staff (step 13). The preview saves nothing and keeps no
// file: the CSV text rides along in a hidden field and every step (preview,
// refresh, import) parses it again and rebuilds the plan from it plus
// staff's choices. No signature is needed because the field holds nothing
// staff couldn't have typed in themselves; the server checks every row and
// every choice again on Import, and Import is one transaction.

type importRowView struct {
	Row         *store.ImportRow
	Value       string // the chosen action, as a form value
	FileMatches []*store.ImportRow
	Options     []importOption
}

type importOption struct {
	Value, Label string
	Selected     bool
}

func (s *Server) handleImportForm(w http.ResponseWriter, r *http.Request) {
	s.renderImportForm(w, r, http.StatusOK, nil)
}

func (s *Server) renderImportForm(w http.ResponseWriter, r *http.Request, status int, errs []string) {
	s.render(w, r, status, "import/index", map[string]any{
		"Title": "Import people", "Errors": errs, "Fields": store.ImportFields, "MaxMB": store.ImportMaxBytes >> 20,
	})
}

// importCSV is the CSV text of this request: the uploaded file, or the
// hidden field a preview carried.
func importCSV(r *http.Request) (string, error) {
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		var b strings.Builder
		buf := make([]byte, 32<<10)
		for {
			n, err := f.Read(buf)
			b.Write(buf[:n])
			if b.Len() > store.ImportMaxBytes {
				return "", fmt.Errorf("That file is too large. The most is %d MB.", store.ImportMaxBytes>>20)
			}
			if err != nil {
				break
			}
		}
		return b.String(), nil
	}
	if v := r.PostFormValue("csv"); v != "" {
		return v, nil
	}
	return "", errors.New("Choose a CSV file to upload.")
}

// importState parses the request's CSV, matches it and reads staff's
// choices (defaults when none were sent).
func (s *Server) importState(r *http.Request) (*store.ImportFile, string, []store.ImportChoice, error) {
	text, err := importCSV(r)
	if err != nil {
		return nil, "", nil, err
	}
	f, err := store.ParseImport(strings.NewReader(text))
	if err != nil {
		return nil, "", nil, err
	}
	if err := s.store.MatchImport(f); err != nil {
		return nil, "", nil, serverErr{err}
	}
	choices := make([]store.ImportChoice, len(f.Rows))
	for i, row := range f.Rows {
		if v := r.PostFormValue(fmt.Sprintf("choice_%d", i)); v != "" {
			choices[i] = store.ParseImportChoice(v)
		} else {
			choices[i] = store.DefaultImportChoice(row)
		}
	}
	return f, text, choices, nil
}

// serverErr marks an error that is ours, not the file's.
type serverErr struct{ error }

func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	f, text, choices, err := s.importState(r)
	if err != nil {
		var se serverErr
		if errors.As(err, &se) {
			s.serverError(w, r, se.error)
			return
		}
		s.renderImportForm(w, r, http.StatusUnprocessableEntity, []string{err.Error()})
		return
	}
	s.renderImportPreview(w, r, http.StatusOK, f, text, choices, nil)
}

func (s *Server) renderImportPreview(w http.ResponseWriter, r *http.Request, status int, f *store.ImportFile, text string, choices []store.ImportChoice, errs []string) {
	plan := store.BuildPlan(f, choices)
	rows := make([]importRowView, len(f.Rows))
	for i, row := range f.Rows {
		v := importRowView{Row: row, Value: choices[i].Value()}
		v.Options = []importOption{{"add", "Add as new person", false}, {"skip", "Skip", false}}
		for _, m := range row.ExistingMatches {
			v.Options = append(v.Options, importOption{Value: store.ImportChoice{Action: store.ImportExisting, PersonID: m.ID}.Value(), Label: "Use " + m.Name() + " already here"})
		}
		for k := range v.Options {
			v.Options[k].Selected = v.Options[k].Value == v.Value
		}
		for _, j := range row.FileMatches {
			v.FileMatches = append(v.FileMatches, f.Rows[j])
		}
		rows[i] = v
	}
	s.render(w, r, status, "import/preview", map[string]any{
		"Title": "Preview import", "File": f, "CSV": text, "Rows": rows, "Plan": plan, "Errors": errs,
	})
}

func (s *Server) handleImportRun(w http.ResponseWriter, r *http.Request) {
	f, text, choices, err := s.importState(r)
	if err != nil {
		var se serverErr
		if errors.As(err, &se) {
			s.serverError(w, r, se.error)
			return
		}
		s.renderImportForm(w, r, http.StatusUnprocessableEntity, []string{err.Error()})
		return
	}
	plan := store.BuildPlan(f, choices)
	if !plan.OK() {
		s.renderImportPreview(w, r, http.StatusUnprocessableEntity, f, text, choices,
			append([]string{"Nothing was imported. Fix these rows or choose Skip for them:"}, plan.Problems...))
		return
	}
	res, err := s.store.ApplyImport(plan, currentUser(r).ID)
	if err != nil {
		s.log.Error("import", "err", err)
		s.renderImportPreview(w, r, http.StatusInternalServerError, f, text, choices,
			[]string{"The import failed and nothing was saved. Try again, and tell the admin if it keeps happening."})
		return
	}
	s.redirect(w, r, "/admin/people", fmt.Sprintf("Imported %d new people, linked %d already here, made %d households with %d addresses.",
		res.People, res.Linked, res.Households, res.Addresses))
}
