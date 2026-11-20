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
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/connector/wadb"
	"go.mau.fi/mautrix-whatsapp/pkg/msgconv"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func getBeaconInfoContent(evt *event.Event) (*event.BeaconInfoEventContent, error) {
	if content, ok := evt.Content.Parsed.(*event.BeaconInfoEventContent); ok {
		return content, nil
	}
	var content event.BeaconInfoEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		return nil, fmt.Errorf("failed to parse beacon_info content: %w", err)
	}
	return &content, nil
}

func getBeaconContent(evt *event.Event) (*event.BeaconEventContent, error) {
	if content, ok := evt.Content.Parsed.(*event.BeaconEventContent); ok {
		return content, nil
	}
	var content event.BeaconEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		return nil, fmt.Errorf("failed to parse beacon content: %w", err)
	}
	return &content, nil
}

func (wa *WhatsAppClient) isOwnLiveLocationSender(evt *event.Event) bool {
	return wa.UserLogin != nil && evt.Sender != "" && evt.Sender == wa.UserLogin.UserMXID
}

// sendLiveLocationMessage sends a single WhatsApp live location message (start,
// update or final stop) and returns the ID WhatsApp assigned to it.
func (wa *WhatsAppClient) sendLiveLocationMessage(ctx context.Context, portal *bridgev2.Portal, msg *waE2E.Message) (types.MessageID, error) {
	chatJID, err := waid.ParsePortalID(portal.ID)
	if err != nil {
		return "", err
	}
	extra := whatsmeow.SendRequestExtra{
		ID:               wa.Client.GenerateMessageID(),
		CheckPermissions: true,
	}
	resp, err := wa.Client.SendMessage(ctx, chatJID, msg, extra)
	if err != nil {
		return "", wrapWhatsAppError(err)
	}
	return resp.ID, nil
}

// stopMatrixLiveLocation sends the final WhatsApp stop (if a message was
// actually started) and removes the share from the database.
func (wa *WhatsAppClient) stopMatrixLiveLocation(ctx context.Context, portal *bridgev2.Portal, mxid id.UserID, row *wadb.LiveLocationShare) error {
	// Nothing was sent to WhatsApp yet if the share never got a beacon, so
	// there is no live location there to stop.
	if row.WAMessageID != "" {
		msg := msgconv.LiveLocationStopMessage(row.Sequence + 1)
		if _, err := wa.sendLiveLocationMessage(ctx, portal, msg); err != nil {
			return err
		}
	}
	return wa.Main.DB.LiveLocation.Delete(ctx, wa.UserLogin.ID, portal.PortalKey, wadb.LiveLocationToWhatsApp, mxid)
}

// HandleMatrixBeaconInfo starts or stops a WhatsApp live location share when
// the logged-in user writes an MSC3672 beacon_info state event. Only the
// logged-in user's own MXID may control the share, and the state key must be
// that MXID as required by MSC3672.
func (wa *WhatsAppClient) HandleMatrixBeaconInfo(ctx context.Context, portal *bridgev2.Portal, evt *event.Event) (retErr error) {
	if !wa.isOwnLiveLocationSender(evt) || evt.GetStateKey() != wa.UserLogin.UserMXID.String() {
		zerolog.Ctx(ctx).Debug().
			Stringer("sender", evt.Sender).
			Msg("Ignoring beacon_info event from another user or state key")
		return nil
	}
	content, err := getBeaconInfoContent(evt)
	if err != nil {
		return err
	}
	db := wa.Main.DB.LiveLocation
	row, err := db.GetAny(ctx, wa.UserLogin.ID, portal.PortalKey, wadb.LiveLocationToWhatsApp, evt.Sender)
	if err != nil {
		return err
	}
	if row != nil && row.Expired() {
		if err = wa.stopMatrixLiveLocation(ctx, portal, evt.Sender, row); err != nil {
			return err
		}
		row = nil
	}
	if !content.Live {
		if row == nil {
			return nil
		}
		return wa.stopMatrixLiveLocation(ctx, portal, evt.Sender, row)
	}
	if content.GetTimestamp() <= 0 || content.Timeout <= 0 {
		return fmt.Errorf("invalid beacon_info: timestamp=%d timeout=%d", content.GetTimestamp(), content.Timeout)
	}
	expiry := content.GetTimestamp() + content.Timeout
	if time.Now().UnixMilli() >= expiry {
		// A beacon_info that is already expired can't start a share.
		return nil
	}
	newRow := &wadb.LiveLocationShare{
		UserLoginID:  wa.UserLogin.ID,
		PortalKey:    portal.PortalKey,
		Direction:    wadb.LiveLocationToWhatsApp,
		MatrixUserID: evt.Sender,
		InfoEventID:  evt.ID,
		Description:  content.Description,
		Timeout:      content.Timeout,
		StartedTS:    content.GetTimestamp(),
		Expiry:       expiry,
	}
	if row != nil {
		// Re-announcing a live beacon_info refreshes the share but keeps its
		// WhatsApp message and sequence, so updates stay in the same session.
		newRow.WAMessageID = row.WAMessageID
		newRow.Sequence = row.Sequence
	}
	defer wa.mcTrack(evt, time.Now(), &retErr)
	return db.Put(ctx, newRow)
}

// HandleMatrixBeacon converts one of the user's own m.beacon messages into a
// WhatsApp live location update. The first beacon starts the WhatsApp message
// with sequence 1; later beacons increment the sequence and refresh expiry.
func (wa *WhatsAppClient) HandleMatrixBeacon(ctx context.Context, portal *bridgev2.Portal, evt *event.Event) (retErr error) {
	if !wa.isOwnLiveLocationSender(evt) {
		zerolog.Ctx(ctx).Debug().Stringer("sender", evt.Sender).Msg("Ignoring beacon event from another user")
		return nil
	}
	content, err := getBeaconContent(evt)
	if err != nil {
		return err
	}
	db := wa.Main.DB.LiveLocation
	row, err := db.GetAny(ctx, wa.UserLogin.ID, portal.PortalKey, wadb.LiveLocationToWhatsApp, evt.Sender)
	if err != nil {
		return err
	}
	if row != nil && row.Expired() {
		if err = wa.stopMatrixLiveLocation(ctx, portal, evt.Sender, row); err != nil {
			return err
		}
		row = nil
	}
	if row == nil {
		zerolog.Ctx(ctx).Debug().Msg("Ignoring beacon without an active live location share")
		return nil
	}
	if ref := content.RelatesTo.EventID; ref != "" && row.InfoEventID != "" && ref != row.InfoEventID {
		zerolog.Ctx(ctx).Debug().Str("beacon_info_id", ref.String()).Msg("Ignoring beacon for a different beacon_info event")
		return nil
	}
	sequence := row.Sequence + 1
	msg, err := msgconv.BeaconToLiveLocationMessage(content, row.Description, sequence)
	if err != nil {
		return wrapConversionError(err)
	}
	defer wa.mcTrack(evt, time.Now(), &retErr)
	messageID, err := wa.sendLiveLocationMessage(ctx, portal, msg)
	if err != nil {
		return err
	}
	if row.WAMessageID == "" {
		row.WAMessageID = string(messageID)
	}
	row.Sequence = sequence
	if ts := content.GetTimestamp(); ts > 0 && row.Timeout > 0 {
		row.Expiry = ts + row.Timeout
	}
	return db.Put(ctx, row)
}
