package store

import (
	"context"
	"slices"
	"time"
)

// FinishedTeam is one team of a finished game, as its list entry shows it.
type FinishedTeam struct {
	Names []string
	Wins  int
	Mine  bool
}

// FinishedGame is one entry in a player's list of finished games.
type FinishedGame struct {
	Room           string
	Outcome        string // "won", "lost" or "draw", for the player's team
	Reason         string // "normal", "forfeit" or "agreement"
	Round          int    // the last round played
	FinishedAt     time.Time
	WinsToFinish   int // 7 for a long game, 4 for a short one
	CountsForStats bool
	Teams          []FinishedTeam // every team, in team order
}

// FinishedGames lists the games a player took part in that ran to the end,
// newest first. Games that were abandoned or are still going are left out.
// Names are the players' current names, and a bot shows its own.
func (s *Store) FinishedGames(ctx context.Context, player string, limit int) ([]FinishedGame, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.room, g.ended_at, g.end_reason, g.winner_team, g.draw, g.counts_for_stats,
		       g.wins_to_finish, g.rounds_played, me.team
		FROM games g JOIN game_players me ON me.game_id = g.id AND me.player_id = $1
		WHERE g.status = 'finished'
		ORDER BY g.ended_at DESC, g.id DESC
		LIMIT $2`, player, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type head struct {
		g      FinishedGame
		winner *int
		draw   bool
		mine   int
	}
	var heads []head
	var ids []int64
	byID := map[int64]int{}
	for rows.Next() {
		var id int64
		var h head
		var reason string
		var rounds int
		if err := rows.Scan(&id, &h.g.Room, &h.g.FinishedAt, &reason, &h.winner, &h.draw, &h.g.CountsForStats, &h.g.WinsToFinish, &rounds, &h.mine); err != nil {
			return nil, err
		}
		h.g.Round = rounds
		switch reason {
		case "forfeit":
			h.g.Reason = "forfeit"
		case "agreement":
			h.g.Reason = "agreement"
		default:
			h.g.Reason = "normal"
		}
		byID[id] = len(heads)
		ids = append(ids, id)
		heads = append(heads, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(heads) == 0 {
		return []FinishedGame{}, nil
	}

	teams := map[int64]map[int]*FinishedTeam{}
	team := func(id int64, t int) *FinishedTeam {
		if teams[id] == nil {
			teams[id] = map[int]*FinishedTeam{}
		}
		if teams[id][t] == nil {
			teams[id][t] = &FinishedTeam{Names: []string{}}
		}
		return teams[id][t]
	}
	prow, err := s.pool.Query(ctx, `
		SELECT gp.game_id, gp.team, gp.player_id, p.name
		FROM game_players gp LEFT JOIN players p ON p.id = gp.player_id
		WHERE gp.game_id = ANY($1)
		ORDER BY gp.game_id, gp.team, gp.player_id`, ids)
	if err != nil {
		return nil, err
	}
	for prow.Next() {
		var id int64
		var t int
		var pid string
		var name *string
		if err := prow.Scan(&id, &t, &pid, &name); err != nil {
			prow.Close()
			return nil, err
		}
		n := ref(pid, name, nil).Name
		ft := team(id, t)
		ft.Names = append(ft.Names, n)
	}
	prow.Close()
	if err := prow.Err(); err != nil {
		return nil, err
	}

	rrow, err := s.pool.Query(ctx, `
		SELECT game_id, team, COUNT(*), MAX(round) FROM rounds WHERE game_id = ANY($1) GROUP BY game_id, team`, ids)
	if err != nil {
		return nil, err
	}
	lastWon := map[int64]int{}
	for rrow.Next() {
		var id int64
		var t, wins, last int
		if err := rrow.Scan(&id, &t, &wins, &last); err != nil {
			rrow.Close()
			return nil, err
		}
		team(id, t).Wins = wins
		lastWon[id] = max(lastWon[id], last)
	}
	rrow.Close()
	if err := rrow.Err(); err != nil {
		return nil, err
	}

	out := make([]FinishedGame, len(heads))
	for id, i := range byID {
		h := heads[i]
		g := h.g
		switch {
		case h.draw:
			g.Outcome = "draw"
		case h.winner != nil && *h.winner == h.mine:
			g.Outcome = "won"
		default:
			g.Outcome = "lost"
		}
		if g.Round == 0 {
			g.Round = lastWon[id]
		}
		var order []int
		for t := range teams[id] {
			order = append(order, t)
		}
		slices.Sort(order)
		g.Teams = make([]FinishedTeam, 0, len(order))
		for _, t := range order {
			ft := *teams[id][t]
			ft.Mine = t == h.mine
			g.Teams = append(g.Teams, ft)
		}
		out[i] = g
	}
	return out, nil
}
