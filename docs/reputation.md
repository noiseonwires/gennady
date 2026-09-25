# Reputation and Karma

Gennady's reputation system is an event-based karma mechanism. It combines community
reactions, confirmed moderation actions, clean-behavior recovery, and an optional small AI
profile adjustment into a bounded score.

The system is enabled by default. Set `reputation.enabled: false` to opt out.

## Principles

1. **Neutral is the stable state.** Evidence decays toward zero, so old behavior does not
   permanently define a user.
2. **Reputation is chat-local.** A user has a separate score in every moderation chat because
   communities can have different standards. Only the AI profile adjustment is global.
3. **Scores are bounded.** Per-message, per-component, and final-score limits prevent unlimited
   growth in either direction.
4. **One incident has one penalty.** When the same message is deleted, warned, and muted, only
   the highest-severity confirmed action contributes.
5. **Moderator corrections remove evidence.** Exoneration and action reversal update the score
   instead of leaving an obsolete penalty behind.
6. **Recovery repairs damage but does not create trust.** Clean messages can bring a negative
   score back to zero, but cannot produce a positive score by themselves.
7. **AI is supporting evidence, not the source of truth.** Its latest profile grade contributes
   only a small, replaceable adjustment.
8. **AI moderation cannot reinforce itself.** Automatic actions performed from an AI verdict
   remain in the audit log but do not affect reputation. This avoids the loop: low reputation
   -> stricter AI verdict -> lower reputation.
9. **Telegram profile screening is a persistent characteristic.** Current screening findings
   contribute globally without decay, but their combined influence is capped.

## Score Calculation

Each event stores raw evidence. Its configured weight and half-life are applied when the score
is read, so changing configuration immediately recalculates existing evidence.

For an event of age $t$ days and half-life $h$ days:

$$
E(t)=\text{raw value}\times\text{weight}\times 2^{-t/h}
$$

A half-life of `0` disables decay for that event type. Per-event limits are applied before
decay. The resulting events are summed into five components:

- `community`: positive and negative reactions;
- `moderation`: confirmed deletes, warnings, and mutes;
- `recovery`: clean-message recovery;
- `ai`: the latest AI behavior-profile assessment;
- `profile_screening`: persistent Telegram profile-screening findings.

Component caps are applied next. Recovery is then limited to the amount needed to bring all
non-recovery evidence back to zero. Finally, the total is clamped to the configured score
range:

$$
S=\operatorname{clamp}(S_{min},S_{max},C+M+A+P+\min(R,\max(0,-(C+M+A+P))))
$$

With the defaults, $S_{min}=-30$ and $S_{max}=30$.

## Default Events

| Evidence | Default weight | Half-life | Additional rule |
|---|---:|---:|---|
| Positive reaction | `+0.20` each | 90 days | Maximum `+1` per message |
| Negative reaction | `-0.25` each | 90 days | Maximum `-1` per message |
| Confirmed delete | `-3` | 45 days | Incident severity 1 |
| Confirmed warning | `-5` | 90 days | Incident severity 2 |
| Mute under 24 hours | `-8` | 180 days | Incident severity 3 |
| Mute of at least 24 hours | `-10` | 180 days | Incident severity 4 |
| Permanent mute | `-15` | 180 days | Incident severity 5 |
| Clean recovery interval | `+1` | No decay | Recovery only, maximum `+5` |
| Telegram profile text finding | `-2` each | No decay | Global component, shared minimum `-5` |
| Content Safety profile/channel photo finding | `-3` each | No decay | Global component, shared minimum `-5` |
| AI `very_bad` | `-3` | 30 days | Latest AI assessment replaces the previous one |
| AI `bad` | `-1.5` | 30 days | Latest AI assessment replaces the previous one |
| AI `neutral` | `0` | 30 days | Latest AI assessment replaces the previous one |
| AI `good` | `+1.5` | 30 days | Latest AI assessment replaces the previous one |
| AI `very_good` | `+3` | 30 days | Latest AI assessment replaces the previous one |

Default component limits are:

| Component | Minimum | Maximum |
|---|---:|---:|
| Community reactions | `-8` | `+24` |
| AI profile | `-3` | `+3` |
| Telegram profile screening | `-5` | `0` |
| Clean recovery | `0` | `+5` |
| Final score | `-30` | `+30` |

Moderation evidence has no separate component cap, but the final score cap still applies.

## Grades

The default score-to-grade mapping is:

| Score | Grade |
|---:|---|
| $S \le -20$ | `very_bad` |
| $-20 < S < -5$ | `bad` |
| $-5 \le S \le 5$ | `neutral` |
| $5 < S < 20$ | `good` |
| $S \ge 20$ | `very_good` |

