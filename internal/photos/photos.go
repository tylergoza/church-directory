// Package photos validates, shrinks and stores uploaded pictures.
//
// Every upload is decoded and re-encoded as a JPEG, which drops all
// metadata (EXIF, GPS location and so on). Standard library only.
package photos

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder
	"io"
	"os"
	"path/filepath"
)

const (
	MaxUpload = 5 << 20 // bytes
	MaxSide   = 6000    // pixels, either side, as uploaded
	Full      = 1200    // longest side of the stored photo
	Thumb     = 300     // longest side of the thumbnail

	jpegQuality = 85
	nameBytes   = 16 // 128 bits
	nameLen     = nameBytes * 2
)

var (
	ErrTooLarge   = errors.New("photos: upload is larger than 5 MB")
	ErrFormat     = errors.New("photos: not a JPEG or PNG picture")
	ErrDimensions = errors.New("photos: picture is too many pixels wide or tall")
)

// Process reads an upload and returns the 1200 px photo and 300 px
// thumbnail, both as JPEG with no metadata.
func Process(r io.Reader) (full, thumb []byte, err error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxUpload+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > MaxUpload {
		return nil, nil, ErrTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, nil, ErrFormat
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxSide || cfg.Height > MaxSide {
		return nil, nil, ErrDimensions
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, ErrFormat
	}
	// Transparent PNG areas become white (JPEG has no alpha).
	b := src.Bounds()
	flat := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), src, b.Min, draw.Over)

	fullImg := resize(flat, Full)
	if full, err = encode(fullImg); err != nil {
		return nil, nil, err
	}
	if thumb, err = encode(resize(fullImg, Thumb)); err != nil {
		return nil, nil, err
	}
	return full, thumb, nil
}

func encode(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// resize shrinks src so its longest side is at most maxSide, keeping the
// aspect ratio. It never enlarges. Each output pixel is the average of the
// source area it covers; source pixels on the edge of that area count in
// proportion to how much of them is covered.
func resize(src image.Image, maxSide int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	long := max(sw, sh)
	if long <= maxSide {
		return src
	}
	dw := max(1, (sw*maxSide+long/2)/long)
	dh := max(1, (sh*maxSide+long/2)/long)

	rgba := image.NewRGBA(image.Rect(0, 0, sw, sh))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	xr, yr := float64(sw)/float64(dw), float64(sh)/float64(dh)
	for dy := 0; dy < dh; dy++ {
		y0, y1 := float64(dy)*yr, float64(dy+1)*yr
		for dx := 0; dx < dw; dx++ {
			x0, x1 := float64(dx)*xr, float64(dx+1)*xr
			var sum [4]float64
			var area float64
			for y := int(y0); y < sh && float64(y) < y1; y++ {
				wy := min(y1, float64(y+1)) - max(y0, float64(y))
				row := rgba.Pix[y*rgba.Stride:]
				for x := int(x0); x < sw && float64(x) < x1; x++ {
					w := wy * (min(x1, float64(x+1)) - max(x0, float64(x)))
					p := row[x*4 : x*4+4]
					for i := 0; i < 4; i++ {
						sum[i] += w * float64(p[i])
					}
					area += w
				}
			}
			o := dst.Pix[dy*dst.Stride+dx*4:]
			for i := 0; i < 4; i++ {
				o[i] = uint8(sum[i]/area + 0.5)
			}
		}
	}
	return dst
}

// Dir is the folder photos are kept in.
type Dir struct{ Path string }

// ValidName reports whether name is a name Save could have made: 32
// lowercase hex characters, nothing else.
func ValidName(name string) bool {
	if len(name) != nameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Save writes the photo and thumbnail under a new random name.
func (d Dir) Save(full, thumb []byte) (string, error) {
	if err := os.MkdirAll(d.Path, 0o750); err != nil {
		return "", err
	}
	raw := make([]byte, nameBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	name := hex.EncodeToString(raw)
	if err := writeNew(d.file(name, false), full); err != nil {
		return "", err
	}
	if err := writeNew(d.file(name, true), thumb); err != nil {
		os.Remove(d.file(name, false))
		return "", err
	}
	return name, nil
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

func (d Dir) file(name string, thumb bool) string {
	if thumb {
		return filepath.Join(d.Path, name+"_t.jpg")
	}
	return filepath.Join(d.Path, name+".jpg")
}

// Remove deletes both files. Missing files and invalid names are ignored.
func (d Dir) Remove(name string) {
	if !ValidName(name) {
		return
	}
	os.Remove(d.file(name, false))
	os.Remove(d.file(name, true))
}

// Open opens the photo or its thumbnail. The name must pass ValidName.
func (d Dir) Open(name string, thumb bool) (*os.File, error) {
	if !ValidName(name) {
		return nil, os.ErrNotExist
	}
	return os.Open(d.file(name, thumb))
}
