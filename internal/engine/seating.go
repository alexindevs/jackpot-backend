package engine

import (
	"math/rand/v2"
	"slices"
)

// seatPlayers arranges players around a ring in random order, with no player
// sitting directly beside their partner. teamOf must give each player's team;
// teams must have exactly two members and there must be at least two teams.
func seatPlayers(rng *rand.Rand, ids []PlayerID, teamOf func(PlayerID) TeamID) []PlayerID {
	seats := slices.Clone(ids)
	for try := 0; try < 1000; try++ {
		rng.Shuffle(len(seats), func(i, j int) { seats[i], seats[j] = seats[j], seats[i] })
		if !partnersAdjacent(seats, teamOf) {
			return seats
		}
	}
	return constructiveSeating(rng, ids, teamOf)
}

// constructiveSeating always works for two or more teams: first members of
// every team, then second members in the same team order. Partners end up
// half a ring apart. A random rotation keeps it from always starting equal.
func constructiveSeating(rng *rand.Rand, ids []PlayerID, teamOf func(PlayerID) TeamID) []PlayerID {
	var teams []TeamID
	members := map[TeamID][]PlayerID{}
	for _, id := range ids {
		t := teamOf(id)
		if _, ok := members[t]; !ok {
			teams = append(teams, t)
		}
		members[t] = append(members[t], id)
	}
	rng.Shuffle(len(teams), func(i, j int) { teams[i], teams[j] = teams[j], teams[i] })
	var first, second []PlayerID
	for _, t := range teams {
		first = append(first, members[t][0])
		second = append(second, members[t][1])
	}
	seats := append(first, second...)
	r := rng.IntN(len(seats))
	return append(slices.Clone(seats[r:]), seats[:r]...)
}

// partnersAdjacent reports whether any two partners sit side by side in the
// ring.
func partnersAdjacent(seats []PlayerID, teamOf func(PlayerID) TeamID) bool {
	n := len(seats)
	if n < 3 {
		return false
	}
	for i := range seats {
		if teamOf(seats[i]) == teamOf(seats[(i+1)%n]) {
			return true
		}
	}
	return false
}
