package voice

import (
	"context"
	"fmt"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"testing"
	"time"
)

func connectPeerKind(t *testing.T, r *Room, id string, video bool) (*webrtc.PeerConnection, *webrtc.TrackLocalStaticRTP, <-chan *rtp.Packet) {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	kind, mime := webrtc.RTPCodecTypeAudio, webrtc.MimeTypeOpus
	if video {
		kind, mime = webrtc.RTPCodecTypeVideo, webrtc.MimeTypeVP8
	}
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: mime}, "mic", id)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := pc.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, e := sender.Sender().Read(buf); e != nil {
				return
			}
		}
	}()
	for i := 0; i < 4; i++ {
		if _, err = pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			t.Fatal(err)
		}
	}
	received := make(chan *rtp.Packet, 32)
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			p, _, e := remote.ReadRTP()
			if e != nil {
				return
			}
			select {
			case received <- p:
			default:
			}
		}
	})
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(10 * time.Second):
		t.Fatal("client ICE timeout")
	}
	answer, err := r.Join(context.Background(), id, id, id+"-call", *pc.LocalDescription())
	if err != nil {
		t.Fatal(err)
	}
	if err = pc.SetRemoteDescription(answer); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Leave(id) })
	return pc, track, received
}

func TestServerForwardsAudioAndReusesSlots(t *testing.T) {
	r, err := NewRoom(Config{HostIP: "127.0.0.1", UDPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, a, _ := connectPeer(t, r, "alice")
	_, _, heard := connectPeer(t, r, "bob")
	sendUntilHeard := func(track *webrtc.TrackLocalStaticRTP, base uint16) *rtp.Packet {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		seq := base
		for {
			select {
			case <-timer.C:
				t.Fatal("audio was not forwarded through server")
				return nil
			case p := <-heard:
				if len(p.Payload) == 3 {
					return p
				}
			case <-ticker.C:
				seq++
				_ = track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq) * 960, SSRC: 1234}, Payload: []byte{0xf8, 0xff, 0xfe}})
			}
		}
	}
	first := sendUntilHeard(a, 100)
	r.Leave("alice")
	_, replacement, _ := connectPeer(t, r, "carol")
	second := sendUntilHeard(replacement, 1)
	if second.SequenceNumber <= first.SequenceNumber || second.Timestamp <= first.Timestamp {
		t.Fatalf("slot timeline reset: %v -> %v", first.Header, second.Header)
	}
	if len(r.Members()) != 2 {
		t.Fatalf("wrong membership: %+v", r.Members())
	}
}

func TestCapacityAndInvalidOfferCleanup(t *testing.T) {
	r, err := NewRoom(Config{HostIP: "127.0.0.1", UDPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = r.Join(context.Background(), "bad", "bad", "bad-call", webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "invalid"}); err == nil {
		t.Fatal("invalid offer accepted")
	}
	if len(r.Members()) != 0 {
		t.Fatal("invalid offer leaked a slot")
	}
	for i := 0; i < 5; i++ {
		connectPeer(t, r, fmt.Sprint(i))
	}
	if _, err = r.Join(context.Background(), "six", "six", "six-call", webrtc.SessionDescription{}); err != ErrFull {
		t.Fatalf("sixth participant: %v", err)
	}
	r.Leave("0")
	if len(r.Members()) != 4 {
		t.Fatal("leave did not free slot")
	}
}

func TestStaleCallCleanupCannotRemoveCurrentCall(t *testing.T) {
	r, err := NewRoom(Config{HostIP: "127.0.0.1", UDPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	connectPeer(t, r, "alice")
	r.LeaveCall("alice", "different-call")
	if len(r.Members()) != 1 {
		t.Fatal("another tab's cleanup removed active call")
	}
	r.LeaveCall("alice", "alice-call")
	if len(r.Members()) != 0 {
		t.Fatal("owner could not leave its call")
	}
}

func TestSiblingRoomsIsolateMediaAndMembership(t *testing.T) {
	root, err := NewRoom(Config{HostIP: "127.0.0.1", UDPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	other, err := root.NewSibling()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	events := make(chan string, 10)
	root.OnMembership = func(action string, m Member) { events <- action + ":" + m.ID }
	sender, track, _ := connectPeer(t, root, "sender")
	receiver, _, heard := connectPeer(t, root, "receiver")
	outsider, _, wrong := connectPeer(t, other, "outsider")
	outsider2, _, wrong2 := connectPeer(t, other, "outsider2")
	for _, pc := range []*webrtc.PeerConnection{sender, receiver, outsider, outsider2} {
		deadline := time.Now().Add(10 * time.Second)
		for pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
			if time.Now().After(deadline) {
				t.Fatal("peer not connected")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var seq uint16
	received := false
	joined := 0
	for !received || joined < 2 {
		select {
		case <-deadline:
			t.Fatal("same-room audio or connected events missing")
		case <-heard:
			received = true
		case <-events:
			joined++
		case <-wrong:
			t.Fatal("audio crossed rooms")
		case <-wrong2:
			t.Fatal("audio crossed rooms to slot 1")
		case <-ticker.C:
			seq++
			track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq) * 960, SSRC: 1234}, Payload: []byte{0xf8, 0xff, 0xfe}})
		}
	}
	select {
	case <-wrong:
		t.Fatal("audio crossed rooms")
	case <-wrong2:
		t.Fatal("audio crossed rooms to slot 1")
	case <-time.After(200 * time.Millisecond):
	}
	root.Leave("sender")
	select {
	case e := <-events:
		if e != "leave:sender" {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("leave event missing")
	}
	root.Leave("sender")
	select {
	case e := <-events:
		t.Fatalf("duplicate event %s", e)
	default:
	}
}

func connectPeer(t *testing.T, r *Room, id string) (*webrtc.PeerConnection, *webrtc.TrackLocalStaticRTP, <-chan *rtp.Packet) {
	return connectPeerKind(t, r, id, false)
}
func TestVideoForwardAndReuse(t *testing.T) {
	r, err := NewRoom(Config{UDPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, a, _ := connectPeerKind(t, r, "a", true)
	_, _, heard := connectPeerKind(t, r, "b", true)
	send := func(track *webrtc.TrackLocalStaticRTP) *rtp.Packet {
		t.Helper()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.After(5 * time.Second)
		var seq uint16
		for {
			select {
			case p := <-heard:
				return p
			case <-deadline:
				t.Fatal("video not forwarded")
				return nil
			case <-ticker.C:
				seq++
				track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq) * 3000, Marker: true}, Payload: []byte{0x10, 0, 1, 2}})
			}
		}
	}
	first := send(a)
	r.Leave("a")
	_, c, _ := connectPeerKind(t, r, "c", true)
	second := send(c)
	if second.SequenceNumber <= first.SequenceNumber || second.Timestamp <= first.Timestamp {
		t.Fatal("video slot timeline reset")
	}
}

// Both rooms have occupied matching slots, so a leaked shared video track
// cannot hide behind the SFU's self-subscription exclusion.
func TestSiblingRoomsIsolateVideo(t *testing.T) {
	root, err := NewRoom(Config{UDPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	other, err := root.NewSibling()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_, track, _ := connectPeerKind(t, root, "publisher", true)
	_, _, heard := connectPeerKind(t, root, "viewer", true)
	_, _, wrong := connectPeerKind(t, other, "outsider", true)
	_, _, wrong2 := connectPeerKind(t, other, "outsider2", true)
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var seq uint16
	received := false
	for {
		select {
		case <-deadline:
			if !received {
				t.Fatal("same room video missing")
			}
			return
		case <-heard:
			received = true
		case <-wrong:
			t.Fatal("video crossed rooms")
		case <-wrong2:
			t.Fatal("video crossed rooms to second slot")
		case <-ticker.C:
			seq++
			track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq) * 3000, Marker: true}, Payload: []byte{0x90, 0x80, 0x80, 0x01, 0, 1, 2}})
		}
	}
}

