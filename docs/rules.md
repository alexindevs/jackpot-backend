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
- Copies per type: 11, which covers the 5-team maximum. Cards not dealt form the **pool**.
- Before **every round**, each team gets a **private chat for 2 minutes** to agree a signalling code. Teams are expected to use a fresh code each round, since reusing signals lets opponents learn them. This is strategy advice, not an enforced rule.
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
- **Provisional:** a false proclamation made at 0 points removes the proclaimer's right to proclaim for the rest of the game. The alternative is a separate points pool for proclamations. Not decided.
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
- *(Assumed: the suspected player chooses which cards to show, with a server-side check and a timeout fallback.)*
- If they can't show enough different types, they **reset**:
  - Their cards go back to the pool and they get a fresh hand of one card of each type.
  - If they hold the passing card, they **pass it to the next player first, then reset**. With 5 cards of one type, they pass one and reset the rest.
  - The player is **prompted to choose** which card to pass. *(Provisional. Assumed: if they don't choose before the hold timer runs out, the server passes the odd-one-out card as in section 2.)*
- If the partner's proclamation is processed **before** the suspicion, the team wins and the suspicion no longer matters.

## 5. Signalling

- Players send gestures from a **fixed vocabulary**. Fake signals are allowed and use the same mechanism as real ones. The server doesn't need to tell them apart. Only the players have to.
- **Visibility is rolled once per signal:**
  - 20% of signals go to the sender's partner only (`team` scope).
  - 80% go to everyone, including the partner (`room` scope).
  - Both numbers should be config values.
- **Open:** the gesture vocabulary itself.

## 6. Concurrency

- Each room has a **single ordered event queue**. Whichever event the server processes first wins. This replaces the in-person vote.
- A short **collection window** of roughly 100 to 200 ms, with a **random tie-break** for near-simultaneous events. This is a tunable starting value, not a final one.

## 7. Ending the game

- First team to **7 round wins** (J-A-C-K-P-O-T) wins.
- Or the game ends early if a **majority of teams** agree. A team counts as agreeing only when **both partners** agree.
- At early end, the team with the most wins wins. A tie is a **drawn game**: no winner.

## 8. Dropping out

- A team can drop out at any time. After a **1 minute timeout**, if the leaving player holds the passing card it's passed on, and the **whole team is removed** (both partners). Their cards go **back to the pool**. The remaining teams continue.
- If only **one team is left, it wins by forfeit**. A game needs at least 2 teams.
- *(Assumed: disconnects follow the same path as dropping out.)*
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
- Messages are **stored for 24 hours**, so disputed wins can be checked.
- It's expected to get spammy. **Moderation is out of MVP scope** and planned soon after.
- **Open:** the rate-limit values (a config value is enough for now).

## 11. Spectators

- Spectators can read the public chat (section 10) but not post.
- **Temporary perk:** spectators can **view every player's cards**. This is expected to be removed once people start abusing it.
- **Open:** whether removed players also see everyone's cards, or only the chat.

---

## Known risks (not rules)

- **Pool exhaustion:** resets return cards of one type but need one of each type from the pool. With 11 per type and many resets, the pool can run short of some types. Resets are expected to be rare, but the engine should handle an empty pool explicitly instead of failing, and keep a per-type card count invariant.
- **Latency:** the ordered queue favours players with better connections, which is why the collection window exists.
- **Spectator card view:** a spectator who can see every hand can relay it to a friend in the game (voice call, another chat). Keep this behind a single config flag, and make the hidden-information filter role-aware (player vs spectator), so removing the perk is a one-line change.
