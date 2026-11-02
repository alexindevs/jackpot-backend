# Bots: design

Status: built on the backend (the frontend button and tag are still to do). Written so that one person, or two, can open a lobby, press "Add bots", and play a full game.

## What the author has decided

- A lobby creator can press **Add bots** and play with as few as one human.
- A bot **can be a human's partner.** With one human in the lobby that is the only way to make teams of two.
- Bots get **convincing, rare Nigerian names**, not "Bot 3".
- What a bot says to its human partner has to be **readable by a human**: plain sentences in the team chat, and a code a person can remember.
- **Bot games never count toward the leaderboards** (MVPs, legendary duos).
- It will add considerable time. That is accepted.
- **No public chat from bots.** Their only words are the code they tell a human partner in the private team chat.
- **As many bots as the room can hold (9 seats of 10), but the host is always a person.** If the last person leaves, or disconnects and does not come back (10 minutes), the game ends.
- **One difficulty:** reasonably hard, not impossible to beat and not terribly easy.
- **A visible "bot" tag** next to every bot, in the lobby and at the table. The names read as real people; the tag keeps nobody guessing.

## How a bot fits in

The rules engine is a pure state machine and the room has no clock of its own: you hand it inputs with a timestamp and it hands back messages. A bot therefore does not need any special case in the rules. **A bot is a player the server drives.**

- It has a player id, a name and a character, like a human.
- It sees what a human in its seat sees, nothing more: its own hand, the public events, signals sent to it, the chat. The easiest way to guarantee that is to give the brain only `room.snapshotFor(botID)` and the messages addressed to the bot. It never gets a pointer to the room or the engine. That makes cheating impossible by construction, including the spectator perk, which never applies to bots.
- It acts by sending the same inputs a human sends: `Pass`, `Suspect`, `Reveal`, `Proclaim`, `SendSignal`, `Ready`, `TeamChat`, `Chat`.
- The brain is a pure function: `Decide(view, now) -> []{after, input}`. A small host inside each room's runner calls it when something changes and when time passes, and submits the inputs it returns after their delays. Because it uses the runner's clock, tests can drive whole games with a fake clock, fast and deterministic.

Where it lives: a new package `internal/bot` (the brain, names, code), a host in `internal/transport` or `internal/room` next to the runner, and small changes to the lobby.

## Lobby and identity

- New request `add_bots` (room creator only, lobby only). It fills the lobby into valid teams of two, with 2 to 5 teams in all:
  - an unpaired human gets a bot partner;
  - leftover bots pair with each other;
  - if there is only one team, it adds a team of bots.
  Optional field `teams` to ask for a number of bot teams. Maximum 9 bots (10 seats).
- New request `remove_bot {player}` (creator only, lobby only). Removing one half of a bot pair removes the pair.
- A bot's id is `bot:<slug>`. The sign-in code must never return an id starting with `bot:`. The real start.ng ids are numbers and dev ids start with `dev:`, so there is no clash, but this needs a test.
- Player info and the room list gain `bot` (true for a bot) so clients can tag them. The room list gains a `bots` count.
- Names and looks: each bot gets a name from the list below and a character in the same `style` shape that start.ng players have, so the frontend draws it unchanged. Names are picked at random without repeats inside a room.

### Names (draft, to be pruned by the author)

Real-sounding, not famous, spread across Nigeria's languages. Each name should be checked by a speaker of the language before it ships, for spelling and for unintended meanings.

- **Yoruba:** Folasade, Temitope, Ayodele, Morenikeji, Titilayo, Omolara, Adewale, Oluwaseun, Babatunde, Iyabo, Kolawole, Yetunde.
- **Igbo:** Chidiebere, Ngozi, Nneka, Ifeanyichukwu, Adaeze, Onyekachi, Uchenna, Obiageli, Chukwuemeka, Ebele.
- **Hausa and Fulani:** Hauwa'u, Binta, Danjuma, Gambo, Hajara, Zakari, Rabi'u, Maryam, Balarabe.
- **Efik, Ibibio, Annang:** Ekaette, Mfon, Idara, Emem, Unwana, Edidiong.
- **Edo, Urhobo, Ijaw, Tiv:** Osaze, Eghosa, Ehi, Ejiro, Oghenekaro, Terkimbi, Aondofa.

