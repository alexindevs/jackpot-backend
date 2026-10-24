# Jackpot: game rules (backend source of truth)

Status markers:
- **Decided**: stated by the game's author.
- **Provisional**: author leans this way, not final.
- **Assumed**: inferred while writing this doc. Needs confirmation.
- **Open**: not decided yet.

Anything not marked is Decided.

---

## 1. Setup

- 2 to 5 teams, 2 players each (4 to 10 players).
- 4 card types.
- Every player starts with one card of each type. One player also gets an extra card of one type, so they start with 5 cards. That extra card is the **passing card**.
- Copies per type: **players + 7** (11 for 2 teams, 17 for 5 teams). Cards not dealt form the **pool**. Passing never draws from the pool, so the spares only matter for resets.
- **Only signed-in start.ng players can play.** Guests can't.
- Before **every round**, each team gets a **private chat for 2 minutes** to agree a signalling code. Teams are expected to use a fresh code each round, since reusing signals lets opponents learn them. This is strategy advice, not an enforced rule.
- Players can click **Ready** to skip the rest of the chat, but **everyone** has to click it. If anyone hasn't, everyone waits out the 2 minutes.
- This is the **only** private channel. Once the round starts, partners can only coordinate through signals (section 5) or the public chat (section 10).

## 2. Passing

- There is exactly one passing card in play. Whoever holds it has 5 cards.
- The holder passes one card to the next player. Typically they pass a card of a type they hold the fewest of.
- **Hold timer:** after 60 seconds, the server passes a card for the holder: the one whose type the holder has the fewest copies of. Ties are broken randomly.
- **Ring order:** players are seated around a ring in random order, with one constraint: **a player never has their partner directly on either side**. With 2 teams this forces alternating seats.
- Seating stays the same across rounds.
- If a team is removed mid-game, the ring closes up. If that puts partners next to each other, they stay that way for the rest of the round, and players are **reshuffled before the next round** (respecting the no-adjacent-partners rule).

## 3. Winning a round

