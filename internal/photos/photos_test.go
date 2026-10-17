package photos

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const marker = "GPSLatitude-MARKER"

func solid(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func jpegBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withEXIF splices an APP1 Exif segment holding the marker after SOI.
func withEXIF(t *testing.T, j []byte) []byte {
	t.Helper()
	payload := append([]byte("Exif\x00\x00"), marker...)
	n := len(payload) + 2
	out := append([]byte{}, j[:2]...)
	out = append(out, 0xFF, 0xE1, byte(n>>8), byte(n))
	out = append(out, payload...)
	return append(out, j[2:]...)
}

// hasAPP1 walks the JPEG segments up to the scan looking for APP1.
func hasAPP1(t *testing.T, j []byte) bool {
	t.Helper()
	i := 2
	for i+4 <= len(j) && j[i] == 0xFF {
		m := j[i+1]
		if m == 0xE1 {
			return true
		}
		if m == 0xDA {
			return false
		}
		i += 2 + int(j[i+2])<<8 + int(j[i+3])
	}
	return false
}

func size(t *testing.T, b []byte) (int, int) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || format != "jpeg" {
		t.Fatalf("output not a JPEG: %v %q", err, format)
	}
	return cfg.Width, cfg.Height
}

func TestProcessStripsEXIF(t *testing.T) {
	in := withEXIF(t, jpegBytes(t, solid(64, 48, color.RGBA{200, 10, 10, 255})))
	if !bytes.Contains(in, []byte(marker)) || !hasAPP1(t, in) {
		t.Fatal("fixture should contain the marker")
	}
	full, thumb, err := Process(bytes.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string][]byte{"full": full, "thumb": thumb} {
		if bytes.Contains(out, []byte(marker)) || bytes.Contains(out, []byte("Exif")) {
			t.Errorf("%s still has EXIF data", name)
		}
		if hasAPP1(t, out) {
			t.Errorf("%s has an APP1 segment", name)
		}
	}
}

func TestProcessRefusals(t *testing.T) {
	var g bytes.Buffer
	if err := gif.Encode(&g, solid(4, 4, color.White), nil); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		in   []byte
		want error
	}{
		{"too large", bytes.Repeat([]byte{0}, MaxUpload+1), ErrTooLarge},
		{"too wide", pngBytes(t, image.NewGray(image.Rect(0, 0, MaxSide+1, 10))), ErrDimensions},
		{"too tall", pngBytes(t, image.NewGray(image.Rect(0, 0, 10, MaxSide+1))), ErrDimensions},
		{"gif", g.Bytes(), ErrFormat},
		{"garbage", []byte("definitely not a picture"), ErrFormat},
		{"empty", nil, ErrFormat},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Process(bytes.NewReader(tc.in))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestProcessSizes(t *testing.T) {
	tests := []struct {
		name   string
		w, h   int
		fw, fh int
		tw, th int
	}{
		{"landscape", 3000, 1500, 1200, 600, 300, 150},
		{"portrait", 1500, 3000, 600, 1200, 150, 300},
		{"not upscaled", 500, 400, 500, 400, 300, 240},
		{"tiny", 20, 10, 20, 10, 20, 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			full, thumb, err := Process(bytes.NewReader(jpegBytes(t, solid(tc.w, tc.h, color.Gray{128}))))
			if err != nil {
				t.Fatal(err)
			}
			if w, h := size(t, full); w != tc.fw || h != tc.fh {
				t.Errorf("full %dx%d, want %dx%d", w, h, tc.fw, tc.fh)
			}
			if w, h := size(t, thumb); w != tc.tw || h != tc.th {
				t.Errorf("thumb %dx%d, want %dx%d", w, h, tc.tw, tc.th)
			}
		})
	}
}

func TestProcessPNGTransparencyBecomesWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10)) // fully transparent
	full, _, err := Process(bytes.NewReader(pngBytes(t, img)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := jpeg.Decode(bytes.NewReader(full))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := out.At(5, 5).RGBA()
	if r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Errorf("transparent pixel came out %d,%d,%d, want white", r>>8, g>>8, b>>8)
	}
}