Short forms such as "Tope", "Ify" or "Dede" can be the display name. Open question: should the lobby and table show a small "bot" tag? The author wants the names convincing. I recommend a small, always-visible tag, so nobody is misled about who they are playing, and since the games don't count for stats anyway nothing is gained by hiding it. This is the author's call.

## What a bot has to be able to do

A starting design. Every number below is a config value to tune.

1. **Ready.** In the pre-round team chat, post to the team (below), then click Ready after a short pause.
2. **Pass.** Pick the card type it is collecting (the one it holds most of, with random tie-breaks) and pass the type it holds fewest of. Partners may collect the same type. If three of its own turns go by without being given a card of the type it is collecting, it gives that type up for the round, passes that stack first and collects something else. This is the same idea as the server's own auto-pass, with a bit of variety. Pass within a few seconds to 20 seconds, well inside the 60 second hold timer.
3. **Reveal and forced pass.** When suspected, show the least revealing cards, as the server does when a human times out. Answer prompts within the 30 second window.
4. **Signal.** Send real signals following the team's code, and occasional fake signals to bait opponents into wasting suspicion. Respect the signal rate limit with room to spare.
5. **Proclaim.** Call Jackpot for the partner when the code says the partner has four of a kind, the partner is not holding the passing card, and the bot is not locked out. Never call on a hunch: a false call costs a point, and a second one at 0 points costs three minutes. It always waits 10 to 30 seconds after its partner's signal before calling, so the call never lands straight after the signal.
6. **Suspect.** A bot suspects opponents, human or bot alike, and never its own partner or team. It spends at most about 2 of its 3 points a round (more only on a strong tell), and each reason below is looked at once, when it happens, then used up:
   - **The same gesture again.** An opponent who sends the same gesture twice in a minute (50% chance to act), or three times or more (80%).
   - **A gesture they won with.** An opponent who sends a gesture they sent in the turns just before their team won an earlier round (70%). A bot only learns this from rounds already played in this game.
   - **A sudden start.** An opponent who has not signalled for 5 turns (a turn is one pass of the card), or at all this round once 5 turns have passed, and then does (40%).
   - **The passing card.** About one pass in ten makes someone at the table suspect whoever just got the card: a 10% chance for the table, shared between the bots on the other team, so it does not grow with the number of bots.
   - **Not every time.** Whatever the reason, a bot lets it go by 40% of the time. The bots take turns in a random order and wait a little extra after the reason, and two of them never suspect the same player at once.
