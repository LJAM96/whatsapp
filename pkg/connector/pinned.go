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
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/msgconv"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

var _ bridgev2.PinnedEventsHandlingNetworkAPI = (*WhatsAppClient)(nil)

// messageByMXID resolves a Matrix event ID to the message in the bridge
// database. It is a function type so the pin conversion can be unit tested
// without a database.
type messageByMXID func(ctx context.Context, mxid id.EventID) (*database.Message, error)

// HandleMatrixRoomPinnedEvents mirrors an m.room.pinned_events change to
// WhatsApp. Event IDs that don't belong to bridged messages can't be mapped
// back to a WhatsApp message key, so they're skipped. The returned boolean
// reports whether any pin was actually sent.
func (wa *WhatsAppClient) HandleMatrixRoomPinnedEvents(ctx context.Context, msg *bridgev2.MatrixRoomPinnedEvents) (bool, error) {
	if msg == nil || msg.Portal == nil || msg.Content == nil {
		return false, nil
	}
	var prev event.PinnedEventsEventContent
	if msg.PrevContent != nil {
		prev = *msg.PrevContent
	}
	changes := msgconv.PinnedEventsDiff(prev, *msg.Content)
	if len(changes) == 0 {
		return false, nil
	}
	portalJID, err := waid.ParsePortalID(msg.Portal.ID)
	if err != nil {
		return false, err
	}
	var retErr error
	start := time.Now()
	defer wa.mcTrack(msg, start, &retErr)
	var handled bool
	for _, change := range changes {
		waMsg, err := wa.pinnedChangeToMessage(ctx, change, wa.Main.Bridge.DB.Message.GetPartByMXID, start)
		if err != nil {
			return false, err
		} else if waMsg == nil {
			continue
		}
		_, retErr = wa.Client.SendMessage(ctx, portalJID, waMsg, whatsmeow.SendRequestExtra{})
		if retErr != nil {
			return false, wrapWhatsAppError(retErr)
		}
		handled = true
	}
	return handled, nil
}

// pinnedChangeToMessage converts one Matrix pin change into a WhatsApp
// PinInChatMessage. It returns nil when the pinned event isn't a bridged
// message or its network ID can't be parsed.
func (wa *WhatsAppClient) pinnedChangeToMessage(ctx context.Context, change msgconv.PinnedEventsChange, getByMXID messageByMXID, ts time.Time) (*waE2E.Message, error) {
	part, err := getByMXID(ctx, change.EventID)
	if err != nil {
		return nil, err
	}
	if part == nil {
		return nil, nil
	}
	parsed, err := waid.ParseMessageID(part.ID)
	if err != nil {
		zerolog.Ctx(ctx).Debug().Err(err).
			Str("event_id", string(change.EventID)).
			Msg("Ignoring pin of an event without a bridged message ID")
		return nil, nil
	}
	return msgconv.NewPinInChatMessage(wa.messageIDToKey(parsed), change.IsPin, ts), nil
}
