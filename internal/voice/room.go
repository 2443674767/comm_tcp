// Package voice implements a five-slot audio/video SFU. Browsers publish one
// Opus stream and subscribe to four stable slots, so membership never requires
// renegotiation. Media always travels through this server.
package voice

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

const Capacity = 5

var ErrFull = errors.New("群语音已满（最多 5 人）")
var ErrAlreadyJoined = errors.New("你已在群语音中，请先退出其他页面的通话")

type Config struct{ HostIP, UDPAddr, TCPAddr string }
type Member struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	Slot     int    `json:"slot"`
}
type peer struct {
	videoSSRC      uint32
	videoFirst     bool
	videoLastSeq   uint16
	videoSeqOffset uint16
	videoTSOffset  uint32
	announced      bool
	callID         string
	Member
	pc      *webrtc.PeerConnection
	first   bool
	lastSeq uint16
	lastTS  uint32
}
type slot struct {
	video     *webrtc.TrackLocalStaticRTP
	videoSeq  uint16
	videoTS   uint32
	track     *webrtc.TrackLocalStaticRTP
	peer      *peer
	seq       uint16
	timestamp uint32
}
type Room struct {
	mu       sync.Mutex
	slots    [Capacity]slot
	api      *webrtc.API
	udp      ice.UDPMux
	tcp      ice.TCPMux
	closed   bool
	OnChange func()
	// OnMembership is called in membership order; callbacks must not reenter Room.
	OnMembership func(string, Member)
}

func NewRoom(c Config) (*Room, error) {
	r := &Room{}
	engine := webrtc.SettingEngine{}
	engine.SetIncludeLoopbackCandidate(true)
	engine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	networks := []webrtc.NetworkType{webrtc.NetworkTypeUDP4}
	if c.HostIP != "" {
		if net.ParseIP(c.HostIP) == nil {
			return nil, errors.New("VOICE_HOST_IP 必须是 IP 地址")
		}
		engine.SetNAT1To1IPs([]string{c.HostIP}, webrtc.ICECandidateTypeHost)
	}
	addr, err := net.ResolveUDPAddr("udp4", c.UDPAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, err
	}
	r.udp = webrtc.NewICEUDPMux(nil, conn)
	engine.SetICEUDPMux(r.udp)
	if c.TCPAddr != "" {
		listener, e := net.Listen("tcp4", c.TCPAddr)
		if e != nil {
			r.udp.Close()
			return nil, e
		}
		r.tcp = webrtc.NewICETCPMux(nil, listener, 8*1024)
		engine.SetICETCPMux(r.tcp)
		networks = append(networks, webrtc.NetworkTypeTCP4)
	}
	engine.SetNetworkTypes(networks)
	media := &webrtc.MediaEngine{}
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}
	if err = media.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: codec, PayloadType: 111}, webrtc.RTPCodecTypeAudio); err != nil {
		r.Close()
		return nil, err
	}
	if err = media.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: videoCodec(), PayloadType: 96}, webrtc.RTPCodecTypeVideo); err != nil {
		r.Close()
		return nil, err
	}
	registry := &interceptor.Registry{}
	if err = webrtc.RegisterDefaultInterceptors(media, registry); err != nil {
		r.Close()
		return nil, err
	}
	r.api = webrtc.NewAPI(webrtc.WithMediaEngine(media), webrtc.WithSettingEngine(engine), webrtc.WithInterceptorRegistry(registry))
	for i := range r.slots {
		track, e := webrtc.NewTrackLocalStaticRTP(codec, fmt.Sprintf("voice-%d", i), fmt.Sprintf("slot-%d", i))
		if e != nil {
			r.Close()
			return nil, e
		}
		r.slots[i].track = track
		r.slots[i].video, _ = webrtc.NewTrackLocalStaticRTP(videoCodec(), fmt.Sprintf("video-%d", i), fmt.Sprintf("slot-%d", i))
	}
	return r, nil
}

// NewSibling shares the ICE listeners and API, but owns independent audio tracks.
// Close siblings before closing the original room that owns the listeners.
func (r *Room) NewSibling() (*Room, error) {
	child := &Room{api: r.api}
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}
	for i := range child.slots {
		track, err := webrtc.NewTrackLocalStaticRTP(codec, fmt.Sprintf("voice-%d", i), fmt.Sprintf("slot-%d", i))
		if err != nil {
			return nil, err
		}
		child.slots[i].track = track
		child.slots[i].video, _ = webrtc.NewTrackLocalStaticRTP(videoCodec(), fmt.Sprintf("video-%d", i), fmt.Sprintf("slot-%d", i))
	}
	return child, nil
}
func (r *Room) connected(p *peer) {
	r.mu.Lock()
	if r.slots[p.Slot].peer == p && !p.announced {
		p.announced = true
		if r.OnMembership != nil {
			r.OnMembership("join", p.Member)
		}
	}
	r.mu.Unlock()
}

