package chat

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const MaxPhotoBytes = 10 * 1024 * 1024
const MaxPhotoPixels = 20 * 1000 * 1000

// Bound concurrent decodes even when several clients upload large images.
var photoDecoders = make(chan struct{}, 2)

func (s *Server) sendPhoto(w http.ResponseWriter, r *http.Request, u Session) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxPhotoBytes+64*1024)
	if err := r.ParseMultipartForm(1024 * 1024); err != nil {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
		fail(w, 400, "照片上传失败，最大 10 MiB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	clientID := r.FormValue("client_id")
	if !validClientID(clientID) {
		fail(w, 400, "需要有效消息 ID")
		return
	}
	file, header, err := r.FormFile("photo")
	if err != nil {
		fail(w, 400, "缺少照片")
		return
	}
	defer file.Close()
	if header.Size < 1 || header.Size > MaxPhotoBytes {
		fail(w, 400, "照片最大 10 MiB")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxPhotoBytes+1))
	if err != nil || len(raw) > MaxPhotoBytes {
		fail(w, 400, "照片读取失败")
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") {
		fail(w, 400, "请发送有效的 JPEG 或 PNG 照片")
		return
	}
	if config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > MaxPhotoPixels {
		fail(w, 400, "照片像素过大，请先压缩（最多 2000 万像素）")
		return
	}
	select {
	case photoDecoders <- struct{}{}:
		defer func() { <-photoDecoders }()
	case <-r.Context().Done():
		return
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		fail(w, 400, "照片已损坏，无法解码")
		return
	}
	ext, mt := ".jpg", "image/jpeg"
	if format == "png" {
		ext, mt = ".png", "image/png"
	}
	name := randomID() + ext
	path := filepath.Join(s.mediaDir, name)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		s.internalError(w, err)
		return
	}
	keep := false
	defer func() {
		if !keep {
			os.Remove(path)
		}
	}()
	// Re-encode the decoded pixels: discard metadata, appended content and filenames.
	if format == "png" {
		err = png.Encode(out, img)
	} else {
		err = jpeg.Encode(out, img, &jpeg.Options{Quality: 88})
	}
	err = errors.Join(err, out.Sync(), out.Close())
	if err != nil {
		s.internalError(w, err)
		return
	}
	m, err := s.persistMessage(Message{ChannelID: requestChannel(r), ClientID: clientID, SessionID: u.ID, Nickname: u.Nickname, Kind: "photo", Media: name, MIME: mt})
	if err != nil {
		s.internalError(w, err)
		return
	}
	keep = m.Media == name
	writeJSON(w, 200, m)
}