func TestVideoPreservesReorderedAndRepairedPackets(t *testing.T) {
	for _, base := range []uint16{100, 65534} {
		t.Run(fmt.Sprint(base), func(t *testing.T) {
			r, err := NewRoom(Config{UDPAddr: "127.0.0.1:0"})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			publisher, _, _ := connectPeerKind(t, r, "publisher", true)
			receiver, _, heard := connectPeerKind(t, r, "receiver", true)
			for _, pc := range []*webrtc.PeerConnection{publisher, receiver} {
				deadline := time.Now().Add(5 * time.Second)
				for pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
					if time.Now().After(deadline) {
						t.Fatal("ICE timeout")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			r.mu.Lock()
			p := r.slots[0].peer
			r.mu.Unlock()
			// Two frames delivered out of order. The missing
			// packet must retain its original sequence and frame timestamp at the viewer.
			baseTS := uint32(0xfffffc17)
			for _, n := range []uint16{0, 2, 1, 3} {
				ts := baseTS
				if n >= 2 {
					ts += 3000
				}
				r.forwardVideo(p, &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: base + n, Timestamp: ts, Marker: n == 1 || n == 3}, Payload: []byte{0x10, 0, byte(n)}})
			}
			for _, n := range []uint16{0, 2, 1, 3} {
				select {
				case packet := <-heard:
					wantTS := uint32(3000)
					if n >= 2 {
						wantTS += 3000
					}
					if packet.SequenceNumber != n+1 || packet.Timestamp != wantTS || packet.Payload[2] != byte(n) {
						t.Fatalf("lost repair mapping: got %v payload %v, want seq %d ts %d", packet.Header, packet.Payload, n+1, wantTS)
					}
				case <-time.After(time.Second):
					t.Fatalf("reordered/repaired packet %d was dropped", n)
				}
			}
			// A repeated repair maps identically, though SRTP correctly suppresses
			// duplicate delivery at the viewer. It must not move the high water.
			duplicate := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: base + 1, Timestamp: baseTS}, Payload: []byte{0x10, 0, 1}}
			r.forwardVideo(p, duplicate)
			if duplicate.SequenceNumber != 2 || duplicate.Timestamp != 3000 {
				t.Fatalf("duplicate repair remapped: %v", duplicate.Header)
			}
			r.mu.Lock()
			seq, ts := r.slots[0].videoSeq, r.slots[0].videoTS
			r.mu.Unlock()
			if seq != 4 || ts != 6000 {
				t.Fatalf("late packet moved high water backwards: %d %d", seq, ts)
			}
			r.Leave("publisher")
			replacement, _, _ := connectPeerKind(t, r, "replacement", true)
			deadline := time.Now().Add(5 * time.Second)
			for replacement.ConnectionState() != webrtc.PeerConnectionStateConnected {
				if time.Now().After(deadline) {
					t.Fatal("replacement ICE timeout")
				}
				time.Sleep(10 * time.Millisecond)
			}
			r.mu.Lock()
			p = r.slots[0].peer
			r.mu.Unlock()
			r.forwardVideo(p, &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: 17, Timestamp: 40, Marker: true}, Payload: []byte{0x10, 0, 7}})
			select {
			case packet := <-heard:
				if packet.SequenceNumber != 5 || packet.Timestamp != 9000 {
					t.Fatalf("reuse after repair reset timeline: %v", packet.Header)
				}
			case <-time.After(time.Second):
				t.Fatal("replacement video missing")
			}
		})
	}
}
