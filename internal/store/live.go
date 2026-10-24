package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// storedSetup is engine.Setup as kept in the database. The two seeds are
// written as strings so they survive any JSON tool between us and the disk
// (they can be larger than a double represents exactly).
type storedSetup struct {
	Config engine.Config       `json:"config"`
	Specs  []engine.PlayerSpec `json:"specs"`
	SeedA  string              `json:"seedA"`
	SeedB  string              `json:"seedB"`
}

func encodeSetup(su engine.Setup) ([]byte, error) {
	return json.Marshal(storedSetup{
		Config: su.Config, Specs: su.Specs,
		SeedA: strconv.FormatUint(su.SeedA, 10), SeedB: strconv.FormatUint(su.SeedB, 10),
	})
}

func decodeSetup(b []byte) (engine.Setup, error) {
	var st storedSetup
	if err := json.Unmarshal(b, &st); err != nil {
		return engine.Setup{}, err
	}
	a, err := strconv.ParseUint(st.SeedA, 10, 64)
	if err != nil {
		return engine.Setup{}, fmt.Errorf("bad seed: %w", err)
	}
	bb, err := strconv.ParseUint(st.SeedB, 10, 64)
	if err != nil {
		return engine.Setup{}, fmt.Errorf("bad seed: %w", err)
	}
	return engine.Setup{Config: st.Config, Specs: st.Specs, SeedA: a, SeedB: bb}, nil
}

// SaveSetup keeps what is needed to rebuild a game: who created the room and
// how the game was set up. Saving it twice is harmless.
func (s *Store) SaveSetup(ctx context.Context, gameID int64, creator string, su engine.Setup) error {
	b, err := encodeSetup(su)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE games SET creator = $2, setup = $3 WHERE id = $1`, gameID, creator, b)
	return err
}

// AddEvent saves engine command number seq of a game. Saving the same number
// twice is harmless, so a retry can't duplicate a command.
func (s *Store) AddEvent(ctx context.Context, gameID int64, seq int, at time.Time, c engine.Command) error {
	b, err := engine.MarshalCommand(c)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		WITH ins AS (
			INSERT INTO game_events (game_id, seq, at, command) VALUES ($1, $2, $3, $4)
			ON CONFLICT (game_id, seq) DO NOTHING
			RETURNING 1
		)
		UPDATE games SET last_event_at = GREATEST(COALESCE(last_event_at, $3), $3)
		WHERE id = $1 AND EXISTS (SELECT 1 FROM ins)`,
		gameID, seq, at, b)
	return err
}

// LiveGame is a game that was still being played when the server stopped, with
// everything needed to bring it back.
type LiveGame struct {
	GameID   int64
	Room     string
	Creator  string
	Setup    engine.Setup
	Commands []engine.Command
	// Gap is true if a command was missing from the log: Commands then stops
	// just before it, so the game comes back a little behind where it was.
	Gap        bool
	LastActive time.Time
	ChatSeq    int
	Chat       []ChatMessage // the most recent lines, oldest first
	TeamChat   []TeamChatLine
}

// LiveGames loads every game still marked as playing that has saved setup. A
// game whose data can't be read is reported in bad, with the reason, and the
// caller decides what to do with it.
func (s *Store) LiveGames(ctx context.Context, chatTail int) (live []LiveGame, bad map[int64]error, err error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, room, COALESCE(creator, ''), setup, COALESCE(last_event_at, started_at)
		FROM games WHERE status = 'playing' AND setup IS NOT NULL ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	type head struct {
		id            int64
		room, creator string
		setup         []byte
		active        time.Time
	}
	var heads []head
	for rows.Next() {
		var h head
		if err := rows.Scan(&h.id, &h.room, &h.creator, &h.setup, &h.active); err != nil {
			rows.Close()
			return nil, nil, err
		}
		heads = append(heads, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	bad = map[int64]error{}
	for _, h := range heads {
		lg := LiveGame{GameID: h.id, Room: h.room, Creator: h.creator, LastActive: h.active}
		if lg.Setup, err = decodeSetup(h.setup); err != nil {
			bad[h.id] = err
			continue
		}
		evs, err := s.pool.Query(ctx, `SELECT seq, command FROM game_events WHERE game_id = $1 ORDER BY seq`, h.id)
		if err != nil {
			return nil, nil, err
		}
		next := 1
		var cmdErr error
		for evs.Next() {
			var seq int
			var raw []byte
			if err := evs.Scan(&seq, &raw); err != nil {
				evs.Close()
				return nil, nil, err
			}
			if seq != next {
				lg.Gap = true
				break
			}
			c, err := engine.UnmarshalCommand(raw)
			if err != nil {
				cmdErr = fmt.Errorf("command %d: %w", seq, err)
				break
			}
			lg.Commands = append(lg.Commands, c)
			next++
		}
		evs.Close()
		if cmdErr != nil {
			bad[h.id] = cmdErr
			continue
		}

		chat, err := s.GameChat(ctx, h.id, time.Time{}, 0, 100000)
		if err != nil {
			return nil, nil, err
		}
		if len(chat) > 0 {
			lg.ChatSeq = chat[len(chat)-1].ID
		}
		if len(chat) > chatTail {
			chat = chat[len(chat)-chatTail:]
		}
		lg.Chat = chat
		if lg.TeamChat, err = s.GameTeamChat(ctx, h.id); err != nil {
			return nil, nil, err
		}
		live = append(live, lg)
	}
	return live, bad, nil
}

// AbandonUnrestorable marks games still "playing" that have no saved setup as
// abandoned: they were cut off by a restart before game saving existed, or
// their setup never reached the database.
func (s *Store) AbandonUnrestorable(ctx context.Context, at time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE games SET status = 'abandoned', ended_at = $1 WHERE status = 'playing' AND setup IS NULL`, at)
	return tag.RowsAffected(), err
}

// SweepExpired removes the data kept for settling disputes once a game has
// been over for long enough: its public chat, saved commands and setup. The
// summary of the game (result, rounds, players) is kept for good, because the
// leaderboards are built from it. It returns how many games were cleared.
func (s *Store) SweepExpired(ctx context.Context, endedBefore time.Time) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		DELETE FROM chat_messages c USING games g
		WHERE c.game_id = g.id AND g.status <> 'playing' AND g.ended_at < $1`, endedBefore); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM team_chat_messages c USING games g
		WHERE c.game_id = g.id AND g.status <> 'playing' AND g.ended_at < $1`, endedBefore); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM lobbies WHERE updated_at < $1`, endedBefore); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM game_events e USING games g
		WHERE e.game_id = g.id AND g.status <> 'playing' AND g.ended_at < $1`, endedBefore); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE games SET setup = NULL
		WHERE status <> 'playing' AND ended_at < $1 AND setup IS NOT NULL`, endedBefore)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

