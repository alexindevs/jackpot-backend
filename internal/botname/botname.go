// Package botname holds what identifies a bot: its player id, its name and its
// look. Rooms, the transport and the store all need to tell a bot from a
// person, and to show one, without depending on each other.
package botname

import (
	"encoding/json"
	"hash/fnv"
	"math/rand/v2"
	"strings"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// Prefix starts every bot's player id. Real start.ng ids are numbers and dev
// sign-ins start with "dev:", so nothing a person signs in with can match it;
// the transport also refuses any sign-in that does.
const Prefix = "bot:"

// IsBotID reports whether a player id belongs to a bot.
func IsBotID(id engine.PlayerID) bool { return strings.HasPrefix(string(id), Prefix) }

// names are given names from across Nigeria's languages, chosen to read as
// real people and not as famous ones. They were gathered from public
// name-meaning pages, which are not linguistic references, so a speaker of
// each language should still check them before they ship. Origin is for that
// review only.
var names = []struct{ Name, Origin string }{
	// Yoruba
	{"Adetula", "Yoruba"}, {"Adigun", "Yoruba"}, {"Akintunde", "Yoruba"}, {"Olatunji", "Yoruba"},
	{"Ereadura", "Yoruba"}, {"Ewatomi", "Yoruba"}, {"Morenikeji", "Yoruba"}, {"Titilayo", "Yoruba"},
	{"Kolawole", "Yoruba"}, {"Yetunde", "Yoruba"}, {"Omolara", "Yoruba"}, {"Folasade", "Yoruba"},
	// Igbo
	{"Ikechi", "Igbo"}, {"Ugochi", "Igbo"}, {"Akuchi", "Igbo"}, {"Akachi", "Igbo"},
	{"Okwuoma", "Igbo"}, {"Omenuko", "Igbo"}, {"Kaluocha", "Igbo"}, {"Kaosisochukwu", "Igbo"},
	{"Kamsiyochukwu", "Igbo"}, {"Isioma", "Igbo"},
	// Hausa and Fulani
	{"Garba", "Hausa"}, {"Sani", "Hausa"}, {"Danjuma", "Hausa"}, {"Gambo", "Hausa"},
	{"Hajara", "Hausa"}, {"Binta", "Hausa"}, {"Zakari", "Hausa"}, {"Jamila", "Hausa"},
	// Efik, Ibibio and Annang
	{"Afiah", "Efik"}, {"Adiaba", "Efik"}, {"Bassey", "Efik"}, {"Uyai", "Ibibio"},
	{"Idara", "Efik"}, {"Uduakobong", "Ibibio"}, {"Emem", "Ibibio"}, {"Unwana", "Ibibio"},
	// Edo
	{"Ogbebor", "Edo"}, {"Adesuwa", "Edo"}, {"Osagie", "Edo"}, {"Eghosa", "Edo"},
	{"Nosakhare", "Edo"}, {"Eseosa", "Edo"},
	// Urhobo, Ijaw, Isoko and Tiv
	{"Ejiroghene", "Urhobo"}, {"Oghenekaro", "Urhobo"}, {"Efemena", "Urhobo"}, {"Seiyefa", "Ijaw"},
	{"Kemela", "Ijaw"}, {"Adigheji", "Isoko"}, {"Terkimbi", "Tiv"}, {"Aondofa", "Tiv"},
}

// Count is how many names there are.
func Count() int { return len(names) }

func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ID is the player id for a bot called name.
func ID(name string) engine.PlayerID { return engine.PlayerID(Prefix + slug(name)) }

// Pick chooses a name at random whose id is not in taken. It reports false
// when every name is in use.
func Pick(rng *rand.Rand, taken map[engine.PlayerID]bool) (engine.PlayerID, bool) {
	var free []int
	for i, n := range names {
		if !taken[ID(n.Name)] {
			free = append(free, i)
		}
	}
	if len(free) == 0 {
		return "", false
	}
	return ID(names[free[rng.IntN(len(free))]].Name), true
}

// Profile gives a bot its name and the look the frontend draws from a start.ng
// style object. The look comes from the id, so a bot always looks the same.
// ok is false for an id that is not a bot's.
func Profile(id engine.PlayerID) (name string, style json.RawMessage, ok bool) {
	if !IsBotID(id) {
		return "", nil, false
	}
	key := strings.TrimPrefix(string(id), Prefix)
	name = key
	for _, n := range names {
		if slug(n.Name) == key {
			name = n.Name
			break
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	v := h.Sum32()
	pick := func(shift uint, from []string) string { return from[(v>>shift)%uint32(len(from))] }
	skin := []string{"#3b2219", "#4a2c1d", "#5c3a28", "#6e432d", "#7a4b32", "#8d5a3c", "#a06a47", "#b98059"}
	shirt := []string{"#2f6f5e", "#8a3b3b", "#3b5a8a", "#8a6b2f", "#5b3b8a", "#2f7a8a", "#7a3b6b", "#4f6f2f"}
	hair := []string{"#161210", "#1c1512", "#241c18", "#2c2018"}
	hairStyle := []string{"short", "short", "curly", "none"}
	hat := []string{"none", "none", "none", "cap"}
	b, _ := json.Marshal(map[string]string{
		"skinColor": pick(0, skin), "primaryColor": pick(3, shirt), "hairColor": pick(6, hair),
		"hair": pick(9, hairStyle), "hat": pick(12, hat), "body": "bot",
	})
	return name, b, true
}
