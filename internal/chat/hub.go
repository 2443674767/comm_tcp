package chat

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type client struct {
	conn    *websocket.Conn
	user    Session
	channel string
	send    chan []byte
	done    chan struct{}
	once    sync.Once
}

func (c *client) close() { c.once.Do(func() { close(c.done); c.conn.Close() }) }

type hub struct {
	mu        sync.Mutex
	clients   map[*client]bool
	onOffline func(string, string)
	closed    bool
}

func newHub() *hub                 { return &hub{clients: make(map[*client]bool)} }
func (h *hub) broadcast(event any) { h.broadcastChannel("", event) }
func (h *hub) broadcastChannel(channel string, event any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcastLocked(channel, event)
}
func (h *hub) broadcastLocked(channel string, event any) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	for c := range h.clients {
		if channel != "" && c.channel != channel {
			continue
		}
		select {
		case c.send <- data:
		default:
			c.close()
		}
	}
}
func (h *hub) usersLocked(channel string) []Session {
	seen := map[string]bool{}
	users := []Session{}
	for c := range h.clients {
		if c.channel == channel && !seen[c.user.ID] {
			seen[c.user.ID] = true
			users = append(users, c.user)
		}
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	return users
}

// onlineUser is one online session and a channel it is currently viewing.
type onlineUser struct {
	ID        string `json:"id"`
	Nickname  string `json:"nickname"`
	ChannelID string `json:"channel_id"`
}

// allUsersLocked lists every online session once, across all channels.
func (h *hub) allUsersLocked() []onlineUser {
	byID := map[string]onlineUser{}
	for c := range h.clients {
		if seen, ok := byID[c.user.ID]; !ok || c.channel < seen.ChannelID {
			byID[c.user.ID] = onlineUser{ID: c.user.ID, Nickname: c.user.Nickname, ChannelID: c.channel}
		}
	}
	users := make([]onlineUser, 0, len(byID))
	for _, user := range byID {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	return users
}

// presence refreshes the channel's own list and everyone's cross-channel list.
func (h *hub) presence(channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcastLocked(channel, map[string]any{"type": "presence", "users": h.usersLocked(channel)})
	h.broadcastLocked("", map[string]any{"type": "online", "users": h.allUsersLocked()})
}
func (h *hub) online(channel, id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.channel == channel && c.user.ID == id {
			return true
		}
	}
	return false
}

// user finds an online session in any channel.
func (h *hub) user(id string) (Session, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.user.ID == id {
			return c.user, true
		}
	}
	return Session{}, false
}

// sendUser delivers an event to every connection one user has, in any channel.
func (h *hub) sendUser(id string, event any) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.user.ID != id {
			continue
		}
		select {
		case c.send <- data:
		default:
			c.close()
		}
	}
}
func (h *hub) remove(c *client) {
	c.close()
	h.mu.Lock()
	delete(h.clients, c)
	last := true
	for other := range h.clients {
		if other.channel == c.channel && other.user.ID == c.user.ID {
			last = false
			break
		}
	}
	h.mu.Unlock()
	if last && h.onOffline != nil {
		h.onOffline(c.channel, c.user.ID)
	}
	h.presence(c.channel)
}
func (h *hub) disconnect(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.user.ID == id {
			c.close()
		}
	}
}
func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for c := range h.clients {
		c.close()
	}
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request, u Session) {
	upgrader := websocket.Upgrader{CheckOrigin: sameOrigin, HandshakeTimeout: 5 * time.Second}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{conn: conn, user: u, channel: requestChannel(r), send: make(chan []byte, 128), done: make(chan struct{})}
	s.hub.mu.Lock()
	if s.hub.closed {
		s.hub.mu.Unlock()
		c.close()
		return
	}
	s.hub.clients[c] = true
	s.hub.mu.Unlock()
	defer s.hub.remove(c)
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		defer c.close()
		for {
			select {
			case <-c.done:
				return
			case data := <-c.send:
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if conn.WriteMessage(websocket.TextMessage, data) != nil {
					return
				}
			case <-ticker.C:
				if _, err := s.store.Session(u.Token); err != nil {
					return
				}
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if conn.WriteMessage(websocket.PingMessage, nil) != nil {
					return
				}
			}
		}
	}()
	ready, _ := json.Marshal(map[string]any{"type": "ready"})
	c.send <- ready
	s.hub.presence(c.channel)
	s.broadcastVoice(c.channel)
	conn.SetReadLimit(4096)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
