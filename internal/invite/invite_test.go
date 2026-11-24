package invite

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) notify(userID string, event map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := fmt.Sprintf("%s %v", userID, event["type"])
	switch event["type"] {
	case "voice_invite":
		line += fmt.Sprintf(" from=%s channel=%v ttl=%v", event["from"].(User).ID, event["channel_id"], event["expires_in"])
	case "voice_invite_reply":
		line += fmt.Sprintf(" %s=%v", event["user"].(User).ID, event["action"])
	case "voice_invite_end":
		line += fmt.Sprintf(" %v", event["reason"])
	}
	r.events = append(r.events, line)
}
func (r *recorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := r.events
	r.events = nil
	return events
}
func expect(t *testing.T, r *recorder, want ...string) {
	t.Helper()
	got := r.take()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got: %q\nwant: %q", got, want)
	}
}

var (
	host = User{ID: "host", Nickname: "发起人"}
	ann  = User{ID: "ann", Nickname: "小安"}
	bob  = User{ID: "bob", Nickname: "小博"}
)

func TestRingDeclineAndAccept(t *testing.T) {
	r := &recorder{}
	m := New(time.Minute, r.notify)
	defer m.Close()
	rung, ok := m.Create("i1", "c", host, []User{ann, bob, ann, host})
	if !ok || !reflect.DeepEqual(rung, []User{ann, bob}) {
		t.Fatalf("rung=%v ok=%v", rung, ok)
	}
	expect(t, r, "ann voice_invite from=host channel=c ttl=60", "bob voice_invite from=host channel=c ttl=60")
	if m.Reply("i1", "stranger", "decline") || m.Reply("missing", "ann", "decline") {
		t.Fatal("reply accepted from a user who was not invited")
	}
	if !m.Reply("i1", "ann", "decline") {
		t.Fatal("decline rejected")
	}
	expect(t, r, "host voice_invite_reply ann=decline", "ann voice_invite_end decline")
	if m.Reply("i1", "ann", "decline") {
		t.Fatal("second reply accepted")
	}
	// Bob joins voice by any route: the inviter hears "accept", Bob's tabs stop ringing.
	m.Sync("other", map[string]bool{"host": true, "bob": true})
	expect(t, r)
	m.Sync("c", map[string]bool{"host": true, "bob": true})
	expect(t, r, "host voice_invite_reply bob=accept", "bob voice_invite_end accept")
	if len(m.invites) != 0 {
		t.Fatal("settled invitation kept")
	}
}

func TestDuplicateIDAndAlreadyRinging(t *testing.T) {
	r := &recorder{}
	m := New(time.Minute, r.notify)
	defer m.Close()
	m.Create("i1", "c", host, []User{ann})
	r.take()
	if _, ok := m.Create("i1", "c", host, []User{bob}); ok {
		t.Fatal("duplicate id accepted")
	}
	// Ann is still ringing for this inviter; only Bob gets a new ring.
	rung, ok := m.Create("i2", "c", host, []User{ann, bob})
	if !ok || !reflect.DeepEqual(rung, []User{bob}) {
		t.Fatalf("rung=%v ok=%v", rung, ok)
	}
	expect(t, r, "bob voice_invite from=host channel=c ttl=60")
	rung, ok = m.Create("i3", "c", host, []User{ann, bob})
	if !ok || len(rung) != 0 || m.invites["i3"] != nil {
		t.Fatalf("empty invitation stored: rung=%v ok=%v", rung, ok)
	}
	// A different inviter may ring Ann at the same time.
	rung, _ = m.Create("i4", "c", bob, []User{ann})
	if len(rung) != 1 {
		t.Fatal("second inviter blocked")
	}
}

func TestInviterLeavingCancels(t *testing.T) {
	r := &recorder{}
	m := New(time.Minute, r.notify)
	defer m.Close()
	m.Create("i1", "c", host, []User{ann, bob})
	r.take()
	m.Sync("c", map[string]bool{})
	got := r.take()
	if len(got) != 2 || len(m.invites) != 0 {
		t.Fatalf("cancel events: %q", got)
	}
	for _, line := range got {
		if line != "ann voice_invite_end cancelled" && line != "bob voice_invite_end cancelled" {
			t.Fatalf("unexpected event %q", line)
		}
	}
	if m.Reply("i1", "ann", "decline") {
		t.Fatal("reply accepted after cancel")
	}
}

func TestTimeoutAndOffline(t *testing.T) {
	r := &recorder{}
	m := New(80*time.Millisecond, r.notify)
	defer m.Close()
	m.Create("i1", "c", host, []User{ann, bob})
	r.take()
	m.Offline("stranger")
	expect(t, r)
	m.Offline("ann")
	expect(t, r, "host voice_invite_reply ann=offline", "ann voice_invite_end offline")
	deadline := time.Now().Add(2 * time.Second)
	for {
		m.mu.Lock()
		left := len(m.invites)
		m.mu.Unlock()
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("invitation never expired")
		}
		time.Sleep(10 * time.Millisecond)
	}
	expect(t, r, "host voice_invite_reply bob=timeout", "bob voice_invite_end timeout")
	if m.Reply("i1", "bob", "decline") {
		t.Fatal("reply accepted after timeout")
	}
}

func TestCloseStopsTimers(t *testing.T) {
	r := &recorder{}
	m := New(30*time.Millisecond, r.notify)
	m.Create("i1", "c", host, []User{ann})
	r.take()
	m.Close()
	time.Sleep(80 * time.Millisecond)
	expect(t, r)
	if _, ok := m.Create("i2", "c", host, []User{ann}); ok {
		t.Fatal("closed manager accepted an invitation")
	}
}
