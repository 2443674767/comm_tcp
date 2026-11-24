package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pion/webrtc/v4"
	"lanchat/internal/invite"
	"lanchat/internal/voice"
)

const MaxAudioBytes = 10 * 1024 * 1024
const cookieName = "lan_chat_session"

// A voice invitation rings for this long before it counts as unanswered.
const inviteTTL = 30 * time.Second
const maxInviteTargets = 20

// Switching channel or reconnecting briefly leaves a session with no
// connection; only a longer absence counts as offline for invitations.
const inviteOfflineGrace = 5 * time.Second

type Server struct {
	publishMu      sync.Mutex
	voicePublishMu sync.Mutex
	roomsMu        sync.Mutex
	rooms          map[string]*voice.Room
	store          *Store
	mediaDir       string
	room           *voice.Room
	hub            *hub
	invites        *invite.Manager
	mux            *http.ServeMux
}

func NewServer(store *Store, mediaDir string, room *voice.Room, webFS fs.FS) *Server {
	s := &Server{store: store, mediaDir: mediaDir, room: room, rooms: make(map[string]*voice.Room), hub: newHub(), mux: http.NewServeMux()}
	s.invites = invite.New(inviteTTL, func(userID string, event map[string]any) { s.hub.sendUser(userID, event) })
	if room != nil {
		s.configureRoom(PublicChannel, room)
	}
	s.hub.onOffline = func(channel, id string) {
		if room := s.channelRoom(channel); room != nil {
			room.Leave(id)
		}
		time.AfterFunc(inviteOfflineGrace, func() {
			if _, online := s.hub.user(id); !online {
				s.invites.Offline(id)
			}
		})
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := store.db.PingContext(r.Context()); err != nil {
			fail(w, 503, "数据库不可用")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("POST /api/login", s.login)
	s.mux.HandleFunc("GET /api/me", s.auth(func(w http.ResponseWriter, r *http.Request, u Session) { writeJSON(w, 200, u) }))
	s.mux.HandleFunc("POST /api/logout", s.auth(s.logout))
	s.mux.HandleFunc("GET /api/channels", s.auth(s.channels))
	s.mux.HandleFunc("POST /api/channels", s.auth(s.createChannel))
	s.mux.HandleFunc("GET /api/messages", s.channelAuth(s.history))
	s.mux.HandleFunc("POST /api/messages", s.channelAuth(s.sendText))
	s.mux.HandleFunc("POST /api/audio", s.channelAuth(s.sendAudio))
	s.mux.HandleFunc("POST /api/photos", s.channelAuth(s.sendPhoto))
	s.mux.HandleFunc("GET /media/{name}", s.channelAuth(s.media))
	s.mux.HandleFunc("GET /ws", s.channelAuth(s.websocket))
	s.mux.HandleFunc("GET /api/voice", s.channelAuth(func(w http.ResponseWriter, r *http.Request, u Session) {
		writeJSON(w, 200, s.voiceMembers(requestChannel(r)))
	}))
	s.mux.HandleFunc("POST /api/voice", s.channelAuth(s.joinVoice))
	s.mux.HandleFunc("POST /api/voice-support", s.auth(s.voiceSupport))
	s.mux.HandleFunc("DELETE /api/voice", s.channelAuth(func(w http.ResponseWriter, r *http.Request, u Session) {
		if room := s.channelRoom(requestChannel(r)); room != nil {
			room.LeaveCall(u.ID, r.URL.Query().Get("call_id"))
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	s.mux.HandleFunc("POST /api/voice-invite", s.channelAuth(s.inviteVoice))
	s.mux.HandleFunc("POST /api/voice-invite/reply", s.channelAuth(s.replyVoiceInvite))
	s.mux.Handle("/", http.FileServer(http.FS(webFS)))
	return s
}
func (s *Server) Close() {
	s.invites.Close()
	s.hub.close()
	s.roomsMu.Lock()
	rooms := make([]*voice.Room, 0, len(s.rooms))
	for _, room := range s.rooms {
		if room != s.room {
			rooms = append(rooms, room)
		}
	}
	s.roomsMu.Unlock()
	for _, room := range rooms {
		room.Close()
	}
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Permissions-Policy", "microphone=(self), camera=(self)")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; media-src 'self' blob:; img-src 'self' data: blob:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-store")
	if !sameOrigin(r) {
		fail(w, 403, "不允许跨站请求")
		return
	}
	s.mux.ServeHTTP(w, r)
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme && u.Host == r.Host && u.User == nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		fail(w, 415, "需要 JSON 请求")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, "请求内容无效或过长")
		return false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		fail(w, 400, "请求包含多余数据")
		return false
	}
	return true
}
func (s *Server) auth(next func(http.ResponseWriter, *http.Request, Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			fail(w, 401, "请先输入昵称登录")
			return
		}
		u, err := s.store.Session(cookie.Value)
		if err != nil {
			fail(w, 401, "登录已过期，请重新登录")
			return
		}
		next(w, r, u)
	}
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Nickname string `json:"nickname"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Nickname)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 24 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		fail(w, 400, "昵称需为 1–24 个字符，不能包含控制字符")
		return
	}
	// Reuse a live session to avoid orphaning another tab when login is retried.
	if c, err := r.Cookie(cookieName); err == nil {
		if u, err := s.store.Session(c.Value); err == nil {
			writeJSON(w, 200, u)
			return
		}
	}
	u, err := s.store.CreateSession(name)
	if err != nil {
		s.internalError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: u.Token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 60 * 60})
	writeJSON(w, 200, u)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request, u Session) {
	if err := s.store.DeleteSession(u.ID); err != nil {
		s.internalError(w, err)
		return
	}
	s.hub.disconnect(u.ID)
	s.roomsMu.Lock()
	rooms := make([]*voice.Room, 0, len(s.rooms))
	for _, room := range s.rooms {
		rooms = append(rooms, room)
	}
	s.roomsMu.Unlock()
	for _, room := range rooms {
		room.Leave(u.ID)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) internalError(w http.ResponseWriter, err error) {
	log.Printf("request failed: %v", err)
	fail(w, 500, "服务器处理失败，请重试")
}
func (s *Server) history(w http.ResponseWriter, r *http.Request, u Session) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	var messages []Message
	var err error
	if r.URL.Query().Has("after") {
		messages, err = s.store.messages(requestChannel(r), 0, after, 50, true)
	} else {
		messages, err = s.store.messages(requestChannel(r), before, 0, 50, false)
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, messages)
}
func validClientID(id string) bool {
	return len(id) > 0 && len(id) <= 80 && strings.IndexFunc(id, func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') }) < 0
}
func (s *Server) sendText(w http.ResponseWriter, r *http.Request, u Session) {
	var input struct {
		ClientID string `json:"client_id"`
		Text     string `json:"text"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	text := strings.TrimSpace(input.Text)
	if !validClientID(input.ClientID) || !utf8.ValidString(text) || text == "" || utf8.RuneCountInString(text) > 4000 {
		fail(w, 400, "消息不能为空，最多 4000 个字符；需提供有效消息 ID")
		return
	}
	m, err := s.persistMessage(Message{ChannelID: requestChannel(r), ClientID: input.ClientID, SessionID: u.ID, Nickname: u.Nickname, Kind: "text", Text: text})
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, m)
}
func (s *Server) persistMessage(message Message) (Message, error) {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	m, inserted, err := s.store.saveMessage(message)
	if err == nil && inserted {
		s.broadcastMessage(m)
	}
	return m, err
}
func (s *Server) broadcastMessage(m Message) {
	s.hub.broadcastChannel(m.ChannelID, map[string]any{"type": "message", "message": m})
}

func audioType(header []byte, contentType string) (string, string, bool) {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", "", false
	}
	switch {
	case mt == "audio/webm" && bytes.HasPrefix(header, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return "audio/webm", ".webm", true
	case mt == "audio/ogg" && bytes.HasPrefix(header, []byte("OggS")):
		return "audio/ogg", ".ogg", true
	case (mt == "audio/mp4" || mt == "audio/x-m4a") && len(header) >= 12 && string(header[4:8]) == "ftyp":
		return "audio/mp4", ".m4a", true
	}
	return "", "", false
}
func (s *Server) sendAudio(w http.ResponseWriter, r *http.Request, u Session) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxAudioBytes+64*1024)
	if err := r.ParseMultipartForm(1024 * 1024); err != nil {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
		fail(w, 400, "录音上传失败，文件最大 10 MiB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	clientID := r.FormValue("client_id")
	duration, err := strconv.ParseFloat(r.FormValue("duration"), 64)
	if !validClientID(clientID) || err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 120 {
		fail(w, 400, "录音时长需为 0–120 秒，且需提供有效消息 ID")
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		fail(w, 400, "缺少录音文件")
		return
	}
	defer file.Close()
	if header.Size < 1 || header.Size > MaxAudioBytes {
		fail(w, 400, "录音最大 10 MiB")
		return
	}
	prefix := make([]byte, 512)
	n, _ := file.Read(prefix)
	prefix = prefix[:n]
	mt, ext, ok := audioType(prefix, header.Header.Get("Content-Type"))
	if !ok {
		fail(w, 400, "不支持的录音格式，请使用 WebM、Ogg 或 MP4 音频")
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		s.internalError(w, err)
		return
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
	_, copyErr := io.Copy(out, file)
	syncErr := out.Sync()
	closeErr := out.Close()
	if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
		s.internalError(w, err)
		return
	}
	m, err := s.persistMessage(Message{ChannelID: requestChannel(r), ClientID: clientID, SessionID: u.ID, Nickname: u.Nickname, Kind: "audio", Media: name, MIME: mt, Duration: duration})
	if err != nil {
		s.internalError(w, err)
		return
	}
	keep = m.Media == name
	writeJSON(w, 200, m)
}
func (s *Server) media(w http.ResponseWriter, r *http.Request, u Session) {
	name := r.PathValue("name")
	if name != filepath.Base(name) {
		http.NotFound(w, r)
		return
	}
	m, err := s.store.MediaMessage(name)
	if err != nil || m.ChannelID != requestChannel(r) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", m.MIME)
	http.ServeFile(w, r, filepath.Join(s.mediaDir, name))
}
func (s *Server) voiceMembers(channel string) []voice.Member {
	if room := s.channelRoom(channel); room != nil {
		return room.Members()
	}
	return []voice.Member{}
}
func (s *Server) broadcastVoice(channel string) {
	s.voicePublishMu.Lock()
	defer s.voicePublishMu.Unlock()
	s.hub.broadcastChannel(channel, map[string]any{"type": "voice", "members": s.voiceMembers(channel)})
}
func (s *Server) configureRoom(channel string, room *voice.Room) {
	s.rooms[channel] = room
	room.OnChange = func() {
		s.broadcastVoice(channel)
		// Joining voice answers an invitation; an inviter who left stops the ringing.
		inVoice := map[string]bool{}
		for _, member := range room.Members() {
			inVoice[member.ID] = true
		}
		s.invites.Sync(channel, inVoice)
	}
	room.OnMembership = func(action string, member voice.Member) {
		s.hub.broadcastChannel(channel, map[string]any{"type": "voice_event", "action": action, "member": member})
	}
}
func (s *Server) channelRoom(channel string) *voice.Room {
	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()
	if s.room == nil {
		return nil
	}
	if room := s.rooms[channel]; room != nil {
		return room
	}
	room, err := s.room.NewSibling()
	if err != nil {
		return nil
	}
	s.configureRoom(channel, room)
	return room
}
func (s *Server) joinVoice(w http.ResponseWriter, r *http.Request, u Session) {
	channel := requestChannel(r)
	room := s.channelRoom(channel)
	if room == nil {
		fail(w, 503, "语音服务未启用")
		return
	}
	if !s.hub.online(channel, u.ID) {
		fail(w, 409, "聊天连接尚未就绪，请稍后重试")
		return
	}
	var input struct {
		CallID string                    `json:"call_id"`
		Offer  webrtc.SessionDescription `json:"offer"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validClientID(input.CallID) {
		fail(w, 400, "缺少有效通话 ID")
		return
	}
	answer, err := room.Join(r.Context(), u.ID, u.Nickname, input.CallID, input.Offer)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !s.hub.online(channel, u.ID) {
		room.LeaveCall(u.ID, input.CallID)
		fail(w, 409, "连接已断开，请重新加入语音")
		return
	}
	writeJSON(w, 200, answer)
}

// inviteVoice rings the chosen online users, whichever channel they are in,
// to join the voice room of the caller's channel. The caller must already be
// in that room, so an invitation always leads to a call somebody is in.
func (s *Server) inviteVoice(w http.ResponseWriter, r *http.Request, u Session) {
	channel := requestChannel(r)
	room := s.channelRoom(channel)
	if room == nil {
		fail(w, 503, "语音服务未启用")
		return
	}
	var input struct {
		InviteID string   `json:"invite_id"`
		Targets  []string `json:"targets"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validClientID(input.InviteID) || len(input.Targets) < 1 || len(input.Targets) > maxInviteTargets {
		fail(w, 400, "请选择 1–20 位要邀请的伙伴，并提供有效邀请 ID")
		return
	}
	members := room.Members()
	inVoice := map[string]bool{}
	for _, member := range members {
		inVoice[member.ID] = true
	}
	if !inVoice[u.ID] {
		fail(w, 409, "请先加入群语音，再邀请其他人")
		return
	}
	if len(members) >= voice.Capacity {
		fail(w, 409, "群语音人数已满，暂时无法邀请")
		return
	}
	type skip struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	targets, skipped, seen := []invite.User{}, []skip{}, map[string]bool{u.ID: true}
	for _, id := range input.Targets {
		if seen[id] {
			continue
		}
		seen[id] = true
		target, online := s.hub.user(id)
		switch {
		case !online:
			skipped = append(skipped, skip{id, "offline"})
		case inVoice[id]:
			skipped = append(skipped, skip{id, "in_voice"})
		default:
			targets = append(targets, invite.User{ID: target.ID, Nickname: target.Nickname})
		}
	}
	rung, ok := s.invites.Create(input.InviteID, channel, invite.User{ID: u.ID, Nickname: u.Nickname}, targets)
	if !ok {
		fail(w, 409, "这次邀请已经发出")
		return
	}
	ringing := map[string]bool{}
	for _, target := range rung {
		ringing[target.ID] = true
	}
	for _, target := range targets {
		if !ringing[target.ID] {
			skipped = append(skipped, skip{target.ID, "ringing"})
		}
	}
	writeJSON(w, 200, map[string]any{"invited": rung, "skipped": skipped})
}

// replyVoiceInvite records an invited user's own answer. Accepting needs no
// call here: joining the voice room is the acceptance.
func (s *Server) replyVoiceInvite(w http.ResponseWriter, r *http.Request, u Session) {
	var input struct {
		InviteID string `json:"invite_id"`
		Action   string `json:"action"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validClientID(input.InviteID) || (input.Action != "decline" && input.Action != "failed") {
		fail(w, 400, "无效的邀请应答")
		return
	}
	if !s.invites.Reply(input.InviteID, u.ID, input.Action) {
		fail(w, 410, "邀请已失效")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func requestChannel(r *http.Request) string {
	id := r.URL.Query().Get("channel_id")
	if id == "" {
		return PublicChannel
	}
	return id
}
func (s *Server) channelAuth(next func(http.ResponseWriter, *http.Request, Session)) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request, u Session) {
		if !s.store.HasChannel(requestChannel(r)) {
			fail(w, 404, "频道不存在")
			return
		}
		next(w, r, u)
	})
}
func (s *Server) channels(w http.ResponseWriter, r *http.Request, u Session) {
	channels, err := s.store.Channels()
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, channels)
}
func (s *Server) createChannel(w http.ResponseWriter, r *http.Request, u Session) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 24 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		fail(w, 400, "频道名称需为 1–24 个字符，不能包含控制字符")
		return
	}
	channel, err := s.store.CreateChannel(name)
	if err != nil {
		list, listErr := s.store.Channels()
		if listErr == nil {
			for _, c := range list {
				if c.Name == name {
					fail(w, 409, "已有同名频道，请换一个名称")
					return
				}
			}
		}
		s.internalError(w, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "channels_changed"})
	writeJSON(w, 201, channel)
}

// Voice diagnostics contain capability flags only, never chat content or credentials.
func (s *Server) voiceSupport(w http.ResponseWriter, r *http.Request, u Session) {
	var input struct {
		Stage       string `json:"stage"`
		Build       string `json:"build"`
		Secure      bool   `json:"secure"`
		Global      string `json:"rtc_global"`
		Window      string `json:"rtc_window"`
		Webkit      string `json:"rtc_webkit"`
		Moz         string `json:"rtc_moz"`
		Transceiver bool   `json:"transceiver"`
		Microphone  bool   `json:"microphone"`
		AudioState  string `json:"audio_state"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if (input.Stage != "before-join" && input.Stage != "after-microphone" && input.Stage != "join-error") || len(input.Build) > 64 || len(input.AudioState) > 24 {
		fail(w, 400, "无效的语音诊断")
		return
	}
	for _, value := range []string{input.Global, input.Window, input.Webkit, input.Moz} {
		if value != "undefined" && value != "function" && value != "object" {
			fail(w, 400, "无效的语音诊断")
			return
		}
	}
	agent := r.UserAgent()
	if len(agent) > 512 {
		agent = agent[:512]
	}
	data, _ := json.Marshal(input)
	log.Printf("voice-support browser=%q capabilities=%s", agent, data)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
