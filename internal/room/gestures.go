package room

import "strings"

// Gesture is one thing a player can do as a signal. ID is what travels over
// the wire. Label is the text for the sender's button, Seen is how it reads to
// someone watching ("Ada <seen>"). Group lets a client lay the palette out in
// sections.
type Gesture struct {
	ID    string
	Label string
	Seen  string
	Group string
}

// vocabulary is the fixed signal vocabulary: ordinary human things a person
// might do at a table, with a Nigerian flavour. One gesture per line as
// "id|button label|seen as"; lines starting with "#" start a group.
const vocabulary = `
# Eyes and face
blink_once|Blink once|blinks once
blink_twice|Blink twice|blinks twice
blink_thrice|Blink three times|blinks three times
wink_left|Wink (left eye)|winks with the left eye
wink_right|Wink (right eye)|winks with the right eye
raise_eyebrow|Raise one eyebrow|raises one eyebrow
raise_eyebrows|Raise both eyebrows|raises both eyebrows
frown|Frown|frowns
smile_slowly|Smile slowly|smiles slowly
grin|Grin|grins
squint|Squint|squints
roll_eyes|Roll eyes|rolls their eyes
stare_at_ceiling|Stare at the ceiling|stares at the ceiling
look_left|Look left|looks left
look_right|Look right|looks right
look_down|Look down|looks down
wide_eyes|Open eyes wide|opens their eyes wide
pout|Pout|pouts
bite_lip|Bite lip|bites their lip
puff_cheeks|Puff cheeks|puffs their cheeks

# Nose, mouth and noises
sneeze|Sneeze|sneezes
sneeze_twice|Sneeze twice|sneezes twice
rub_nose|Rub nose|rubs their nose
scratch_nose|Scratch nose|scratches their nose
sniff|Sniff|sniffs
sniffle|Sniffle|sniffles
blow_nose|Blow nose|blows their nose
cough|Cough|coughs
cough_twice|Cough twice|coughs twice
clear_throat|Clear throat|clears their throat
hiccup|Hiccup|hiccups
belch|Belch|belches
yawn|Yawn|yawns
sigh|Sigh|sighs
groan|Groan|groans
gasp|Gasp|gasps
giggle|Giggle|giggles
snort|Snort|snorts
laugh_out_loud|Laugh out loud|laughs out loud
laugh_at_nothing|Laugh at nothing|laughs at nothing
whistle|Whistle|whistles
hum|Hum|hums
kiss_teeth|Kiss teeth (tsk)|kisses their teeth
hiss|Hiss|hisses
smack_lips|Smack lips|smacks their lips
lick_lips|Lick lips|licks their lips
click_tongue|Click tongue|clicks their tongue
mutter|Mutter to self|mutters to themselves
whisper|Whisper something|whispers something
cough_into_fist|Cough into fist|coughs into their fist

# Hands and arms
snap_fingers|Snap fingers|snaps their fingers
clap_once|Clap once|claps once
clap_twice|Clap twice|claps twice
clap_thrice|Clap three times|claps three times
slow_clap|Slow clap|does a slow clap
drum_fingers|Drum fingers|drums their fingers
tap_table|Tap the table|taps the table
knock_table_twice|Knock the table twice|knocks on the table twice
wave|Wave|waves
thumbs_up|Thumbs up|gives a thumbs up
thumbs_down|Thumbs down|gives a thumbs down
peace_sign|Peace sign|makes a peace sign
point_at_partner|Point at partner|points at their partner
point_at_ceiling|Point at the ceiling|points at the ceiling
point_at_nothing|Point at nothing|points at nothing
fist_in_palm|Fist into palm|punches their fist into their palm
rub_hands|Rub hands together|rubs their hands together
crack_knuckles|Crack knuckles|cracks their knuckles
cross_arms|Cross arms|crosses their arms
hands_behind_head|Hands behind head|puts their hands behind their head
hand_on_head|Hand on head (shock)|puts a hand on their head in shock
hand_on_chest|Hand on chest|puts a hand on their chest
hand_on_cheek|Hand on cheek|puts a hand on their cheek
shrug|Shrug|shrugs
open_palms|Open palms|opens their palms
cover_mouth|Cover mouth|covers their mouth
cover_eyes|Cover eyes|covers their eyes
count_fingers|Count on fingers|counts on their fingers
salute|Salute|salutes
fan_self|Fan yourself|fans themselves
wipe_forehead|Wipe forehead|wipes their forehead

# Head and neck
nod|Nod|nods
shake_head|Shake head|shakes their head
shake_head_slowly|Shake head slowly|shakes their head slowly
tilt_head|Tilt head|tilts their head
scratch_head|Scratch head|scratches their head
rub_neck|Rub neck|rubs their neck
stretch_neck|Stretch neck|stretches their neck
rub_chin|Rub chin|rubs their chin
touch_ear|Touch ear|touches their ear
pull_earlobe|Pull earlobe|pulls their earlobe
smooth_hair|Smooth hair|smooths their hair
adjust_cap|Adjust cap|adjusts their cap
adjust_glasses|Adjust glasses|adjusts their glasses
bow_head|Bow head|bows their head
pat_hair|Pat head wrap|pats their head wrap

# Posture and movement
lean_back|Lean back|leans back
lean_forward|Lean forward|leans forward
sit_up_straight|Sit up straight|sits up straight
slouch|Slouch|slouches
cross_legs|Cross legs|crosses their legs
shake_leg|Shake leg|shakes their leg
tap_foot|Tap foot|taps their foot
stand_up|Stand up|stands up
sit_down|Sit down|sits down
stretch_arms|Stretch arms|stretches their arms
turn_around|Turn around|turns around
shift_in_seat|Shift in seat|shifts in their seat
rock_chair|Rock chair|rocks their chair
rub_knees|Rub knees|rubs their knees
pat_belly|Pat belly|pats their belly
adjust_clothes|Adjust clothes|adjusts their clothes
adjust_wrapper|Adjust wrapper|adjusts their wrapper
dust_shoulder|Dust shoulder|dusts off their shoulder
smooth_clothes|Smooth clothes|smooths their clothes
tie_shoelace|Tie shoelace|ties their shoelace
check_watch|Check watch|checks their watch
check_phone|Check phone|checks their phone
look_at_wall_clock|Look at the clock|looks at the clock
dance_a_little|Dance a little|dances a little
do_the_shoki|Shoki shoulders|does a little shoki

# Food, drink and objects
sip_water|Sip water|sips water
gulp_water|Gulp water|gulps down water
sip_zobo|Sip zobo|sips zobo
eat_groundnut|Eat groundnut|eats a groundnut
crunch_chin_chin|Crunch chin chin|crunches chin chin
chew_gum|Chew gum|chews gum
suck_sweet|Suck a sweet|sucks a sweet
bite_nails|Bite nails|bites their nails
open_bottle|Open a bottle|opens a bottle
fan_with_paper|Fan with paper|fans with a piece of paper
click_pen|Click pen|clicks their pen
shuffle_papers|Shuffle papers|shuffles papers
drop_something|Drop something|drops something
knock_over_cup|Knock over a cup|knocks over a cup
wipe_sweat|Wipe sweat|wipes off sweat
lick_fingers|Lick fingers|licks their fingers
rub_belly_hungry|Rub belly (hungry)|rubs their belly, hungry
rattle_ice|Rattle the ice|rattles the ice in their cup
pick_teeth|Pick teeth|picks their teeth
use_toothpick|Use a toothpick|uses a toothpick

# Singing and sounds
sing_a_line|Sing a line|sings a line
sing_off_key|Sing off key|sings off key
sing_happy_birthday|Sing happy birthday|sings happy birthday
sing_a_hymn|Sing a hymn|sings a hymn
sing_afrobeats_hook|Sing an afrobeats hook|sings an afrobeats hook
sing_a_jingle|Sing an advert jingle|sings an advert jingle
rap_a_line|Rap a line|raps a line
beatbox|Beatbox|beatboxes
drum_a_beat|Drum a beat|drums out a beat
hum_anthem|Hum the anthem|hums the national anthem
whistle_a_tune|Whistle a tune|whistles a tune
call_a_name|Shout a name|shouts a name
ululate|Ululate|ululates

# Words and phrases
say_oya|Say "oya"|says "oya"
say_abeg|Say "abeg"|says "abeg"
say_ehn|Say "ehn?"|says "ehn?"
say_ah_ah|Say "ah ah"|says "ah ah"
say_chai|Say "chai!"|says "chai!"
say_wahala|Say "wahala"|says "wahala"
say_na_wa|Say "na wa o"|says "na wa o"
say_sharp_sharp|Say "sharp sharp"|says "sharp sharp"
say_wetin_dey|Say "wetin dey"|says "wetin dey"
say_how_far|Say "how far"|says "how far"
say_kai|Say "kai!"|says "kai!"
say_walahi|Say "walahi"|says "walahi"
say_shebi|Say "shebi"|says "shebi"
say_e_choke|Say "e choke"|says "e choke"
say_ehen|Say "ehen"|says "ehen"
say_omo|Say "omo"|says "omo"
say_mehn|Say "mehn"|says "mehn"
say_oga|Say "oga"|says "oga"
say_my_guy|Say "my guy"|says "my guy"
say_no_wahala|Say "no wahala"|says "no wahala"
say_calm_down|Say "calm down"|says "calm down"
say_make_we_go|Say "make we go"|says "make we go"
say_e_dey_hot|Say "e dey hot"|says "e dey hot"
say_we_move|Say "we move"|says "we move"
say_well_done|Say "well done"|says "well done"
say_nice_one|Say "nice one"|says "nice one"
say_sorry|Say "sorry"|says "sorry"
say_hmm|Say "hmm"|says "hmm"
say_wow|Say "wow"|says "wow"
say_seriously|Say "seriously?"|says "seriously?"
say_not_today|Say "not today"|says "not today"
say_i_am_finished|Say "I am finished"|says "I am finished"

# Random behaviour
ask_the_time|Ask the time|asks the time
ask_for_water|Ask for water|asks for water
complain_about_heat|Complain about the heat|complains about the heat
complain_about_light|Complain about the light (NEPA)|complains that NEPA took the light
talk_about_traffic|Talk about traffic|talks about traffic
mention_jollof|Mention jollof|mentions jollof
mention_football|Mention football|mentions football
mention_fuel_price|Mention the fuel price|mentions the fuel price
mention_the_weather|Mention the weather|mentions the weather
fake_phone_call|Fake a phone call|fakes a phone call
pretend_to_sleep|Pretend to sleep|pretends to sleep
pretend_to_be_shocked|Pretend to be shocked|pretends to be shocked
dramatic_gasp|Dramatic gasp|gasps dramatically
tell_a_joke|Tell a short joke|tells a short joke
praise_partner|Praise your partner|praises their partner
tease_opponent|Tease an opponent|teases an opponent
`

var gestures = parseVocabulary(vocabulary)

// DefaultGestures is the signal vocabulary a room uses unless told otherwise.
func DefaultGestures() []Gesture { return append([]Gesture(nil), gestures...) }

func parseVocabulary(s string) []Gesture {
	var out []Gesture
	group := ""
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "# "):
			group = strings.TrimPrefix(line, "# ")
		default:
			f := strings.Split(line, "|")
			if len(f) != 3 {
				panic("bad gesture line: " + line)
			}
			out = append(out, Gesture{ID: f[0], Label: f[1], Seen: f[2], Group: group})
		}
	}
	return out
}
