package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
)

func TestCatchUpFromZeroReturnsOldestFirst(t *testing.T) {
	ts, store := testServer(t)
	cookie := login(t, ts, "补消息")
	u, _ := store.Session(cookie.Value)
	for i := 0; i < 60; i++ {
		if _, err := store.SaveMessage(Message{ClientID: fmt.Sprint(i), SessionID: u.ID, Nickname: u.Nickname, Kind: "text", Text: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	resp := request(t, ts, "GET", "/api/messages?after=0", "", cookie)
	defer resp.Body.Close()
	var page []Message
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page) != 50 || page[0].ID != 1 || page[49].ID != 50 {
		t.Fatalf("catch-up skipped initial messages: first=%d last=%d", page[0].ID, page[len(page)-1].ID)
	}
}

func testServer(t *testing.T) (*httptest.Server, *Store) {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	app := NewServer(s, t.TempDir(), nil, fstest.MapFS{"index.html": {Data: []byte("hello")}})
	ts := httptest.NewServer(app)
	t.Cleanup(func() { app.Close(); ts.Close(); s.Close() })
	return ts, s
}
func request(t *testing.T, ts *httptest.Server, method, path, body string, cookie *http.Cookie) *http.Response {
	t.Helper()
	r, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	resp, err := ts.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
func login(t *testing.T, ts *httptest.Server, name string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"nickname": name})
	resp := request(t, ts, "POST", "/api/login", string(body), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login %d: %s", resp.StatusCode, b)
	}
	return resp.Cookies()[0]
}
func TestChatHTTPAndWebSocket(t *testing.T) {
	ts, _ := testServer(t)
	resp := request(t, ts, "GET", "/api/messages", "", nil)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("anonymous history allowed")
	}
	a := login(t, ts, "甲")
	b := login(t, ts, "乙")
	headers := http.Header{"Cookie": []string{b.String()}, "Origin": []string{ts.URL}}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	resp = request(t, ts, "POST", "/api/messages", `{"client_id":"test-1","text":"你好 <script>"}`, a)
	var saved Message
	json.NewDecoder(resp.Body).Decode(&saved)
	resp.Body.Close()
	if resp.StatusCode != 200 || saved.Text != "你好 <script>" {
		t.Fatalf("save failed: %+v", saved)
	}
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	seen := false
	for !seen {
		var event struct {
			Type    string  `json:"type"`
			Message Message `json:"message"`
		}
		if err := ws.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "message" {
			seen = event.Message.ID == saved.ID
		}
	}
	resp = request(t, ts, "POST", "/api/messages", `{"client_id":"test-1","text":"你好 <script>"}`, a)
	resp.Body.Close()
	resp = request(t, ts, "GET", "/api/messages", "", b)
	var messages []Message
	json.NewDecoder(resp.Body).Decode(&messages)
	resp.Body.Close()
	if len(messages) != 1 {
		t.Fatalf("duplicate persisted: %+v", messages)
	}
	if strings.Contains(saved.SessionID, a.Value) {
		t.Fatal("credential leaked in message")
	}
	resp = request(t, ts, "POST", "/api/logout", "{}", a)
	resp.Body.Close()
	resp = request(t, ts, "GET", "/api/messages", "", a)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("logout failed")
	}
}
func TestValidationAndOrigin(t *testing.T) {
	ts, _ := testServer(t)
	resp := request(t, ts, "POST", "/api/login", `{"nickname":"  "}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatal("empty nickname accepted")
	}
	cookie := login(t, ts, "甲")
	for _, body := range []string{`{"client_id":"a","text":" "}`, `{"text":"no id"}`, `{"client_id":"b","text":"` + strings.Repeat("字", 4001) + `"}`} {
		resp = request(t, ts, "POST", "/api/messages", body, cookie)
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("invalid payload accepted: status %d", resp.StatusCode)
		}
	}
	r, _ := http.NewRequest("POST", ts.URL+"/api/messages", strings.NewReader(`{"client_id":"x","text":"evil"}`))
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross-origin request accepted")
	}
	h := http.Header{"Cookie": []string{cookie.String()}, "Origin": []string{"https://evil.example"}}
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", h)
	if err == nil {
		conn.Close()
		t.Fatal("cross-origin WS accepted")
	}
	if resp.StatusCode != 403 {
		t.Fatal(resp.StatusCode)
	}
}
func TestAudioUploadReadAndInvalidType(t *testing.T) {
	ts, _ := testServer(t)
	cookie := login(t, ts, "录音")
	upload := func(content []byte, id string) *http.Response {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="audio"; filename="voice.webm"`)
		h.Set("Content-Type", "audio/webm")
		part, _ := w.CreatePart(h)
		part.Write(content)
		w.WriteField("client_id", id)
		w.WriteField("duration", "2.5")
		w.Close()
		req, _ := http.NewRequest("POST", ts.URL+"/api/audio", &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.AddCookie(cookie)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	content := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, []byte("test fixture")...)
	resp := upload(content, "v1")
	var m Message
	json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if resp.StatusCode != 200 || m.Kind != "audio" {
		t.Fatalf("audio failed: %+v", m)
	}
	resp = request(t, ts, "GET", "/media/"+m.Media, "", cookie)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, content) {
		t.Fatal("recording changed")
	}
	resp = request(t, ts, "GET", "/media/"+m.Media, "", nil)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("anonymous media access")
	}
	resp = upload([]byte("<html>invalid</html>"), "v2")
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatal("invalid media accepted")
	}
}
