package chat

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
)

func photoUpload(t *testing.T, endpoint string, cookie *http.Cookie, data []byte, client string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	f, _ := w.CreateFormFile("photo", "../../photo.png")
	f.Write(data)
	w.WriteField("client_id", client)
	w.Close()
	req, _ := http.NewRequest("POST", endpoint, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
func TestPhotoUploadHistoryAndIsolation(t *testing.T) {
	ts, store := testServer(t)
	cookie := login(t, ts, "照片用户")
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	png.Encode(&buf, img)
	res := photoUpload(t, ts.URL+"/api/photos", nil, buf.Bytes(), "photo-auth")
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("anonymous %d", res.StatusCode)
	}
	upload := func() Message {
		res := photoUpload(t, ts.URL+"/api/photos", cookie, buf.Bytes(), "photo-once")
		defer res.Body.Close()
		if res.StatusCode != 200 {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("upload %d %s", res.StatusCode, b)
		}
		var m Message
		json.NewDecoder(res.Body).Decode(&m)
		return m
	}
	m := upload()
	if m.Kind != "photo" || m.MIME != "image/png" {
		t.Fatalf("message %+v", m)
	}
	if again := upload(); again.ID != m.ID || again.Media != m.Media {
		t.Fatal("duplicate upload")
	}
	page, err := store.Messages(0, 0, 50)
	if err != nil || len(page) != 1 || page[0].ID != m.ID {
		t.Fatal("photo history", err)
	}
	res = request(t, ts, "GET", "/media/"+m.Media, "", nil)
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("public media")
	}
	res = request(t, ts, "GET", "/media/"+m.Media, "", cookie)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("media unavailable")
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	res = request(t, ts, "POST", "/api/channels", `{"name":"其他"}`, cookie)
	var ch Channel
	json.NewDecoder(res.Body).Decode(&ch)
	res.Body.Close()
	res = request(t, ts, "GET", "/media/"+m.Media+"?channel_id="+ch.ID, "", cookie)
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("cross-channel media exposed")
	}
}
func TestRejectInvalidPhoto(t *testing.T) {
	ts, _ := testServer(t)
	cookie := login(t, ts, "图片校验")
	for _, data := range [][]byte{[]byte("<svg onload=alert(1)></svg>"), []byte("not an image"), bytes.Repeat([]byte{1}, 10*1024*1024+1)} {
		res := photoUpload(t, ts.URL+"/api/photos", cookie, data, "invalid-photo")
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("invalid image accepted %d", res.StatusCode)
		}
	}
}

func TestRejectOversizedPixelsAndTruncatedPhoto(t *testing.T) {
	ts, _ := testServer(t)
	cookie := login(t, ts, "尺寸验证")
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	raw := buf.Bytes()
	huge := append([]byte(nil), raw...)
	binary.BigEndian.PutUint32(huge[16:20], 5000)
	binary.BigEndian.PutUint32(huge[20:24], 5000)
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))
	for _, data := range [][]byte{huge, raw[:33]} {
		res := photoUpload(t, ts.URL+"/api/photos", cookie, data, "bad-pixels")
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("invalid dimensions or incomplete PNG accepted %d", res.StatusCode)
		}
	}
}
