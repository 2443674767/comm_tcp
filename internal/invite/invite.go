// Package invite tracks pending "pull into voice" invitations in memory.
// Invitations are short-lived and never persisted.
package invite

import (
	"sync"
	"time"
)

// User identifies a chat session without exposing its credential.
type User struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
}

// Notify delivers one event to every connection a user has, in whichever
// channel they are: people can be pulled in from any channel. It is called
// with the manager locked so events for one invitation stay ordered; it must
// not call back into the manager.
type Notify func(userID string, event map[string]any)

type pending struct {
	channel string
	from    User
	targets map[string]User
	timer   *time.Timer
}

type Manager struct {
	mu      sync.Mutex
	ttl     time.Duration
	notify  Notify
	invites map[string]*pending
	closed  bool
}

func New(ttl time.Duration, notify Notify) *Manager {
	return &Manager{ttl: ttl, notify: notify, invites: make(map[string]*pending)}
}

// Create rings every target the inviter is not already ringing for the
// channel's voice room and returns the users that were rung. ok is false when id is already in use.
func (m *Manager) Create(id, channel string, from User, targets []User) (rung []User, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.invites[id] != nil {
		return nil, false
	}
	ringing := map[string]bool{from.ID: true}
	for _, p := range m.invites {
		if p.channel == channel && p.from.ID == from.ID {
			for target := range p.targets {
				ringing[target] = true
			}
		}
	}
	p := &pending{channel: channel, from: from, targets: make(map[string]User)}
	rung = []User{}
	for _, target := range targets {
		if ringing[target.ID] {
			continue
		}
		ringing[target.ID] = true
		p.targets[target.ID] = target
		rung = append(rung, target)
	}
	if len(rung) == 0 {
		return rung, true
	}
	m.invites[id] = p
	p.timer = time.AfterFunc(m.ttl, func() { m.expire(id, p) })
	for _, target := range rung {
		m.notify(target.ID, map[string]any{"type": "voice_invite", "invite_id": id, "channel_id": channel, "from": from, "expires_in": int(m.ttl / time.Second)})
	}
	return rung, true
}

// finish settles one target: the inviter learns the outcome (unless action is
// empty) and every tab of the target stops ringing. Callers hold m.mu.
func (m *Manager) finish(id string, p *pending, target User, action, reason string) {
	delete(p.targets, target.ID)
	if action != "" {
		m.notify(p.from.ID, map[string]any{"type": "voice_invite_reply", "invite_id": id, "user": target, "action": action})
	}
	m.notify(target.ID, map[string]any{"type": "voice_invite_end", "invite_id": id, "reason": reason})
	if len(p.targets) == 0 {
		p.timer.Stop()
		delete(m.invites, id)
	}
}

// Reply records a target's own answer. It reports false when the invitation
// no longer waits for that user.
func (m *Manager) Reply(id, userID, action string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.invites[id]
	if p == nil {
		return false
	}
	target, ok := p.targets[userID]
	if !ok {
		return false
	}
	m.finish(id, p, target, action, action)
	return true
}

func (m *Manager) expire(id string, p *pending) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.invites[id] != p {
		return
	}
	for _, target := range p.targets {
		m.finish(id, p, target, "timeout", "timeout")
	}
}

// Sync reconciles a channel's invitations with its current voice members:
// an inviter who left voice cancels the ringing, and a target who is now in
// voice has accepted, however they joined.
func (m *Manager) Sync(channel string, inVoice map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.invites {
		if p.channel != channel {
			continue
		}
		for _, target := range p.targets {
			if !inVoice[p.from.ID] {
				m.finish(id, p, target, "", "cancelled")
			} else if inVoice[target.ID] {
				m.finish(id, p, target, "accept", "accept")
			}
		}
	}
}

// Offline settles invitations waiting for a user who has no connection left
// in any channel. An inviter going offline also leaves voice, which Sync sees.
func (m *Manager) Offline(userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.invites {
		if target, ok := p.targets[userID]; ok {
			m.finish(id, p, target, "offline", "offline")
		}
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for id, p := range m.invites {
		p.timer.Stop()
		delete(m.invites, id)
	}
}
