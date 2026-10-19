// Package store keeps games, rounds and chat in Postgres and answers the
// leaderboard queries.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// ErrNotFound means there is no such row.
var ErrNotFound = errors.New("not found")

// Store is a Postgres-backed store.
type Store struct{ pool *pgxpool.Pool }

// Open connects and checks the connection.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database unreachable: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Ping reports whether the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// migrationLock is an arbitrary number that identifies our migrations to
// Postgres' advisory locks, so two servers starting together don't both run
// them.
const migrationLock = 7272_0001

// Migrate applies any migrations that haven't run yet, in order. It is safe
// to call on every start and from several processes at once.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLock); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLock) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var done bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// --- writing ---

// Player is who someone is, as start.ng tells us.
type Player struct {
	ID    string
	Name  string
	Style json.RawMessage
}

// UpsertPlayer saves or refreshes a player's name and look.
func (s *Store) UpsertPlayer(ctx context.Context, p Player) error {
	var style any
	if len(p.Style) > 0 && string(p.Style) != "null" {
		style = []byte(p.Style)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO players (id, name, style) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, style = EXCLUDED.style, last_seen = now()`,
		p.ID, p.Name, style)
	return err
}

// StartGame records a game and its players. Teams[i] is team i.
func (s *Store) StartGame(ctx context.Context, room string, at time.Time, teams [][2]string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO games (room, started_at) VALUES ($1, $2) RETURNING id`, room, at).Scan(&id); err != nil {
		return 0, err
	}
	for team, pair := range teams {
		for _, player := range pair {
			if _, err := tx.Exec(ctx, `INSERT INTO game_players (game_id, player_id, team) VALUES ($1, $2, $3)`, id, player, team); err != nil {
				return 0, err
			}
		}
	}
	return id, tx.Commit(ctx)
}

// RecordRound saves a won round. Saving the same round twice is harmless, so
// a retry can't double count.
func (s *Store) RecordRound(ctx context.Context, gameID int64, round, team int, proclaimer, holder string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO rounds (game_id, round, team, proclaimer, holder, won_at) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (game_id, round) DO NOTHING`, gameID, round, team, proclaimer, holder, at)
	return err
}

// Outcome is how a finished game ended.
type Outcome struct {
	Reason         string // "wins", "agreement" or "forfeit"
	WinnerTeam     int    // ignored when Draw
	Draw           bool
	CountsForStats bool
}

func (s *Store) FinishGame(ctx context.Context, gameID int64, at time.Time, o Outcome) error {
	var winner *int
	if !o.Draw {
		winner = &o.WinnerTeam
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE games SET status = 'finished', ended_at = $2, end_reason = $3, winner_team = $4, draw = $5, counts_for_stats = $6
		WHERE id = $1`, gameID, at, o.Reason, winner, o.Draw, o.CountsForStats)
	return err
}

func (s *Store) AbandonGame(ctx context.Context, gameID int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE games SET status = 'abandoned', ended_at = $2 WHERE id = $1 AND status = 'playing'`, gameID, at)
	return err
}

// AbandonUnfinished marks games still "playing" as abandoned. Rooms live in
// memory, so any game still open when the server starts is one that was cut
// off by a restart. This is only correct while a single server runs.
func (s *Store) AbandonUnfinished(ctx context.Context, at time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE games SET status = 'abandoned', ended_at = $1 WHERE status = 'playing'`, at)
	return tag.RowsAffected(), err
}

// --- chat ---