7. **Timing.** Never act instantly. A bot takes a random **5 to 37 seconds** to do anything (the author's choice), except for questions the server only waits 30 seconds for (what to show when suspected, which card to give up after being caught), where it answers within 5 to 25 seconds. It signals that it is ready only after it has posted its code, so a person can read it first. Two bots never act at the same moment.
8. **Chat.** Light, rare public-chat lines, in a natural voice. Off by default; the team chat is the one that matters.

### Partnering: a code a person can read

The game is about partners telling each other things through signals, and 80% of signals are seen by the whole table. If bots shared hands internally, bot teams would be perfect, so they must use the real channel too.

- **Bot with bot:** the pair invents a small code each round and uses it through the normal signal channel. Opponents can read it, and humans can learn to read it. That is intended.
- **Bot with a human:** before each round the bot posts its code in the team chat, in plain sentences a person can follow and remember, for example:
  - "Oya, this round: I'll cough when I'm one card from four. I'll tap the table when I've got four. If you've got four, nod and I'll call Jackpot."
  - A code is two or three signals, picked from easy, common ones (the examples shown by the Signal ideas panel are a good pool), changed every round.
  - Messages come from a few templates with variety, in a friendly voice with a little Nigerian flavour, never robotic and never over the top.
- The bot reads the human partner's signals only by the code it announced, and calls Jackpot when the human's "I have four" matches and the partner is not the holder.
- A bot that gets no signal from a quiet human partner simply plays its own game and does not guess.

### Difficulty

Start with one level. A second level (sharper suspicion, better bluffing, less predictable codes) can follow once there is something to compare against.

## Rules for the rest of the system

- **Liveness.** Bots never disconnect and never time out. The room must ignore them when it decides whether anyone is connected: a room with only bots closes after the usual empty period (20 minutes in a lobby, pause after 1 minute in a game), and bots are never part of the reconnect window.
- **Leaderboards.** A game with any bot has `counts_for_stats = false`. Bots are never written to the `players` table and are marked in `game_players` (new column `is_bot`, a migration). Leaderboard queries exclude bot rows. A test against Postgres must prove a game with bots changes no leaderboard.
- **Restarts.** A restored paused game brings its bots back at once, connected. A bot's code for the current round is not saved; if the server restarts mid-chat the bot just posts a fresh code.
- **Late join.** Bots do not fill the waiting line. Only humans wait in it.
- **Limits.** At most 9 bots in a room, and a cap on rooms with bots per account, so nobody can use bots to burn server time.
- **Drop-outs.** If a human drops out, their team is removed as usual, bot partner included.
- **No new rules.** The engine does not change.

## Protocol changes

- Requests: `add_bots {teams?}`, `remove_bot {player}`.
- Player info: `bot` (bool). Room list: `bots` (count).
- Errors: `not_creator`, `wrong_phase` (not in the lobby), `room_full`, `too_many_bots`.
- Everything else, messages and events included, is as for human players.

## Testing

The biggest benefit: **bot-only games become a soak test for the whole server.**

- **Simulation.** Thousands of bot-only games with a fake clock. Every game must end, no engine invariant may break, nothing may deadlock (a phase that makes no progress for a long time fails the test), and the average number of rounds and the bluff rate are logged so tuning is measurable.
- **Fairness by structure.** The brain package imports only the exported snapshot and message types. A test checks it cannot see another player's hand, and a game-replay test checks the bot's decisions use nothing but its own view.
- **Decision tests.** Hand-built snapshots check each decision: passing, revealing, proclaiming only when the partner is not the holder, never proclaiming while locked out, never suspecting its own partner, spending at most 3 points a round.
- **Adversarial.** A human partner who never signals; a human who signals the wrong thing; a human who disconnects and returns; pause and resume with bots; a restart with bots; the lockout; a late join into a game with bots; a room whose humans all leave; `add_bots` by a non-creator; `add_bots` twice; the sign-in rejecting a `bot:` id; a flood of `add_bots` and `remove_bot` requests.
- **Leaderboards** against a real Postgres, as above.
- The race detector over all of it.

## Frontend needs

An **Add bots** button (creator, lobby only), a **remove** button on a bot, the bot tag if the author wants one, and nothing else new: bots use the same messages as players. The Signal ideas panel is a natural place to show what the bot's code means.

## Effort, in rough order

1. **Groundwork** (lobby requests, identity and names, liveness, stats exclusion, tests): 1 to 2 days.
2. **A bot that plays legally and sensibly** (ready, pass, reveal, forced pass, a bot-only simulation harness): 2 to 3 days.
3. **Signals, partnering, Jackpot calls and suspicion,** including the readable code with a human partner: 3 to 4 days. This is the long part, and the part players will notice.
4. **Frontend, names review, polish:** 1 to 2 days.
5. **Tuning:** ongoing, helped a great deal by the simulation.

## What was built

- `internal/botname`: the 52 names, bot ids (`bot:<name>`), and each bot's look.
- `internal/bot`: the driver and the brain. `add_bots` and `remove_bot` in the room, with the rules above.
- `engine.Config.NoStats`: a game with bots never counts. The duo and MVP boards also leave out every game that has a bot in it, even if it were marked as counting.
- The REST API shows a bot's name and look, though a bot has no profile row.

## Still to do

- Frontend: the Add bots button (creator, lobby), the visible bot tag, a remove button, and a way to show what a bot's code means.
- Tuning against real people. In simulation (whole games played with a fake clock, at a person's pace) a short game takes about 7 rounds and 16 minutes, with a handful of suspicions a game. How hard that feels to a human partner who reads the code badly is not known yet.
- The names should be checked by speakers of each language.
