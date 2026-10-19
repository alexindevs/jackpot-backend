package engine

import "testing"

func TestSeatingNeverPlacesPartnersTogether(t *testing.T) {
	for teams := 2; teams <= 5; teams++ {
		specs := testSpecs(teams)
		teamOf := map[PlayerID]TeamID{}
		var ids []PlayerID
		for _, sp := range specs {
			teamOf[sp.ID] = sp.Team
			ids = append(ids, sp.ID)
		}
		f := func(id PlayerID) TeamID { return teamOf[id] }
		for seed := uint64(0); seed < 500; seed++ {
			seats := seatPlayers(testRNG(seed), ids, f)
			if len(seats) != len(ids) || partnersAdjacent(seats, f) {
				t.Fatalf("%d teams, seed %d: bad seating %v", teams, seed, seats)
			}
			seats = constructiveSeating(testRNG(seed), ids, f)
			if len(seats) != len(ids) || partnersAdjacent(seats, f) {
				t.Fatalf("%d teams, seed %d: bad constructive seating %v", teams, seed, seats)
			}
		}
	}
}