// AddChat saves a chat line as message number seq of its game. Saving the
// same number twice is harmless, so a retry can't duplicate a line.
func (s *Store) AddChat(ctx context.Context, gameID int64, seq int, player, text string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_messages (game_id, seq, player_id, body, created_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (game_id, seq) DO NOTHING`, gameID, seq, player, text, at)
	return err
}

// RoomGame is the most recent game played in a room.
func (s *Store) RoomGame(ctx context.Context, room string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM games WHERE room = $1 ORDER BY id DESC LIMIT 1`, room).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// SweepChat deletes chat older than the cutoff.
func (s *Store) SweepChat(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM chat_messages WHERE created_at < $1`, olderThan)
	return tag.RowsAffected(), err
}

// RunChatSweeper deletes expired chat every interval until ctx ends.
func (s *Store) RunChatSweeper(ctx context.Context, every, retention time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n, err := s.SweepChat(ctx, now.Add(-retention)); err != nil {
				log.Error("chat sweep failed", "err", err)
			} else if n > 0 {
				log.Info("chat swept", "deleted", n)
			}
		}
	}
}

// --- reading ---

// PlayerRef is a player as a page shows them.
type PlayerRef struct {
	ID    string
	Name  string
	Style json.RawMessage
}

func ref(id string, name *string, style []byte) PlayerRef {
	r := PlayerRef{ID: id, Name: id, Style: style}
	if name != nil {
		r.Name = *name
	}
	return r
}

// Role is what a player did to win a round.
type Role string

const (
	RoleProclaimer Role = "proclaimer" // shouted Jackpot for their partner
	RoleHolder     Role = "holder"     // held the four of a kind
)

type MVPEntry struct {
	Rank   int
	Player PlayerRef
	Wins   int
}

// TopPlayers ranks players by rounds won in a role. Players on equal wins
// share a rank.
func (s *Store) TopPlayers(ctx context.Context, role Role, limit int) ([]MVPEntry, error) {
	var col string
	switch role {
	case RoleProclaimer:
		col = "proclaimer"
	case RoleHolder:
		col = "holder"
	default:
		return nil, fmt.Errorf("unknown role %q", role)
	}
	// col is one of two constants above, never user input.
	rows, err := s.pool.Query(ctx, `
		SELECT r.`+col+`, p.name, p.style, COUNT(*) AS wins
		FROM rounds r LEFT JOIN players p ON p.id = r.`+col+`
		GROUP BY r.`+col+`, p.name, p.style
		ORDER BY wins DESC, r.`+col+` ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MVPEntry
	for rows.Next() {
		var id string
		var name *string
		var style []byte
		var wins int
		if err := rows.Scan(&id, &name, &style, &wins); err != nil {
			return nil, err
		}
		e := MVPEntry{Player: ref(id, name, style), Wins: wins}
		e.Rank = len(out) + 1
		if n := len(out); n > 0 && out[n-1].Wins == wins {
			e.Rank = out[n-1].Rank
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type DuoEntry struct {
	Rank    int
	Players [2]PlayerRef
	Wins    int // games won together
	Games   int // games played together
}

// TopDuos ranks pairs of partners by games won together. Forfeits, draws and
// abandoned games don't count.
func (s *Store) TopDuos(ctx context.Context, limit int) ([]DuoEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.player_id, pa.name, pa.style, b.player_id, pb.name, pb.style,
		       COUNT(*) FILTER (WHERE g.winner_team = a.team) AS wins,
		       COUNT(*) AS games
		FROM game_players a
		JOIN game_players b ON b.game_id = a.game_id AND b.team = a.team AND a.player_id < b.player_id
		JOIN games g ON g.id = a.game_id
		LEFT JOIN players pa ON pa.id = a.player_id
		LEFT JOIN players pb ON pb.id = b.player_id
		WHERE g.status = 'finished' AND g.counts_for_stats
		GROUP BY a.player_id, pa.name, pa.style, b.player_id, pb.name, pb.style
		HAVING COUNT(*) FILTER (WHERE g.winner_team = a.team) > 0
		ORDER BY wins DESC, games ASC, a.player_id, b.player_id
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DuoEntry
	for rows.Next() {
		var ida, idb string
		var na, nb *string
		var sa, sb []byte
		var e DuoEntry
		if err := rows.Scan(&ida, &na, &sa, &idb, &nb, &sb, &e.Wins, &e.Games); err != nil {
			return nil, err
		}
		e.Players = [2]PlayerRef{ref(ida, na, sa), ref(idb, nb, sb)}
		e.Rank = len(out) + 1
		if n := len(out); n > 0 && out[n-1].Wins == e.Wins && out[n-1].Games == e.Games {
			e.Rank = out[n-1].Rank
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type GamePlayer struct {
	Player PlayerRef
	Team   int
}

type RoundSummary struct {
	Round      int
	Team       int
	Proclaimer PlayerRef
	Holder     PlayerRef
	WonAt      time.Time
}

type GameSummary struct {
	ID             int64
	Room           string
	StartedAt      time.Time
	EndedAt        *time.Time
	Status         string
	EndReason      *string
	WinnerTeam     *int
	Draw           bool
	CountsForStats bool
	Players        []GamePlayer
	Rounds         []RoundSummary
}

// Game returns a game with its players and rounds.
func (s *Store) Game(ctx context.Context, id int64) (*GameSummary, error) {
	g := &GameSummary{ID: id}
	err := s.pool.QueryRow(ctx, `
		SELECT room, started_at, ended_at, status, end_reason, winner_team, draw, counts_for_stats
		FROM games WHERE id = $1`, id).Scan(&g.Room, &g.StartedAt, &g.EndedAt, &g.Status, &g.EndReason, &g.WinnerTeam, &g.Draw, &g.CountsForStats)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	prow, err := s.pool.Query(ctx, `
		SELECT gp.player_id, p.name, p.style, gp.team
		FROM game_players gp LEFT JOIN players p ON p.id = gp.player_id
		WHERE gp.game_id = $1 ORDER BY gp.team, gp.player_id`, id)
	if err != nil {
		return nil, err
	}
	defer prow.Close()
	for prow.Next() {
		var pid string
		var name *string
		var style []byte
		var team int
		if err := prow.Scan(&pid, &name, &style, &team); err != nil {
			return nil, err
		}
		g.Players = append(g.Players, GamePlayer{Player: ref(pid, name, style), Team: team})
	}
	if err := prow.Err(); err != nil {
		return nil, err
	}

	rrow, err := s.pool.Query(ctx, `
		SELECT r.round, r.team, r.proclaimer, pp.name, pp.style, r.holder, ph.name, ph.style, r.won_at
		FROM rounds r
		LEFT JOIN players pp ON pp.id = r.proclaimer
		LEFT JOIN players ph ON ph.id = r.holder
		WHERE r.game_id = $1 ORDER BY r.round`, id)
	if err != nil {
		return nil, err
	}
	defer rrow.Close()
	for rrow.Next() {
		var rs RoundSummary
		var pid, hid string
		var pn, hn *string
		var ps, hs []byte
		if err := rrow.Scan(&rs.Round, &rs.Team, &pid, &pn, &ps, &hid, &hn, &hs, &rs.WonAt); err != nil {
			return nil, err
		}
		rs.Proclaimer, rs.Holder = ref(pid, pn, ps), ref(hid, hn, hs)
		g.Rounds = append(g.Rounds, rs)
	}
	return g, rrow.Err()
}

// WasPlayer reports whether the player took part in the game.
func (s *Store) WasPlayer(ctx context.Context, gameID int64, player string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM game_players WHERE game_id = $1 AND player_id = $2)`, gameID, player).Scan(&ok)
	return ok, err
}

// ChatMessage is one line of a game's public chat. ID is its number within
// the game, the same one it had when it was sent live.
type ChatMessage struct {
	ID     int
	Player PlayerRef
	Text   string
	At     time.Time
}

// GameChat returns a game's chat since the given time, oldest first,
// starting after message number afterSeq (0 for all) and at most limit lines.
func (s *Store) GameChat(ctx context.Context, gameID int64, since time.Time, afterSeq, limit int) ([]ChatMessage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.seq, c.player_id, p.name, p.style, c.body, c.created_at
		FROM chat_messages c LEFT JOIN players p ON p.id = c.player_id
		WHERE c.game_id = $1 AND c.created_at >= $2 AND c.seq > $3
		ORDER BY c.seq LIMIT $4`, gameID, since, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var pid string
		var name *string
		var style []byte
		if err := rows.Scan(&m.ID, &pid, &name, &style, &m.Text, &m.At); err != nil {
			return nil, err
		}
		m.Player = ref(pid, name, style)
		out = append(out, m)
	}
	return out, rows.Err()
}
