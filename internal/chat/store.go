package chat

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

type Session struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	Token    string `json:"-"`
}

const PublicChannel = "public"

type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Message struct {
	ChannelID string  `json:"channel_id"`
	ID        int64   `json:"id"`
	ClientID  string  `json:"client_id"`
	SessionID string  `json:"session_id"`
	Nickname  string  `json:"nickname"`
	Kind      string  `json:"kind"`
	Text      string  `json:"text,omitempty"`
	Media     string  `json:"media,omitempty"`
	MIME      string  `json:"mime,omitempty"`
	Duration  float64 `json:"duration,omitempty"`
	CreatedAt string  `json:"created_at"`
}

type Store struct{ db *sql.DB }

func randomID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY, token TEXT UNIQUE NOT NULL, nickname TEXT NOT NULL, expires_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS messages(id INTEGER PRIMARY KEY AUTOINCREMENT, client_id TEXT NOT NULL, session_id TEXT NOT NULL, nickname TEXT NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL DEFAULT '', media TEXT NOT NULL DEFAULT '', mime TEXT NOT NULL DEFAULT '', duration REAL NOT NULL DEFAULT 0, created_at TEXT NOT NULL, UNIQUE(session_id, client_id));
 CREATE INDEX IF NOT EXISTS messages_media ON messages(media);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	// Migrate existing installations without rebuilding their messages or media.
	var columns int
	if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('messages') WHERE name='channel_id'").Scan(&columns); err == nil && columns == 0 {
		_, err = db.Exec("ALTER TABLE messages ADD COLUMN channel_id TEXT NOT NULL DEFAULT 'public'")
	}
	if err == nil {
		_, err = db.Exec(`CREATE TABLE IF NOT EXISTS channels(id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE);
   INSERT OR IGNORE INTO channels(id,name) VALUES('public','公共频道');
   CREATE INDEX IF NOT EXISTS messages_channel_id ON messages(channel_id,id);`)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) CreateSession(name string) (Session, error) {
	u := Session{ID: randomID(), Token: randomID(), Nickname: name}
	_, err := s.db.Exec("INSERT INTO sessions(id,token,nickname,expires_at) VALUES(?,?,?,?)", u.ID, u.Token, name, time.Now().Add(30*24*time.Hour).Unix())
	return u, err
}
func (s *Store) Session(token string) (Session, error) {
	var u Session
	err := s.db.QueryRow("SELECT id,token,nickname FROM sessions WHERE token=? AND expires_at>?", token, time.Now().Unix()).Scan(&u.ID, &u.Token, &u.Nickname)
	return u, err
}
func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE id=?", id)
	return err
}

const messageColumns = "channel_id,id,client_id,session_id,nickname,kind,text,media,mime,duration,created_at"

type scanner interface{ Scan(...any) error }

func scanMessage(row scanner) (Message, error) {
	var m Message
	err := row.Scan(&m.ChannelID, &m.ID, &m.ClientID, &m.SessionID, &m.Nickname, &m.Kind, &m.Text, &m.Media, &m.MIME, &m.Duration, &m.CreatedAt)
	return m, err
}
func (s *Store) SaveMessage(m Message) (Message, error) {
	saved, _, err := s.saveMessage(m)
	return saved, err
}
func (s *Store) saveMessage(m Message) (Message, bool, error) {
	if m.ChannelID == "" {
		m.ChannelID = PublicChannel
	}
	result, err := s.db.Exec(`INSERT INTO messages(channel_id,client_id,session_id,nickname,kind,text,media,mime,duration,created_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(session_id,client_id) DO NOTHING`, m.ChannelID, m.ClientID, m.SessionID, m.Nickname, m.Kind, m.Text, m.Media, m.MIME, m.Duration, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return Message{}, false, err
	}
	saved, err := scanMessage(s.db.QueryRow("SELECT "+messageColumns+" FROM messages WHERE session_id=? AND client_id=?", m.SessionID, m.ClientID))
	if err == nil && saved.ChannelID != m.ChannelID {
		return Message{}, false, errors.New("消息 ID 已在其他频道使用")
	}
	inserted, _ := result.RowsAffected()
	return saved, inserted == 1, err
}
func (s *Store) MediaMessage(name string) (Message, error) {
	return scanMessage(s.db.QueryRow("SELECT "+messageColumns+" FROM messages WHERE kind IN ('audio','photo') AND media=?", name))
}

// Messages returns chronological pages. before=0 gets latest; after>0 catches up oldest first.
func (s *Store) Messages(before, after int64, limit int) ([]Message, error) {
	return s.messages(PublicChannel, before, after, limit, after > 0)
}

// MessagesAfter includes the zero cursor: an empty-room reconnect must start at
// the oldest message, rather than accidentally receiving only the latest page.
func (s *Store) MessagesAfter(after int64, limit int) ([]Message, error) {
	return s.messages(PublicChannel, 0, after, limit, true)
}

func (s *Store) messages(channel string, before, after int64, limit int, catchUp bool) ([]Message, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	query := "SELECT " + messageColumns + " FROM messages WHERE channel_id=?"
	args := []any{channel}
	if catchUp {
		query += " AND id>?"
		args = append(args, after)
	} else if before > 0 {
		query += " AND id<?"
		args = append(args, before)
	}
	if catchUp {
		query += " ORDER BY id ASC"
	} else {
		query += " ORDER BY id DESC"
	}
	query += " LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Message{}
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, m)
	}
	if !catchUp {
		for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
			result[i], result[j] = result[j], result[i]
		}
	}
	return result, rows.Err()
}

func (s *Store) Channels() ([]Channel, error) {
	rows, err := s.db.Query("SELECT id,name FROM channels ORDER BY id='public' DESC, rowid ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Channel{}
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *Store) HasChannel(id string) bool {
	var found string
	return s.db.QueryRow("SELECT id FROM channels WHERE id=?", id).Scan(&found) == nil
}
func (s *Store) CreateChannel(name string) (Channel, error) {
	c := Channel{ID: randomID(), Name: name}
	_, err := s.db.Exec("INSERT INTO channels(id,name) VALUES(?,?)", c.ID, c.Name)
	return c, err
}
