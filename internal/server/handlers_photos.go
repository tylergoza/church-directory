package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/tylergoza/church-directory/internal/access"
	"github.com/tylergoza/church-directory/internal/photos"
	"github.com/tylergoza/church-directory/internal/store"
)

// uploadMax is the most a photo upload request may hold: the picture plus
// room for the multipart framing and the other form fields.
const uploadMax = photos.MaxUpload + 1<<20

// photoField is the name of the file input on every photo form.
const photoField = "photo"

// photoMessage turns a refused upload into words for the person who picked it.
func photoMessage(err error) string {
	switch {
	case errors.Is(err, photos.ErrTooLarge), isTooBig(err):
		return "That picture is too large. Photos can be up to 5 MB."
	case errors.Is(err, photos.ErrFormat):
		return "That file isn't a JPEG or PNG picture. Try another one."
	case errors.Is(err, photos.ErrDimensions):
		return "That picture is too many pixels wide or tall. Try a smaller one."
	}
	return ""
}

func isTooBig(err error) bool {
	var mb *http.MaxBytesError
	return errors.As(err, &mb)
}

// readUpload takes the picture out of a multipart request and makes the
// stored versions. A problem with the picture itself comes back as msg
// (fit to show the user); err is only for real failures.
func (s *Server) readUpload(r *http.Request) (full, thumb []byte, msg string, err error) {
	if s.cfg.Photos.Path == "" {
		return nil, nil, "", errors.New("photos folder is not set up")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, uploadMax)
	f, _, err := r.FormFile(photoField)
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, nil, "Choose a picture first.", nil
		}
		if m := photoMessage(err); m != "" {
			return nil, nil, m, nil
		}
		return nil, nil, "That upload didn't arrive. Please try again.", nil
	}
	defer f.Close()
	full, thumb, err = photos.Process(f)
	if err != nil {
		if m := photoMessage(err); m != "" {
			return nil, nil, m, nil
		}
		return nil, nil, "", err
	}
	return full, thumb, "", nil
}

// savePersonPhoto stores the multipart upload in r as the person's photo,
// under a new random name, and removes the old files. The caller has
// already checked that the user may change this photo. msg is a message
// for the user when the picture was refused (nothing changed); err is an
// internal failure.
func (s *Server) savePersonPhoto(r *http.Request, personID, by int64) (msg string, err error) {
	if _, err := s.store.GetPerson(personID); err != nil {
		return "", err
	}
	full, thumb, msg, err := s.readUpload(r)
	if msg != "" || err != nil {
		return msg, err
	}
	name, err := s.cfg.Photos.Save(full, thumb)
	if err != nil {
		return "", err
	}
	old, err := s.store.SetPersonPhoto(personID, name, by)
	if err != nil {
		s.cfg.Photos.Remove(name)
		return "", err
	}
	s.removePhoto(old)
	return "", nil
}

// saveHouseholdPhoto is savePersonPhoto for a household.
func (s *Server) saveHouseholdPhoto(r *http.Request, householdID, by int64) (msg string, err error) {
	if _, err := s.store.GetHousehold(householdID); err != nil {
		return "", err
	}
	full, thumb, msg, err := s.readUpload(r)
	if msg != "" || err != nil {
		return msg, err
	}
	name, err := s.cfg.Photos.Save(full, thumb)
	if err != nil {
		return "", err
	}
	old, err := s.store.SetHouseholdPhoto(householdID, name, by)
	if err != nil {
		s.cfg.Photos.Remove(name)
		return "", err
	}
	s.removePhoto(old)
	return "", nil
}

// removePersonPhoto and removeHouseholdPhoto clear the photo and delete its files.
func (s *Server) removePersonPhoto(personID, by int64) error {
	old, err := s.store.SetPersonPhoto(personID, "", by)
	s.removePhoto(old)
	return err
}

func (s *Server) removeHouseholdPhoto(householdID, by int64) error {
	old, err := s.store.SetHouseholdPhoto(householdID, "", by)
	s.removePhoto(old)
	return err
}

// Staff pages ------------------------------------------------------------

func (s *Server) handlePersonPhotoSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	back := "/admin/people/" + itoa(id)
	msg, err := s.savePersonPhoto(r, id, currentUser(r).ID)
	s.photoResult(w, r, back, "Photo saved.", msg, err)
}

func (s *Server) handlePersonPhotoRemove(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.removePersonPhoto(id, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/people/"+itoa(id), "Photo removed.")
}

func (s *Server) handleHouseholdPhotoSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	back := "/admin/households/" + itoa(id)
	msg, err := s.saveHouseholdPhoto(r, id, currentUser(r).ID)
	s.photoResult(w, r, back, "Photo saved.", msg, err)
}

func (s *Server) handleHouseholdPhotoRemove(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.removeHouseholdPhoto(id, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/households/"+itoa(id), "Photo removed.")
}

// photoResult finishes an upload request: back to the page with a message.
func (s *Server) photoResult(w http.ResponseWriter, r *http.Request, back, done, msg string, err error) {
	switch {
	case err != nil:
		s.serverError(w, r, err)
	case msg != "":
		s.redirectError(w, r, back, msg)
	default:
		s.redirect(w, r, back, done)
	}
}

// Serving ----------------------------------------------------------------

// photoVisible says whether the directory's viewer may see the photo
// that belongs to a person or a household (the other ID is 0). It is the
// one check behind /photos/{name}; signed photo URLs call it too.
func photoVisible(d *access.Directory, name string, personID, householdID int64) bool {
	if personID != 0 {
		a, ok := d.Person(personID)
		return ok && a.Person.Photo == name && d.CanSeePerson(a) && access.Visible(d.Viewer, a, store.FieldPhoto)
	}
	h, ok := d.Household(householdID)
	return ok && h.Household.Photo == name && d.CanSeeHousehold(h) && access.VisibleHouseholdPhoto(d.Viewer, h)
}

// mayViewPhoto looks up whose photo name is and applies photoVisible for
// the viewer. Unknown names, invalid names and hidden photos all give false.
func (s *Server) mayViewPhoto(v *access.Viewer, name string) (bool, error) {
	if !photos.ValidName(name) {
		return false, nil
	}
	pid, hid, err := s.store.PhotoOwner(name)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	d, err := access.Load(s.store, v)
	if err != nil {
		return false, err
	}
	return photoVisible(d, name, pid, hid), nil
}

// handlePhoto serves /photos/{name}, or its 300 px thumbnail with
// ?size=thumb. A photo the viewer may not see is a plain 404.
func (s *Server) handlePhoto(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	v, err := s.viewer(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	ok, err := s.mayViewPhoto(v, name)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.servePhotoFile(w, r, name, r.URL.Query().Get("size") == "thumb")
}

// servePhotoFile writes the file with the headers photos always carry.
// Only call it after the visibility check.
func (s *Server) servePhotoFile(w http.ResponseWriter, r *http.Request, name string, thumb bool) {
	f, err := s.cfg.Photos.Open(name, thumb)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", fi.ModTime().Truncate(time.Second), f)
}

// photoURL and thumbURL are the template funcs for a photo's address.
func photoURL(name string) string {
	if name == "" {
		return ""
	}
	return "/photos/" + name
}

func thumbURL(name string) string {
	if name == "" {
		return ""
	}
	return "/photos/" + name + "?size=thumb"
}