All four boundaries are configurable under `reputation.thresholds` and must remain in ascending
order. Invalid ordering falls back to the default boundaries.

## Reactions

Telegram reaction totals are treated as authoritative snapshots, not additive events. Updating
or removing reactions replaces the previous contribution for that message, which makes webhook
retries idempotent.

Default positive reactions are `👍`, `❤`, `🔥`, `👏`, `🎉`, `😁`, and `🤩`. Default negative
reactions are `👎`, `😡`, `🤬`, `💩`, `🤡`, and `🤮`.

All reactions configured for the bot's own status indicators are excluded, even if the same
emoji appears in a positive or negative list. This prevents a moderation reaction from counting
the same incident twice. Emoji variation selectors are ignored during matching.

Reaction evidence is attributed to the message author. The score does not identify or rate the
people who reacted, and therefore cannot independently detect coordinated reaction brigading.
The community component and per-message caps limit that risk.

## Moderation Incidents and Reversals

An incident is normally identified by user, chat, and message. A later action of greater or equal
severity replaces the current incident event; a lower-severity action cannot replace a higher
one. Actions that are not tied to a message receive a unique time-based incident key.

Moderator corrections behave as follows:

| Correction | Effect on reputation |
|---|---|
| `cleared` / Not a Violation | Removes the complete incident event |
| `restore` | Removes the incident only when its current event is a delete |
| `warning_removed` | Removes the incident only when its current event is a warning |
| `unmute` | Does not erase a valid historical mute penalty |

A newly confirmed incident resets previously earned clean-recovery evidence for that user and
chat.

## Clean-Behavior Recovery

Recovery is considered only while the current score is negative. By default:

- every 50 clean messages emits one `+1` recovery event;
- at most 20 messages per UTC day advance recovery progress;
- unused progress carries across days;
- total recovery is capped at `+5`;
- effective recovery can never raise the score above zero.

A clean message is a newly received, non-edited message for which the moderation pipeline did
not trigger an action. Raising the daily limit makes recovery faster but also makes message spam
more effective; lowering `clean_messages_per_point` should therefore be done conservatively.

## AI Profile Adjustment

Scheduled AI behavior profiling may still return `very_bad`, `bad`, `neutral`, `good`, or
`very_good`. That grade is stored as one global event per user. A new profile run replaces the
previous AI event instead of accumulating another adjustment.

The AI adjustment applies to the user's score in every chat, but its default component cap is
only `[-3,+3]`. Community and confirmed moderation evidence therefore remain authoritative.
Set `reputation.ai_profile_affects_score: false` to keep the AI verdict visible as a separate
reputation without including it in the event-based score.

## Telegram Profile Screening

Each non-empty line in `user_profiles.tg_profile_analysis` contributes persistent global
screening evidence. The default weight is `-2` per suspicious text finding and `-3` per
Content Safety profile/channel photo finding; their shared component is clamped at `-5`. It has
no half-life because it represents the stored profile characteristic, not historical chat behavior.

Repeated identical findings are deduplicated and do not accumulate. Existing installations are
backfilled from stored findings at startup and after database import. Deleting the user profile
removes this component while leaving reaction and moderation history intact.

## Configuration

Every weight, half-life, cap, threshold, recovery limit, and reaction set can be changed under
the top-level `reputation` section. Explicit numeric zeroes are respected, so setting a weight
to `0` disables that signal without disabling the entire mechanism.

See [config.example.yaml](../config.example.yaml) for a complete example and
[CONFIG_REFERENCE_en.md](CONFIG_REFERENCE_en.md) for every key and environment variable.

## Prompts and Web UI

The following placeholders are available in content-moderation and creative-reply prompts:

- `{{user_reputation}}`: compact grade-and-score block plus relevant profile-screening notes;
- `{{reputation_score}}`: numeric chat-local score with one decimal place;
- `{{reputation_grade}}`: `very_bad`, `bad`, `neutral`, `good`, or `very_good`.

Warning prompts support the same rich `{{user_profile}}` and compact `{{user_reputation}}`
blocks, plus `{{reputation_score}}` and `{{reputation_grade}}`; the legacy `{{reputation}}`
placeholder resolves to the calculated score grade.

The Profiles page and admin profile reports show both reputations: the stored AI profile verdict
and the current grade/numeric score for every configured moderation chat. Scores are calculated
when requested, so configuration and decay changes are visible without a backfill job.

## Persistence

`reputation_events` stores replaceable evidence, and `reputation_clean_state` stores partial
clean-message recovery progress. Both tables are included in database export, import, and backup
round trips. Stable source keys make reaction updates, callback retries, and repeated AI profile
runs idempotent.