- Hold 4 cards of the same type.
- Your **partner** proclaims "Jackpot". You can never proclaim for yourself.
- A proclamation is **rejected, with no penalty**, while the player who would win (the proclaimer's partner) holds the passing card (has 5 cards).
- At any other time a proclamation is accepted. If it's **false** (the proclaimer's partner doesn't hold 4 of a kind), the proclaimer **loses one suspicion point**.
- A false proclamation made at **0 points** removes the proclaimer's right to proclaim **for the rest of that round**. The right comes back next round.
- A winning team earns the next letter of J-A-C-K-P-O-T (one round win = one letter).
- A new round **redeals**, and **suspicion points refresh**. Each round begins with the 2-minute private team chat (see section 1).
- The player who starts with the extra (passing) card is **picked at random** each round. Seating is not reshuffled between rounds, except as described in section 2.

## 4. Suspicion

- Every player has **3 suspicion points per round**, refreshed at the start of each round. Once they're used up you can't suspect anyone until the next round.
- You can suspect **anyone at any time**, including your own partner (allowed, but a bad idea) and players who don't hold 4 of a kind.
- The **suspector always loses the point**, whether or not the suspicion was right.
- The suspected player must immediately show cards of different types:
  - If they hold **4 cards**: show **2** different types.
  - If they hold **5 cards**: show **3** different types.
  - This is judged on the hand **as of the suspicion's position in the event log**. If the player passed one millisecond before the suspicion, they hold 4 cards and show 2.
  - A 5-card hand with 3+2 has only 2 types, so it fails. This is intended.
- Shown cards are **revealed to the room**. Leaking hand info is part of the game.
- When a player fails the check, the room sees the hand they were holding.
- The suspected player chooses which cards to show, within **30 seconds**. If they don't, the server shows the types they hold the **fewest** of, so it gives away as little as possible and hides what they're collecting. (If they can't show enough different types, they were already caught when the suspicion was raised.)
- If they can't show enough different types, they **reset**:
  - Their cards go back to the pool and they get a fresh hand of **exactly one card of each type**, always.
  - *(Assumed: if the pool ever lacks a type, that card is created, so the rule never bends. The deck is rebuilt every round, so this never carries over. With players + 7 per type it didn't happen through 5 resets in a round in simulation.)*
  - If they hold the passing card, they **pass it to the next player first, then reset**. With 5 cards of one type, they pass one and reset the rest.
  - The player is **prompted to choose** which card to pass, within **30 seconds**. If they don't, the server passes the odd-one-out card as in section 2.
- If the partner's proclamation is processed **before** the suspicion, the team wins and the suspicion no longer matters.

## 5. Signalling

- Players send gestures from a **fixed vocabulary of about 200** ordinary human things: sneezing, blinking once, twice or thrice, rubbing your nose, a belch, a hiccup, humming, saying "oya", "abeg" or "wetin dey", singing a line, mentioning jollof. They are listed in `internal/room/gestures.go`, in groups (eyes and face, noises, hands, posture, food and objects, singing, phrases, random behaviour). Fake signals are allowed and use the same mechanism as real ones. The server doesn't tell them apart. Only the players have to.
- Signals only work **while a round is running**, not in the pre-round chat. Only players still in the game can send them.
- **Visibility is rolled once per signal:**
  - 20% of signals go to the sender's partner only (`team` scope).
  - 80% go to everyone, including the partner and spectators (`room` scope).
  - Both numbers are config values (`TeamSignalChance`, the vocabulary).
- *(Assumed: the sender is **not told** which scope was rolled, the way you can't know who noticed a wink at a real table. Their own copy of the signal carries no scope.)*
- *(Assumed: a player can signal about once a second, in bursts of 3. Config values.)*
- *(Assumed: the list is a first draft. Adding or removing gestures is a one-line change.)*

## 6. Concurrency

- Each room has a **single ordered event queue**. Whichever event the server receives first wins. This replaces the in-person vote.
- Order is **arrival order** at the server. There is no collection window and no random tie-break. Only contested actions (suspicions and proclamations) are ever close enough to matter.

## 7. Ending the game

- First team to **7 round wins** (J-A-C-K-P-O-T) wins.
- Or the game ends early if a **majority of teams** agree. A team counts as agreeing only when **both partners** agree.
- At early end, the team with the most wins wins. A tie is a **drawn game**: no winner.

## 8. Dropping out

- A team can drop out at any time. After a **1 minute timeout**, if the leaving player holds the passing card it's passed on, and the **whole team is removed** (both partners). Their cards go **back to the pool**. The remaining teams continue.
- If only **one team is left, it wins by forfeit**. A game needs at least 2 teams.
- A player who **disconnects** gets a **90 second reconnect window**. If they haven't reconnected by then, their team is removed as above.
- A **deliberate** drop-out removes the team **immediately**.
- If **nobody** is connected for **20 minutes**, a lobby closes, so a lobby link keeps working while people step away. A game that has started is **paused** instead, after **1 minute** (section 13). *(The game's wait has to stay under the 90 second reconnect window, or the teams would be dropped before the game could pause.)*
- A forfeit win **does not count** toward leaderboard or duo stats.

## 9. Stats and leaderboards

- **MVPs:** players who win mostly by proclaiming, or mostly by holding the 4 cards.
- **Legendary duos:** partners with many wins together.
- This means every round win must record who **proclaimed** and who **held the cards**, and the team.
- **Open:** accounts and persistence, which are needed for leaderboards.

## 10. Public chat

- There is a **live public chat**, shown in a pane beside the main view while players pass cards.
- Everyone in the game sees it. It is a normal part of play: distractions, bluffs, rules arguments and accusations all happen here.
- Partners may use it for coded messages, knowing opponents can read it.
- **Spectators and removed players can read it but can't post.**
- Messages are **stored until 24 hours after the game ends**, so disputed wins can be checked.
- It's expected to get spammy. **Moderation is out of MVP scope** and planned soon after.
- Delivered over the **WebSocket**, with a **polling fallback about once a minute** through the REST API for a client whose socket is down.
- *(Assumed: it opens when the game starts, because chat is stored against a game, and stays open through the rounds and after the game ends, so a disputed win can be argued. There is no public chat in the lobby.)*
- *(Assumed: each player can post about once a second, in bursts of up to 5, and a line is 1 to 500 characters on one line. These are config values.)*

## 11. Spectators

- Spectators can read the public chat (section 10) but not post.
- **Temporary perk:** spectators can **view every player's cards**. This is expected to be removed once people start abusing it.
- **Open:** whether removed players also see everyone's cards, or only the chat.

## 12. Rooms and lobby

- A player can **view running rooms** to spectate, **create** a room and invite people to play, **invite a specific person to be their partner**, or **join** a room and invite a partner (or anyone else).
- **4 players** are needed to start. The room **stops accepting joiners at 10** (enough for 5 teams).
- A **2 minute countdown** starts when 4 players have joined. This is separate from the pre-round chat. When it ends, the game starts. If the room drops below 4 the countdown cancels, and it restarts from a full 2 minutes at 4 again.
- The room's **creator** can **skip the countdown** once there are at least 2 teams and **nobody is left without a partner**. Nobody else can. *(Assumed: if the creator leaves the lobby, nobody can skip; the countdown still runs.)*
- A **full room (10 players, five teams) starts immediately**, without waiting for the countdown. Anyone still without a partner is paired at random.
- Anyone who hasn't picked a partner by then is **randomly paired with another free player**.
- Only signed-in start.ng players can join as players.
- If one free player is left over without a partner, they become a spectator. If they don't want that, they should pick a partner in time.
- *(Assumed: accepting a partner invite also joins you to the room. Leaving the lobby frees your partner. A lobby player who disconnects gets the same 90 second window, then is removed.)*
- **Open:** how a player finds or is told about invitations when they aren't in the room; the list of running rooms and who may see it.

---

## 13. Pausing and resuming

- A game that has started is never lost to a restart or a bad night of connections. It is **saved as it is played** (the setup and every action), and the server can rebuild it exactly.
- A game is **paused** when the server restarts, or when nobody has been connected for 1 minute. While paused nothing moves: no timers run, nobody can pass, suspect, proclaim, vote, ready up or signal. Players can still sign in, chat publicly and read.
- A player finds their paused games in a list (`my_games`) and goes back to one (`resume_game`). A paused game is **not** the player's current room, so they can use the app meanwhile.
- When **every player still in the game** is back, it **resumes**. Timers start fresh: the holder gets a new 60 second hold timer, and between rounds the 2 minute team chat starts over.
- A paused game waits **24 hours** for its players (counted from when it was last active), then is **abandoned**. A player who leaves for good while it is paused drops their team, as usual.
- The server itself pauses games; players cannot pause one on purpose. *(Assumed. Add a vote or a button if wanted.)*
- Kept for **24 hours after a game ends**: the saved actions, the setup and the public chat, for settling disputes. The summary (who played, rounds, results) is kept **for good**, because leaderboards are built from it.
- **Lobbies are saved too.** A lobby that was open when the server restarted comes back with the same members, partners and open partner invitations, **paused**: its members find it in their paused list, and the countdown starts again (a full 2 minutes) once every member is back. While it is paused, nobody new can join and the creator can't skip the countdown, but members can still pair up. A restored lobby that nobody returns to is closed after **1 hour** (`LobbyPausedFor`). *(Assumed: shorter than a game's 24 hours, since a lobby nobody comes back to is just clutter.)* A lobby that empties out on a running server closes after 20 minutes (`LobbyEmptyGrace`).
- **Private team chat is saved**, line by line, and kept until 24 hours after the game ends like the public chat. A player coming back sees their own team's chat again (in the snapshot). Other teams and spectators never do.
- *(Not restored: spectators (they watch again) and signals already sent. Message numbers (`s`) start again from zero after a restart; see protocol.md.)*

---

## Known risks (not rules)

- **Pool exhaustion (resolved):** with the old 11 cards per type, 5-team games ran short of a type on about 76% of first resets, which would have forced hands that weren't one of each. The deck is now players + 7 per type. In the engine's simulation (a caught player holding four of one type, built by trading with other players), that removed every shortfall through 5 resets in a round.
- **Latency:** arrival order favours players with better connections. Accepted for now.
- **Spectator card view:** a spectator who can see every hand can relay it to a friend in the game (voice call, another chat). Keep this behind a single config flag, and make the hidden-information filter role-aware (player vs spectator), so removing the perk is a one-line change.