func (r *Room) Members() []Member {
	r.mu.Lock()
	defer r.mu.Unlock()
	members := []Member{}
	for _, s := range r.slots {
		if s.peer != nil {
			members = append(members, s.peer.Member)
		}
	}
	return members
}
func (r *Room) changed() {
	if r.OnChange != nil {
		r.OnChange()
	}
}

func (r *Room) Join(ctx context.Context, id, name, callID string, offer webrtc.SessionDescription) (answer webrtc.SessionDescription, err error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return answer, errors.New("语音服务已关闭")
	}
	index := -1
	for i, s := range r.slots {
		if s.peer != nil && s.peer.ID == id {
			r.mu.Unlock()
			return answer, ErrAlreadyJoined
		}
		if s.peer == nil && index < 0 {
			index = i
		}
	}
	if index < 0 {
		r.mu.Unlock()
		return answer, ErrFull
	}
	pc, e := r.api.NewPeerConnection(webrtc.Configuration{})
	if e != nil {
		r.mu.Unlock()
		return answer, e
	}
	p := &peer{Member: Member{ID: id, Nickname: name, Slot: index}, pc: pc, first: true, videoFirst: true, callID: callID}
	r.slots[index].peer = p
	r.mu.Unlock()
	defer func() {
		if err != nil {
			r.remove(p)
		}
	}()
	if offer.Type != webrtc.SDPTypeOffer {
		return answer, errors.New("需要有效的语音 offer")
	}
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateConnected:
			r.connected(p)
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			go r.remove(p)
		case webrtc.PeerConnectionStateDisconnected:
			time.AfterFunc(8*time.Second, func() {
				if pc.ConnectionState() == webrtc.PeerConnectionStateDisconnected {
					r.remove(p)
				}
			})
		}
	})
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		video := remote.Kind() == webrtc.RTPCodecTypeVideo
		if video {
			r.mu.Lock()
			p.videoSSRC = uint32(remote.SSRC())
			r.mu.Unlock()
			done := make(chan struct{})
			defer close(done)
			go func() {
				ticker := time.NewTicker(2 * time.Second)
				defer ticker.Stop()
				r.requestKeyframe(index)
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						r.requestKeyframe(index)
					}
				}
			}()
		}
		for {
			packet, _, readErr := remote.ReadRTP()
			if readErr != nil {
				return
			}
			if video {
				r.forwardVideo(p, packet)
			} else {
				r.forward(p, packet)
			}
		}
	})
	if err = pc.SetRemoteDescription(offer); err != nil {
		return answer, errors.New("无法解析语音协商信息")
	}
	// Only attach media kinds offered by this client; legacy audio-only clients remain valid.
	offered := map[webrtc.RTPCodecType]bool{}
	for _, transceiver := range pc.GetTransceivers() {
		offered[transceiver.Kind()] = true
	}
	for i := range r.slots {
		if i == index {
			continue
		}
		for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio, webrtc.RTPCodecTypeVideo} {
			if !offered[kind] {
				continue
			}
			track := r.slots[i].track
			if kind == webrtc.RTPCodecTypeVideo {
				track = r.slots[i].video
			}
			sender, addErr := pc.AddTrack(track)
			if addErr != nil {
				return answer, addErr
			}
			go func(slotIndex int, video bool) {
				for {
					packets, _, readErr := sender.ReadRTCP()
					if readErr != nil {
						return
					}
					if video {
						for _, packet := range packets {
							switch packet.(type) {
							case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
								r.requestKeyframe(slotIndex)
							}
						}
					}
				}
			}(i, kind == webrtc.RTPCodecTypeVideo)
		}
	}
	answer, err = pc.CreateAnswer(nil)
	if err != nil {
		return answer, err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		return answer, err
	}
	select {
	case <-ctx.Done():
		return answer, ctx.Err()
	case <-time.After(8 * time.Second):
		return answer, errors.New("语音地址收集超时")
	case <-gathered:
	}
	answer = *pc.LocalDescription()
	time.AfterFunc(20*time.Second, func() {
		if pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
			r.remove(p)
		}
	})
	r.changed()
	return answer, nil
}

