package chat

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestOnlineListAndInviteDeliveryCrossChannels(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app := NewServer(store, t.TempDir(), nil, fstest.MapFS{})
	defer app.Close()
	// Capture deliveries through the real hub queue without a network writer.
	a := &client{channel: PublicChannel, user: Session{ID: "a", Nickname: "甲"}, send: make(chan []byte, 10)}
	b := &client{channel: "other", user: Session{ID: "b", Nickname: "乙"}, send: make(chan []byte, 10)}
	app.hub.clients[a], app.hub.clients[b] = true, true
	defer func() { delete(app.hub.clients, a); delete(app.hub.clients, b) }()
	next := func(c *client) map[string]any {
		t.Helper()
		if len(c.send) == 0 {
			t.Fatal("expected an event")
		}
		var event map[string]any
		if err := json.Unmarshal(<-c.send, &event); err != nil {
			t.Fatal(err)
		}
		return event
	}
	app.hub.presence(PublicChannel)
	if event := next(a); event["type"] != "presence" || len(event["users"].([]any)) != 1 {
		t.Fatalf("channel presence: %v", event)
	}
	// Both channels learn who is online anywhere, and where.
	for _, c := range []*client{a, b} {
		event := next(c)
		users, _ := event["users"].([]any)
		if event["type"] != "online" || len(users) != 2 {
			t.Fatalf("online list: %v", event)
		}
		second := users[1].(map[string]any)
		if second["id"] != "b" || second["nickname"] != "乙" || second["channel_id"] != "other" {
			t.Fatalf("online entry: %v", second)
		}
	}
	if len(a.send) != 0 || len(b.send) != 0 {
		t.Fatal("unexpected extra events")
	}
	if u, ok := app.hub.user("b"); !ok || u.Nickname != "乙" {
		t.Fatal("user in another channel not found")
	}
	if _, ok := app.hub.user("nobody"); ok {
		t.Fatal("offline user reported online")
	}
	app.hub.sendUser("b", map[string]any{"type": "voice_invite"})
	if event := next(b); event["type"] != "voice_invite" || len(a.send) != 0 {
		t.Fatalf("invite delivery: %v", event)
	}
}

func TestInviteNeedsVoiceServiceAndLogin(t *testing.T) {
	ts, _ := testServer(t)
	resp := request(t, ts, "POST", "/api/voice-invite", `{"invite_id":"i1","targets":["x"]}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous invite: %d", resp.StatusCode)
	}
	cookie := login(t, ts, "邀请人")
	resp = request(t, ts, "POST", "/api/voice-invite", `{"invite_id":"i1","targets":["x"]}`, cookie)
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("invite without voice service: %d", resp.StatusCode)
	}
	resp = request(t, ts, "POST", "/api/voice-invite/reply", `{"invite_id":"i1","action":"accept"}`, cookie)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("unknown reply action: %d", resp.StatusCode)
	}
	resp = request(t, ts, "POST", "/api/voice-invite/reply", `{"invite_id":"i1","action":"decline"}`, cookie)
	resp.Body.Close()
	if resp.StatusCode != 410 {
		t.Fatalf("reply to missing invitation: %d", resp.StatusCode)
	}
}