// RunSweeper clears expired dispute data every interval until ctx ends.
func (s *Store) RunSweeper(ctx context.Context, every, retention time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n, err := s.SweepExpired(ctx, now.Add(-retention)); err != nil {
				log.Error("sweep failed", "err", err)
			} else if n > 0 {
				log.Info("expired game data cleared", "games", n)
			}
		}
	}
}

// Players loads the names and looks of the given players, for showing who is in
// a game before they have signed in again. Players we have never seen are left out.
func (s *Store) Players(ctx context.Context, ids []string) ([]Player, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, name, style FROM players WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Player
	for rows.Next() {
		var p Player
		var style []byte
		if err := rows.Scan(&p.ID, &p.Name, &style); err != nil {
			return nil, err
		}
		p.Style = style
		out = append(out, p)
	}
	return out, rows.Err()
}

// CommandCount is how many commands are saved for a game and the highest
// sequence number among them. They match when the log has no holes.
func (s *Store) CommandCount(ctx context.Context, gameID int64) (count, maxSeq int, err error) {
	err = s.pool.QueryRow(ctx, `SELECT count(*), COALESCE(max(seq), 0) FROM game_events WHERE game_id = $1`, gameID).Scan(&count, &maxSeq)
	return
}

// --- lobbies and private team chat ---

// LobbyRow is a saved lobby.
type LobbyRow struct {
	Room      string
	Creator   string
	Members   []string
	Pairs     [][2]string
	Invites   [][2]string
	Short     bool
	UpdatedAt time.Time
}

type lobbyState struct {
	Members []string    `json:"members"`
	Pairs   [][2]string `json:"pairs"`
	Invites [][2]string `json:"invites"`
	Short   bool        `json:"short,omitempty"`
}

// SaveLobby keeps a lobby as it stands now, replacing what was kept before.
func (s *Store) SaveLobby(ctx context.Context, l LobbyRow) error {
	b, err := json.Marshal(lobbyState{Members: l.Members, Pairs: l.Pairs, Invites: l.Invites, Short: l.Short})
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO lobbies (room, creator, state, updated_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (room) DO UPDATE SET creator = EXCLUDED.creator, state = EXCLUDED.state, updated_at = EXCLUDED.updated_at
		WHERE lobbies.updated_at <= EXCLUDED.updated_at`, l.Room, l.Creator, b, l.UpdatedAt)
	return err
}

// DeleteLobby forgets a saved lobby.
func (s *Store) DeleteLobby(ctx context.Context, room string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM lobbies WHERE room = $1`, room)
	return err
}

// Lobbies loads every saved lobby. Rows that can't be read are reported in bad,
// by room, and left for the caller to delete.
func (s *Store) Lobbies(ctx context.Context) (ok []LobbyRow, bad map[string]error, err error) {
	rows, err := s.pool.Query(ctx, `SELECT room, creator, state, updated_at FROM lobbies ORDER BY room`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	bad = map[string]error{}
	for rows.Next() {
		var l LobbyRow
		var raw []byte
		if err := rows.Scan(&l.Room, &l.Creator, &raw, &l.UpdatedAt); err != nil {
			return nil, nil, err
		}
		var st lobbyState
		if err := json.Unmarshal(raw, &st); err != nil {
			bad[l.Room] = err
			continue
		}
		l.Members, l.Pairs, l.Invites, l.Short = st.Members, st.Pairs, st.Invites, st.Short
		ok = append(ok, l)
	}
	return ok, bad, rows.Err()
}

// TeamChatLine is a saved line of private team chat.
type TeamChatLine struct {
	Seq    int
	Team   int
	Round  int
	Player string
	Text   string
	At     time.Time
}

// AddTeamChat saves line number seq of a game's private team chat. Saving the
// same number twice is harmless.
func (s *Store) AddTeamChat(ctx context.Context, gameID int64, l TeamChatLine) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO team_chat_messages (game_id, seq, team, round, player_id, body, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (game_id, seq) DO NOTHING`, gameID, l.Seq, l.Team, l.Round, l.Player, l.Text, l.At)
	return err
}

// GameTeamChat loads a game's private team chat, oldest first.
func (s *Store) GameTeamChat(ctx context.Context, gameID int64) ([]TeamChatLine, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT seq, team, round, player_id, body, created_at FROM team_chat_messages WHERE game_id = $1 ORDER BY seq`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TeamChatLine
	for rows.Next() {
		var l TeamChatLine
		if err := rows.Scan(&l.Seq, &l.Team, &l.Round, &l.Player, &l.Text, &l.At); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
