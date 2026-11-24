package chat

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestChannelHTTPIsolationAndValidation(t *testing.T) {
	ts, _ := testServer(t)
	cookie := login(t, ts, "创建者")
	resp := request(t, ts, "POST", "/api/channels", `{"name":" 项目讨论 "}`, cookie)
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create channel: status %d", resp.StatusCode)
	}
	var channel struct{ ID, Name string }
	json.NewDecoder(resp.Body).Decode(&channel)
	if channel.ID == "" || channel.Name != "项目讨论" {
		t.Fatalf("channel: %+v", channel)
	}
	for i, path := range []string{"/api/messages", "/api/messages?channel_id=" + channel.ID} {
		resp = request(t, ts, "POST", path, fmt.Sprintf(`{"client_id":"message-%d","text":"%s"}`, i, path), cookie)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("send: %d", resp.StatusCode)
		}
	}
	for _, path := range []string{"/api/messages", "/api/messages?channel_id=" + channel.ID} {
		resp = request(t, ts, "GET", path, "", cookie)
		var messages []Message
		json.NewDecoder(resp.Body).Decode(&messages)
		resp.Body.Close()
		if len(messages) != 1 || messages[0].Text != path {
			t.Fatalf("cross-channel history: %+v", messages)
		}
	}
	for _, body := range []string{`{"name":"项目讨论"}`, `{"name":" "}`} {
		resp = request(t, ts, "POST", "/api/channels", body, cookie)
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Fatalf("invalid channel accepted")
		}
	}
	resp = request(t, ts, "GET", "/api/messages?channel_id=missing", "", cookie)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing channel: %d", resp.StatusCode)
	}
	resp = request(t, ts, "POST", "/api/channels", `{"name":"匿名"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("anonymous create")
	}
}

// Start from the deployed single-room schema, then reopen twice to prove that
// migrations preserve historical messages, audio references and new channels.
func TestLegacyMigrationAndChannelPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE messages(id INTEGER PRIMARY KEY AUTOINCREMENT,client_id TEXT NOT NULL,session_id TEXT NOT NULL,nickname TEXT NOT NULL,kind TEXT NOT NULL,text TEXT NOT NULL DEFAULT '',media TEXT NOT NULL DEFAULT '',mime TEXT NOT NULL DEFAULT '',duration REAL NOT NULL DEFAULT 0,created_at TEXT NOT NULL,UNIQUE(session_id,client_id));
 INSERT INTO messages(client_id,session_id,nickname,kind,text,created_at) VALUES('old','s','旧用户','text','以前的消息','2026-11-09');
 INSERT INTO messages(client_id,session_id,nickname,kind,media,mime,duration,created_at) VALUES('audio','s','旧用户','audio','old.webm','audio/webm',5,'2026-11-09');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.CreateChannel("重启保留")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.SaveMessage(Message{ChannelID: c.ID, ClientID: "new", SessionID: "s", Nickname: "旧用户", Kind: "text", Text: "频道历史"})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	for i := 0; i < 2; i++ {
		store, err = OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		old, err := store.Messages(0, 0, 50)
		if err != nil || len(old) != 2 || old[0].Text != "以前的消息" || old[1].Media != "old.webm" || old[1].ChannelID != PublicChannel {
			t.Fatalf("old history lost: %+v %v", old, err)
		}
		messages, err := store.messages(c.ID, 0, 0, 50, false)
		if err != nil || len(messages) != 1 || messages[0].Text != "频道历史" {
			t.Fatalf("new history lost: %+v %v", messages, err)
		}
		channels, err := store.Channels()
		if err != nil || len(channels) != 2 || channels[1].Name != c.Name {
			t.Fatalf("channels lost: %+v %v", channels, err)
		}
		if _, err = store.SaveMessage(Message{ChannelID: c.ID, ClientID: "old", SessionID: "s", Kind: "text"}); err == nil {
			t.Fatal("cross-channel retry returned public message")
		}
		store.Close()
	}
}

func TestRetryDoesNotBroadcastOldMessage(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app := NewServer(store, t.TempDir(), nil, fstest.MapFS{})
	defer app.Close()
	// Capture deliveries through the real hub queue without a network writer.
	c := &client{channel: PublicChannel, send: make(chan []byte, 10)}
	app.hub.clients[c] = true
	defer delete(app.hub.clients, c)
	m := Message{ClientID: "once", SessionID: "s", Nickname: "发送者", Kind: "text", Text: "一次"}
	if _, err = app.persistMessage(m); err != nil {
		t.Fatal(err)
	}
	if len(c.send) != 1 {
		t.Fatal("new message missing")
	}
	<-c.send
	if _, err = app.persistMessage(m); err != nil {
		t.Fatal(err)
	}
	if len(c.send) != 0 {
		t.Fatal("retry rebroadcast historical message")
	}
}

func TestChannelAudioAndPaginationIsolation(t *testing.T) {
	ts, store := testServer(t)
	cookie := login(t, ts, "录音频道")
	channel, err := store.CreateChannel("录音讨论")
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="audio"; filename="a.webm"`)
	header.Set("Content-Type", "audio/webm")
	part, _ := writer.CreatePart(header)
	content := []byte{0x1a, 0x45, 0xdf, 0xa3, 0, 0, 1}
	part.Write(content)
	writer.WriteField("client_id", "audio-scoped")
	writer.WriteField("duration", "1")
	writer.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/audio?channel_id="+channel.ID, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var saved Message
	json.NewDecoder(resp.Body).Decode(&saved)
	resp.Body.Close()
	if resp.StatusCode != 200 || saved.ChannelID != channel.ID {
		t.Fatalf("audio scope: %d %+v", resp.StatusCode, saved)
	}
	for _, path := range []string{"/media/" + saved.Media, "/media/" + saved.Media + "?channel_id=" + channel.ID} {
		resp = request(t, ts, "GET", path, "", cookie)
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(path, "channel_id=") {
			if !bytes.Equal(got, content) {
				t.Fatal("channel audio changed")
			}
		} else if resp.StatusCode != 404 {
			t.Fatal("cross-channel media allowed")
		}
	}
	u, _ := store.Session(cookie.Value)
	for i := 0; i < 60; i++ {
		for _, c := range []string{PublicChannel, channel.ID} {
			_, err = store.SaveMessage(Message{ChannelID: c, ClientID: fmt.Sprintf("%s-%d", c, i), SessionID: u.ID, Kind: "text", Text: c})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, suffix := range []string{"", "&after=0", "&before=100"} {
		resp = request(t, ts, "GET", "/api/messages?channel_id="+channel.ID+suffix, "", cookie)
		var messages []Message
		json.NewDecoder(resp.Body).Decode(&messages)
		resp.Body.Close()
		if len(messages) == 0 {
			t.Fatal("empty page")
		}
		for _, m := range messages {
			if m.ChannelID != channel.ID {
				t.Fatal("pagination crossed channel")
			}
		}
	}
}
