// Package live brings games that were running when the server last stopped
// back, from what the database saved.
package live

import (
	"context"
	"log/slog"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
	"github.com/alexindevs/jackpot-backend/internal/store"
	"github.com/alexindevs/jackpot-backend/internal/transport"
)

// chatOnRestore is how many recent public chat lines a restored game remembers
// for its snapshots.
const chatOnRestore = 50

// Restore brings games that were running when the server last stopped
// back as paused rooms, so their players can pick them up again. Games that
// can't be rebuilt are marked abandoned.
func Restore(ctx context.Context, db *store.Store, m *transport.Manager, w *store.Writer, log *slog.Logger) error {
	now := time.Now()
	if n, err := db.AbandonUnrestorable(ctx, now); err != nil {
		return err
	} else if n > 0 {
		log.Warn("games with no saved setup were abandoned", "games", n)
	}
	live, bad, err := db.LiveGames(ctx, chatOnRestore)
	if err != nil {
		return err
	}
	for id, reason := range bad {
		log.Error("a live game could not be read and was abandoned", "game", id, "err", reason)
		_ = db.AbandonGame(ctx, id, now)
	}
	lobbies, badLobbies, err := db.Lobbies(ctx)
	if err != nil {
		return err
	}
	for room, reason := range badLobbies {
		log.Error("a saved lobby could not be read and was dropped", "room", room, "err", reason)
		_ = db.DeleteLobby(ctx, room)
	}
	if len(live) == 0 && len(lobbies) == 0 {
		return nil
	}

	var items []transport.RestoreItem
	var ids []string
	for _, g := range live {
		if g.Gap {
			log.Warn("a live game's log has a gap; it comes back at the last complete step", "game", g.GameID, "room", g.Room, "commands", len(g.Commands))
		}
		items = append(items, transport.RestoreItem{GameID: g.GameID, Saved: room.Saved{
			ID: g.Room, Creator: engine.PlayerID(g.Creator), Setup: g.Setup, Commands: g.Commands,
			ChatSeq: g.ChatSeq, Chat: toRoomChat(g.Chat), TeamChat: toRoomTeamChat(g.TeamChat), Since: g.LastActive,
		}})
		for _, sp := range g.Setup.Specs {
			ids = append(ids, string(sp.ID))
		}
	}
	for _, l := range lobbies {
		ids = append(ids, l.Members...)
	}
	profiles := map[engine.PlayerID]transport.Identity{}
	known, err := db.Players(ctx, ids)
	if err != nil {
		log.Error("could not load player names for restored games", "err", err)
	}
	for _, p := range known {
		profiles[engine.PlayerID(p.ID)] = transport.Identity{ID: engine.PlayerID(p.ID), Name: p.Name, Style: p.Style}
	}

	ok, failed := m.Restore(items, profiles)
	for _, r := range ok {
		w.Adopt(r.Room, r.GameID)
	}
	for id, reason := range failed {
		log.Warn("a live game could not be restored and was abandoned", "game", id, "err", reason)
		_ = db.AbandonGame(ctx, id, now)
	}
	log.Info("live games restored, paused until their players return", "restored", len(ok), "abandoned", len(failed))

	// Lobbies come after games, so a lobby row left behind by a game that had
	// already started is dropped instead of bringing the room back twice.
	var saved []room.SavedLobby
	for _, l := range lobbies {
		sl := room.SavedLobby{ID: l.Room, Creator: engine.PlayerID(l.Creator), Short: l.Short, Since: l.UpdatedAt}
		for _, id := range l.Members {
			sl.Members = append(sl.Members, engine.PlayerID(id))
		}
		for _, p := range l.Pairs {
			sl.Pairs = append(sl.Pairs, [2]engine.PlayerID{engine.PlayerID(p[0]), engine.PlayerID(p[1])})
		}
		for _, p := range l.Invites {
			sl.Invites = append(sl.Invites, [2]engine.PlayerID{engine.PlayerID(p[0]), engine.PlayerID(p[1])})
		}
		saved = append(saved, sl)
	}
	okLobbies, failedLobbies := m.RestoreLobbies(saved, profiles)
	for roomID, reason := range failedLobbies {
		log.Info("a saved lobby was not restored", "room", roomID, "reason", reason)
		_ = db.DeleteLobby(ctx, roomID)
	}
	if len(lobbies) > 0 {
		log.Info("lobbies restored, paused until their members return", "restored", len(okLobbies), "dropped", len(failedLobbies))
	}
	return nil
}

func toRoomTeamChat(in []store.TeamChatLine) []room.TeamChatEntry {
	out := make([]room.TeamChatEntry, 0, len(in))
	for _, l := range in {
		out = append(out, room.TeamChatEntry{Seq: l.Seq, Round: l.Round, Team: engine.TeamID(l.Team), From: engine.PlayerID(l.Player), Text: l.Text, At: l.At})
	}
	return out
}

func toRoomChat(in []store.ChatMessage) []room.ChatMessage {
	out := make([]room.ChatMessage, 0, len(in))
	for _, c := range in {
		out = append(out, room.ChatMessage{ID: c.ID, From: engine.PlayerID(c.Player.ID), Text: c.Text, At: c.At})
	}
	return out
}