func (r *Room) forward(p *peer, packet *rtp.Packet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := &r.slots[p.Slot]
	if s.peer != p {
		return
	}
	seq, ts := packet.SequenceNumber, packet.Timestamp
	if p.first {
		s.seq++
		s.timestamp += 960
		p.first = false
	} else {
		delta := uint16(seq - p.lastSeq)
		if delta == 0 || delta > 32768 {
			return
		} // Drop duplicates and late packets.
		s.seq += delta
		s.timestamp += ts - p.lastTS
	}
	p.lastSeq, p.lastTS = seq, ts
	// Header extensions belong to the publisher's negotiation, not subscribers'.
	packet.Header = rtp.Header{Version: 2, Marker: packet.Marker, PayloadType: packet.PayloadType, SequenceNumber: s.seq, Timestamp: s.timestamp, SSRC: packet.SSRC}
	packet.PaddingSize = 0
	_ = s.track.WriteRTP(packet)
}
func (r *Room) remove(p *peer) {
	r.mu.Lock()
	if r.slots[p.Slot].peer != p {
		r.mu.Unlock()
		return
	}
	r.slots[p.Slot].peer = nil
	if p.announced && r.OnMembership != nil {
		r.OnMembership("leave", p.Member)
	}
	r.mu.Unlock()
	_ = p.pc.Close()
	r.changed()
}
func (r *Room) Leave(id string) {
	r.mu.Lock()
	var found *peer
	for _, s := range r.slots {
		if s.peer != nil && s.peer.ID == id {
			found = s.peer
			break
		}
	}
	r.mu.Unlock()
	if found != nil {
		r.remove(found)
	}
}

// LeaveCall limits browser cleanup to the call that requested it. Session-wide
// Leave remains reserved for logout or loss of the last chat connection.
func (r *Room) LeaveCall(id, callID string) {
	r.mu.Lock()
	var found *peer
	for _, s := range r.slots {
		if s.peer != nil && s.peer.ID == id && s.peer.callID == callID {
			found = s.peer
			break
		}
	}
	r.mu.Unlock()
	if found != nil {
		r.remove(found)
	}
}
func (r *Room) Close() {
	r.mu.Lock()
	r.closed = true
	peers := []*peer{}
	for _, s := range r.slots {
		if s.peer != nil {
			peers = append(peers, s.peer)
		}
	}
	r.mu.Unlock()
	for _, p := range peers {
		r.remove(p)
	}
	if r.udp != nil {
		r.udp.Close()
	}
	if r.tcp != nil {
		r.tcp.Close()
	}
}

func videoCodec() webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "ccm", Parameter: "fir"}}}
}
func (r *Room) requestKeyframe(index int) {
	r.mu.Lock()
	p := r.slots[index].peer
	var ssrc uint32
	if p != nil {
		ssrc = p.videoSSRC
	}
	r.mu.Unlock()
	if p != nil && ssrc != 0 {
		_ = p.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ssrc}})
	}
}
func (r *Room) forwardVideo(p *peer, packet *rtp.Packet) {
	// RFC 7741 optional picture/temporal IDs are publisher-local. Strip them
	// instead of leaking resetting IDs into a long-lived subscriber slot.
	var vp8 codecs.VP8Packet
	payload, err := vp8.Unmarshal(packet.Payload)
	if err != nil {
		return
	}
	clean := make([]byte, 1+len(payload))
	clean[0] = packet.Payload[0] & 0x3f
	copy(clean[1:], payload)
	r.mu.Lock()
	defer r.mu.Unlock()
	s := &r.slots[p.Slot]
	if s.peer != p {
		return
	}
	seq, ts := packet.SequenceNumber, packet.Timestamp
	if p.videoFirst {
		p.videoSeqOffset = s.videoSeq + 1 - seq
		p.videoTSOffset = s.videoTS + 3000 - ts
		p.videoLastSeq = seq
		p.videoFirst = false
	}
	mappedSeq, mappedTS := seq+p.videoSeqOffset, ts+p.videoTSOffset
	// A publisher has a fixed mapping for its entire slot tenure. Reordered
	// packets and NACK repairs retain the missing packet's sequence/timestamp;
	// only newer packets advance the high water used by the next publisher.
	// Modular comparisons also handle ordinary RTP sequence/timestamp rollover.
	if int16(seq-p.videoLastSeq) >= 0 {
		p.videoLastSeq = seq
		s.videoSeq = mappedSeq
		if int32(mappedTS-s.videoTS) > 0 {
			s.videoTS = mappedTS
		}
	}
	packet.Header = rtp.Header{Version: 2, Marker: packet.Marker, SequenceNumber: mappedSeq, Timestamp: mappedTS, SSRC: packet.SSRC}
	packet.Payload = clean
	packet.PaddingSize = 0
	_ = s.video.WriteRTP(packet)
}
