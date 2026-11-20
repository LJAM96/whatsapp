# Features & roadmap

This is the Weave fork of mautrix-whatsapp. The first list is upstream's
feature list, kept as upstream maintains it (items marked "Weave fork" were
added here). The second part lists what this fork adds, what is in progress,
and what WhatsApp's protocol does not allow.

## Upstream features
* Matrix → WhatsApp
  * [x] Message content
    * [x] Plain text
    * [x] Formatted messages
    * [x] Location messages
    * [x] Media/files
    * [x] Replies
    * [x] Polls
    * [x] Poll votes
  * [x] Message redactions
  * [x] Reactions
  * [x] Presence
  * [x] Typing notifications
  * [x] Read receipts
  * [x] Power level (admin promote/demote, announce-only, locked; Weave fork)
  * [x] Membership actions
    * [x] Invite
    * [x] Leave
    * [x] Kick
  * [ ] Room metadata changes
    * [ ] Name
    * [ ] Avatar
    * [ ] Topic
  * [ ] Initial room metadata
* WhatsApp → Matrix
  * [x] Message content
    * [x] Plain text
    * [x] Formatted messages
    * [x] Media/files
    * [x] Location messages
    * [x] Contact messages
    * [x] Replies
    * [x] Polls
    * [x] Poll votes
  * [ ] Chat types
    * [x] Private chat
    * [x] Group chat
    * [x] Communities
    * [x] Status broadcast
    * [ ] Broadcast list (not currently supported on WhatsApp web)
  * [x] Message deletions
  * [x] Reactions
  * [x] Avatars
  * [x] Presence (DM contacts, opt-in `bridge_contact_presence`; Weave fork)
  * [x] Typing notifications
  * [x] Read receipts
  * [x] Admin/superadmin status
  * [x] Membership actions
    * [x] Invite
    * [x] Join
    * [x] Leave
    * [x] Kick
  * [x] Group metadata changes
    * [x] Title
    * [x] Avatar
    * [x] Description
  * [x] Initial group metadata
  * [x] User metadata changes
    * [x] Display name
    * [x] Avatar
  * [x] Initial user metadata
    * [x] Display name
    * [x] Avatar
* Misc
  * [x] Automatic portal creation
    * [x] After login
    * [x] When added to group
    * [x] When receiving message
  * [x] Private chat creation by inviting Matrix puppet of WhatsApp user to new room
  * [x] Option to use own Matrix account for messages sent from WhatsApp mobile/other web clients
  * [x] Shared group chat portals

## Weave fork additions

Every addition has its own branch so it can be offered upstream. Matrix → WhatsApp
items marked † need the Weave mautrix-go fork
([LJAM96/mautrix-go](https://github.com/LJAM96/mautrix-go), branch
`weave/bridgev2-routing-33abe66`), which `go.mod` already points at. The
Weave-specific control events (`com.lukemulvaney.weave.*`) are accepted only
from the logged-in user's own Matrix account.

### Merged

| Feature | WhatsApp → Matrix | Matrix → WhatsApp |
| --- | --- | --- |
| Contact cards (vCard files as native contact messages, several cards per file) | upstream | [x] |
| Call history notices (missed, declined, durations; `call_history_notices`), including call logs from history sync | [x] | – |
| Pinned messages (`m.room.pinned_events`) | [x] | [x] † |
| Group admin: promote/demote, announce-only, locked | [x] | [x] |
| Group join approval (`knock` join rule) | [x] | [x] † |
| Video notes (round videos, `fi.mau.whatsapp.video_note`) | [x] | [x] |
| View-once media (view-limited marker; redacted after the client reports the view) | [x] | [x] (view-once video notes are rejected) |
| Contact presence (online / last seen; opt-in, at most 100 contacts) | [x] | upstream |
| "Recording audio" (shown as typing) | [x] | – |
| Event RSVPs and `.ics` calendar files | [x] | [x] (👍/👎/🤷 on an event message) |
| Starred messages (`fi.mau.whatsapp.starred` room account data) | [x] | `!wa star` / `!wa unstar` |
| Live location (MSC3672 beacons, updates and stop) | [x] | – |
| Archive, pin, mute, mark unread (`com.lukemulvaney.weave.chat_settings`) | upstream (tags) | [x] † |
| Business labels (room tags `u.wa.<label>`) | [x] | – |

### In progress (branches, not merged)

| Feature | Direction | Branch |
| --- | --- | --- |
| Stars and labels from Weave (`com.lukemulvaney.weave.star`, `.label`) | Matrix → WhatsApp † | `feat/weave-stars-labels` |
| Live location from Weave; shares kept across restarts | Matrix → WhatsApp † | `feat/live-location-send` |
| Recording indicator from Weave (`com.lukemulvaney.weave.recording`) | Matrix → WhatsApp † | `feat/recording-signal` |
| Channels (newsletters): follow, read, react | both | `feat/newsletters` |
| Communities as Matrix spaces with linked groups | WhatsApp → Matrix | `feat/communities` |
| Status updates in a "WhatsApp Status" room (opt-in) | WhatsApp → Matrix | `feat/status` |
| Business catalogs (read only, if whatsmeow supports it) | WhatsApp → Matrix | `feat/business-catalog` |
| Group creation and invite links | Matrix → WhatsApp | `feat/group-create` |

### Not possible through the bridge

* Sending to broadcast lists: WhatsApp Web cannot.
* Usernames: not exposed by the protocol.
* Voice and video calls: deliberately not in the bridge. Weave handles them in
  the separate `weave-calls` service, which links as a second WhatsApp device.

### Known limits

* Matrix room account data (stars, labels, tags) is never delivered to
  appservices by Synapse, so changes made in a Matrix client reach WhatsApp only
  through the Weave control events above.
* Live-location shares started on WhatsApp are kept in memory until
  `feat/live-location-send` lands durable storage.

