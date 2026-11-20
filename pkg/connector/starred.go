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
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/msgconv"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

// accountDataIntent is the part of the double-puppet Matrix API needed to read
// and write room account data. The bridgev2 MatrixAPI interface does not expose
// account data, but the standard Matrix connector's intent does.
type accountDataIntent interface {
	Matrix() *appservice.IntentAPI
}

// handleWAStar handles WhatsApp app-state star/unstar events for messages by
// updating the fi.mau.whatsapp.starred room account data with the Matrix event
// ID of the starred message.
func (wa *WhatsAppClient) handleWAStar(ctx context.Context, evt *events.Star) bool {
	if evt.Action == nil {
		zerolog.Ctx(ctx).Debug().Msg("Ignoring star event with no action")
		return true
	}
	chatJID := wa.maybeConvertJIDToLID(ctx, evt.ChatJID)
	senderJID := wa.getRealSender(chatJID, wa.maybeConvertJIDToLID(ctx, evt.SenderJID), evt.IsFromMe)
	messageID := waid.MakeMessageID(chatJID, senderJID, evt.MessageID)
	msg, err := wa.Main.Bridge.DB.Message.GetLastPartByID(ctx, wa.UserLogin.ID, messageID)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).
			Stringer("chat", chatJID).
			Str("message_id", evt.MessageID).
			Msg("Failed to look up message for star event")
		return true
	} else if msg == nil || msg.MXID == "" {
		zerolog.Ctx(ctx).Debug().
			Stringer("chat", chatJID).
			Str("message_id", evt.MessageID).
			Msg("Ignoring star event for message not found in database")
		return true
	}
	portal, err := wa.Main.Bridge.GetPortalByKey(ctx, msg.Room)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to get portal for star event")
		return true
	} else if portal == nil || portal.MXID == "" {
		zerolog.Ctx(ctx).Debug().Msg("Ignoring star event for portal without Matrix room")
		return true
	}
	// Always consume the event: a failure to write account data must not abort
	// the rest of an app-state recovery batch (whatsmeow stops dispatching on
	// the first handler that reports failure during recovery).
	err = wa.setStarredEvent(ctx, portal.MXID, msg.MXID, evt.Action.GetStarred())
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to update starred events account data")
	}
	return true
}

// setStarredEvent reads the current starred list from room account data, applies
// the given star/unstar action and writes the updated list back. The mutex keeps
// concurrent app-state events from losing each other's updates.
func (wa *WhatsAppClient) setStarredEvent(ctx context.Context, roomID id.RoomID, eventID id.EventID, starred bool) error {
	wa.starredLock.Lock()
	defer wa.starredLock.Unlock()

	dp := wa.UserLogin.User.DoublePuppet(ctx)
	if dp == nil {
		return errors.New("no double puppet available for user")
	}
	intent, ok := dp.(accountDataIntent)
	if !ok {
		return fmt.Errorf("matrix API %T does not expose account data", dp)
	}
	client := intent.Matrix()

	var content msgconv.StarredEventContent
	err := client.GetRoomAccountData(ctx, roomID, msgconv.AccountDataStarred.Type, &content)
	if err != nil && !errors.Is(err, mautrix.MNotFound) {
		return fmt.Errorf("failed to get starred account data: %w", err)
	}
	updated, changed := msgconv.UpdateStarredEvents(content, eventID, starred)
	if !changed {
		return nil
	}
	err = client.SetRoomAccountData(ctx, roomID, msgconv.AccountDataStarred.Type, &updated)
	if err != nil {
		return fmt.Errorf("failed to set starred account data: %w", err)
	}
	return nil
}
