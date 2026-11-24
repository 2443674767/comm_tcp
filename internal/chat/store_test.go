package chat

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPersistencePaginationAndRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession("小明")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ id, text string }{{"a", "第一条"}, {"b", "第二条"}, {"c", "第三条"}} {
		if _, err := s.SaveMessage(Message{ClientID: item.id, SessionID: session.ID, Nickname: session.Nickname, Kind: "text", Text: item.text}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.SaveMessage(Message{ClientID: "a", SessionID: session.ID, Nickname: session.Nickname, Kind: "text", Text: "重复提交不能覆盖"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != "第一条" || first.ID != 1 {
		t.Fatalf("retry changed message: %+v", first)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Session(session.Token)
	if err != nil || got.Nickname != "小明" {
		t.Fatalf("session lost: %+v %v", got, err)
	}
	page, err := s.Messages(0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Text != "第二条" || page[1].Text != "第三条" {
		t.Fatalf("latest page: %+v", page)
	}
	older, _ := s.Messages(page[0].ID, 0, 2)
	if len(older) != 1 || older[0].Text != "第一条" {
		t.Fatalf("older: %+v", older)
	}
	missed, _ := s.Messages(0, 1, 2)
	if len(missed) != 2 || missed[0].ID != 2 || missed[1].ID != 3 {
		t.Fatalf("catch up: %+v", missed)
	}
	if err := s.DeleteSession(session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(session.Token); err == nil {
		t.Fatal("logged-out session still valid")
	}
	history, _ := s.Messages(0, 0, 50)
	if len(history) != 3 {
		t.Fatal("logout erased history")
	}
}

func TestExpiredSessionAndAudioPersistence(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, _ := s.CreateSession("语音用户")
	m, err := s.SaveMessage(Message{ClientID: "voice", SessionID: u.ID, Nickname: u.Nickname, Kind: "audio", Media: "random.webm", MIME: "audio/webm", Duration: 3.5})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.MediaMessage(m.Media)
	if err != nil || stored.Duration != 3.5 || stored.MIME != "audio/webm" {
		t.Fatalf("media lost: %+v %v", stored, err)
	}
	_, err = s.db.Exec("UPDATE sessions SET expires_at=? WHERE id=?", time.Now().Add(-time.Hour).Unix(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Session(u.Token); err == nil {
		t.Fatal("expired session accepted")
	}
}