func TestResize(t *testing.T) {
	red, blue := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}
	halves := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if x < 2 {
				halves.Set(x, y, red)
			} else {
				halves.Set(x, y, blue)
			}
		}
	}
	got := resize(halves, 2)
	if b := got.Bounds(); b.Dx() != 2 || b.Dy() != 2 {
		t.Fatalf("size %v", b)
	}
	for y := 0; y < 2; y++ {
		for x, want := range []color.RGBA{red, blue} {
			if c := got.At(x, y); c != want {
				t.Errorf("(%d,%d) = %v, want %v", x, y, c, want)
			}
		}
	}

	// A uniform colour stays uniform even with a fractional ratio.
	u := color.RGBA{12, 200, 99, 255}
	got = resize(solid(7, 5, u), 3)
	if b := got.Bounds(); b.Dx() != 3 || b.Dy() != 2 {
		t.Fatalf("size %v", b)
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			if c := got.At(x, y); c != u {
				t.Errorf("(%d,%d) = %v, want %v", x, y, c, u)
			}
		}
	}

	// Fractional coverage: 3 px (black, black, white) to 2 px.
	// Dest 0 covers 1.5 source px: (1+0.5 black) -> 0; dest 1 is half
	// black, 1 white: 255*1/1.5 = 170.
	three := image.NewRGBA(image.Rect(0, 0, 3, 1))
	for x := 0; x < 3; x++ {
		three.Set(x, 0, color.RGBA{0, 0, 0, 255})
	}
	three.Set(2, 0, color.RGBA{255, 255, 255, 255})
	got = resize(three, 2)
	if c := got.At(0, 0).(color.RGBA); c.R != 0 {
		t.Errorf("left = %v, want black", c)
	}
	if c := got.At(1, 0).(color.RGBA); c.R != 170 {
		t.Errorf("right = %v, want R=170", c)
	}

	// No upscale.
	if got := resize(solid(10, 10, u), 100); got.Bounds().Dx() != 10 {
		t.Errorf("upscaled to %v", got.Bounds())
	}
}

func TestValidName(t *testing.T) {
	good := strings.Repeat("ab12", 8)
	tests := []struct {
		name string
		want bool
	}{
		{good, true},
		{"../x", false},
		{"../" + good[3:], false},
		{strings.ToUpper(good), false},
		{good[:31], false},
		{good + "0", false},
		{strings.Repeat("g", 32), false},
		{good[:28] + ".jpg", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := ValidName(tc.name); got != tc.want {
			t.Errorf("ValidName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDir(t *testing.T) {
	d := Dir{Path: filepath.Join(t.TempDir(), "photos")}
	a, err := d.Save([]byte("full-a"), []byte("thumb-a"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Save([]byte("full-b"), []byte("thumb-b"))
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !ValidName(a) || !ValidName(b) {
		t.Fatalf("names %q %q", a, b)
	}
	for _, p := range []string{a + ".jpg", a + "_t.jpg"} {
		st, err := os.Stat(filepath.Join(d.Path, p))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o640 {
			t.Errorf("%s mode %v", p, st.Mode().Perm())
		}
	}
	f, err := d.Open(a, true)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(f)
	f.Close()
	if buf.String() != "thumb-a" {
		t.Errorf("thumb contents %q", buf.String())
	}
	if _, err := d.Open("../"+a[3:], false); err == nil {
		t.Error("Open accepted a bad name")
	}

	d.Remove(a)
	d.Remove(a) // already gone: no problem
	for _, p := range []string{a + ".jpg", a + "_t.jpg"} {
		if _, err := os.Stat(filepath.Join(d.Path, p)); !os.IsNotExist(err) {
			t.Errorf("%s still there (%v)", p, err)
		}
	}
	if _, err := d.Open(b, false); err != nil {
		t.Errorf("other photo was removed: %v", err)
	}
}
