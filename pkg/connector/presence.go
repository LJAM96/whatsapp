// mautrix-whatsapp - A Matrix-WhatsApp puppeting bridge.
// Copyright (C) 2026 Tulir Asokan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package connector

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/jsontime"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

const (
	// maxPresenceSubscriptions bounds the number of contacts we ask WhatsApp about.
	maxPresenceSubscriptions = 100
	// maxPresenceContactAge is how recently a DM must have been active to be subscribed to on connect.
	maxPresenceContactAge = 7 * 24 * time.Hour
)

// ghostPresenceSetter is the shape of the bridgev2 MatrixAPI presence method once it exists.
// bridgev2 does not expose ghost presence yet, so this is checked as an optional interface.
type ghostPresenceSetter interface {
	SetPresence(ctx context.Context, content *event.PresenceEventContent) error
}

// presenceSharingAllowed checks whether the user's own WhatsApp privacy settings allow
// seeing other users' presence. WhatsApp only shares presence with users who share their
// own last seen time, so a user who hid their last seen from everyone shouldn't get
// other people's presence bridged.
func presenceSharingAllowed(settings types.PrivacySettings) bool {
	switch settings.LastSeen {
	case types.PrivacySettingAll, types.PrivacySettingContacts, types.PrivacySettingContactBlacklist:
	default:
		return false
	}
	return settings.Online == types.PrivacySettingAll ||
		settings.Online == types.PrivacySettingMatchLastSeen ||
		settings.Online == types.PrivacySettingUndefined
}

// presenceToMatrix converts a whatsmeow presence event into Matrix m.presence content.
// A hidden last seen time maps to "unavailable" rather than "offline", as the latter
// would imply that we know when the user was last active.
func presenceToMatrix(evt *events.Presence, now time.Time) *event.PresenceEventContent {
	content := &event.PresenceEventContent{}
	switch {
	case !evt.Unavailable:
		content.Presence = event.PresenceOnline
		content.CurrentlyActive = true
	case evt.LastSeen.IsZero():
		content.Presence = event.PresenceUnavailable
	default:
		content.Presence = event.PresenceOffline
		if ago := now.Sub(evt.LastSeen); ago > 0 {
			content.LastActiveAgo = ago.Milliseconds()
		}
	}
	return content
}

func isPresenceContact(jid types.JID) bool {
	jid = jid.ToNonAD()
	return jid.Server == types.DefaultUserServer || jid.Server == types.HiddenUserServer
}

func (wa *WhatsAppClient) canBridgePresence(ctx context.Context) bool {
	if wa.Client == nil || !wa.Main.Config.BridgeContactPresence {
		return false
	}
	settings, err := wa.Client.TryFetchPrivacySettings(ctx, false)
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to fetch privacy settings for presence bridging")
		return false
	}
	return presenceSharingAllowed(*settings)
}

// subscribeToPresence subscribes to the presence of a recently active DM partner.
func (wa *WhatsAppClient) subscribeToPresence(ctx context.Context, jid types.JID) {
	if !isPresenceContact(jid) || wa.IsOwnJID(jid) || !wa.canBridgePresence(ctx) {
		return
	}
	// Normalize to LIDs so that presence events and ghosts use the same identifier as LID DMs.
	jid = wa.maybeConvertJIDToLID(ctx, jid.ToNonAD()).ToNonAD()
	if wa.IsOwnJID(jid) || wa.presenceSubs.Has(jid) {
		return
	} else if wa.presenceSubs.Size() >= maxPresenceSubscriptions {
		zerolog.Ctx(ctx).Debug().Msg("Not subscribing to contact presence: subscription limit reached")
		return
	} else if !wa.presenceSubs.Add(jid) {
		return
	}
	err := wa.Client.SubscribePresence(ctx, jid)
	if err != nil {
		wa.presenceSubs.Remove(jid)
		zerolog.Ctx(ctx).Warn().Err(err).Stringer("contact", jid).Msg("Failed to subscribe to contact presence")
	}
}

// resubscribePresences restores presence subscriptions after a reconnect and adds
// recently active DM partners from the history sync conversation table.
func (wa *WhatsAppClient) resubscribePresences(ctx context.Context) {
	if !wa.canBridgePresence(ctx) {
		return
	}
	// WhatsApp drops subscriptions when the connection drops, so resend them all.
	for jid := range wa.presenceSubs.Iter() {
		err := wa.Client.SubscribePresence(ctx, jid)
		if err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Stringer("contact", jid).Msg("Failed to resubscribe to contact presence")
		}
	}
	conversations, err := wa.Main.DB.Conversation.GetRecent(ctx, wa.UserLogin.ID, -1, jsontime.UnixNow())
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to get recent conversations for presence subscriptions")
		return
	}
	// GetRecent orders by last message timestamp descending, so the first conversations are the most recent.
	cutoff := time.Now().Add(-maxPresenceContactAge)
	for _, conv := range conversations {
		if conv.LastMessageTimestamp.Before(cutoff) || wa.presenceSubs.Size() >= maxPresenceSubscriptions {
			break
		}
		wa.subscribeToPresence(ctx, conv.ChatJID)
	}
	zerolog.Ctx(ctx).Debug().Int("subscription_count", wa.presenceSubs.Size()).Msg("Updated contact presence subscriptions")
}

func (wa *WhatsAppClient) handleWAPresence(ctx context.Context, evt *events.Presence) {
	if evt == nil || wa.IsOwnJID(evt.From) {
		return
	}
	jid := wa.maybeConvertJIDToLID(ctx, evt.From.ToNonAD()).ToNonAD()
	if !wa.presenceSubs.Has(jid) {
		return
	}
	wa.setGhostPresence(ctx, waid.MakeUserID(jid), presenceToMatrix(evt, time.Now()))
}

func (wa *WhatsAppClient) setGhostPresence(ctx context.Context, userID networkid.UserID, content *event.PresenceEventContent) {
	ghost, err := wa.Main.Bridge.GetExistingGhostByID(ctx, userID)
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Str("ghost_id", string(userID)).Msg("Failed to get ghost to update presence")
		return
	} else if ghost == nil {
		return
	}
	setter, ok := ghost.Intent.(ghostPresenceSetter)
	if !ok {
		// TODO: remove this check once bridgev2 exposes ghost presence in MatrixAPI.
		zerolog.Ctx(ctx).Debug().Str("ghost_id", string(userID)).Msg("Matrix backend does not support setting ghost presence")
		return
	}
	err = setter.SetPresence(ctx, content)
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Str("ghost_id", string(userID)).Msg("Failed to set ghost presence")
	}
}
