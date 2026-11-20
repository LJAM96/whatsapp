// mautrix-whatsapp - A Matrix-WhatsApp puppeting bridge.
// Copyright (C) 2024 Tulir Asokan
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

package msgconv

import (
	"context"
	"fmt"
	"html"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	// whatsmeow does not expose the share duration from the WhatsApp <enc>
	// element, so use the most common WhatsApp share duration as a fallback.
	// Clients stop considering a beacon live at ts+timeout when no new beacon
	// arrives, which also covers WhatsApp expiry without an explicit stop.
	liveLocationTimeout = time.Hour
)

func buildBeaconInfoContent(live bool, ts time.Time, description string) *event.BeaconInfoEventContent {
	return &event.BeaconInfoEventContent{
		Live:        live,
		Timeout:     liveLocationTimeout.Milliseconds(),
		Timestamp:   ts.UnixMilli(),
		Description: description,
		Asset:       &event.BeaconAsset{Type: event.LocationAssetTypeSelf},
	}
}

func buildBeaconContent(infoEventID id.EventID, geoURI string, ts time.Time, description string) *event.BeaconEventContent {
	return &event.BeaconEventContent{
		RelatesTo: event.RelatesTo{Type: event.RelReference, EventID: infoEventID},
		Location:  &event.BeaconLocation{URI: geoURI, Description: description},
		Timestamp: ts.UnixMilli(),
	}
}

// liveLocationGeoURI formats a geo URI for an m.beacon event. The accuracy is
// included as the optional radius (MSC3488 uses the same u= parameter as
// geo_uri in m.location).
func liveLocationGeoURI(lat, long float64, accuracy uint32) string {
	uri := fmt.Sprintf("geo:%.7f,%.7f", lat, long)
	if accuracy > 0 {
		uri += fmt.Sprintf(";u=%d", accuracy)
	}
	return uri
}

func liveLocationDescription(msg *waE2E.LiveLocationMessage) string {
	if caption := msg.GetCaption(); caption != "" {
		return caption
	}
	return formatCoordinates(msg.GetDegreesLatitude(), msg.GetDegreesLongitude())
}

// liveLocationShare is the state needed to anchor further beacons to the
// beacon_info event sent when sharing started.
type liveLocationShare struct {
	InfoEventID id.EventID
	Info        event.BeaconInfoEventContent
}

func liveLocationShareKey(portal *bridgev2.Portal, intent bridgev2.MatrixAPI) string {
	return portal.MXID.String() + "\x00" + intent.GetMXID().String()
}

// liveLocationShareActive reports whether a beacon_info can still anchor new
// beacons. Clients consider a share live until ts+timeout, so an expired share
// must be replaced with a fresh one instead of being reused.
func liveLocationShareActive(info event.BeaconInfoEventContent) bool {
	if !info.Live || info.Timeout <= 0 || info.GetTimestamp() <= 0 {
		return false
	}
	expiresAt := time.UnixMilli(info.GetTimestamp()).Add(time.Duration(info.Timeout) * time.Millisecond)
	return time.Now().Before(expiresAt)
}

// recoverLiveLocationShare reads an active share from the room state. This is
// needed after a bridge restart, when the in-memory map is empty. A stopped or
// expired beacon_info is ignored so a fresh start doesn't get attached to it.
func recoverLiveLocationShare(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) *liveLocationShare {
	stateAPI, ok := intent.(bridgev2.MatrixAPIWithArbitraryRoomState)
	if !ok {
		return nil
	}
	stateEvt, err := stateAPI.GetStateEvent(ctx, portal.MXID, event.StateUnstableBeaconInfo, intent.GetMXID().String())
	if err != nil || stateEvt == nil {
		return nil
	}
	_ = stateEvt.Content.ParseRaw(event.StateUnstableBeaconInfo)
	info := stateEvt.Content.AsBeaconInfo()
	if !info.Live || info.Timeout <= 0 || info.GetTimestamp() <= 0 {
		return nil
	}
	if !liveLocationShareActive(*info) {
		return nil
	}
	return &liveLocationShare{InfoEventID: stateEvt.ID, Info: *info}
}

func (mc *MessageConverter) getLiveLocationShare(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) *liveLocationShare {
	key := liveLocationShareKey(portal, intent)
	mc.liveLocationLock.Lock()
	share := mc.liveLocations[key]
	if share != nil && !liveLocationShareActive(share.Info) {
		// An expired cached share must not anchor new beacons. Drop it so the
		// caller starts a fresh beacon_info and keeps the m.location fallback.
		delete(mc.liveLocations, key)
		share = nil
	}
	mc.liveLocationLock.Unlock()
	if share != nil {
		return share
	}
	share = recoverLiveLocationShare(ctx, portal, intent)
	if share != nil {
		mc.liveLocationLock.Lock()
		mc.liveLocations[key] = share
		mc.liveLocationLock.Unlock()
	}
	return share
}

func (mc *MessageConverter) sendLiveLocationBeacon(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg *waE2E.LiveLocationMessage, info *types.MessageInfo, share *liveLocationShare) {
	geoURI := liveLocationGeoURI(msg.GetDegreesLatitude(), msg.GetDegreesLongitude(), msg.GetAccuracyInMeters())
	content := buildBeaconContent(share.InfoEventID, geoURI, info.Timestamp, share.Info.Description)
	_, err := intent.SendMessage(ctx, portal.MXID, event.EventUnstableBeacon, &event.Content{Parsed: content}, &bridgev2.MatrixSendExtra{
		Timestamp:     info.Timestamp,
		TransactionID: fmt.Sprintf("wa-live-location-%s-%d", info.ID, msg.GetSequenceNumber()),
	})
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to send live location beacon")
	}
}

func (mc *MessageConverter) startLiveLocationBeacon(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg *waE2E.LiveLocationMessage, info *types.MessageInfo) {
	content := buildBeaconInfoContent(true, info.Timestamp, liveLocationDescription(msg))
	// Ghosts usually lack the power level to write state. MSC3672 identifies
	// the sharer by the state key, so the bridge bot can safely write it.
	resp, err := mc.Bridge.Bot.SendState(ctx, portal.MXID, event.StateUnstableBeaconInfo, intent.GetMXID().String(), &event.Content{Parsed: content}, info.Timestamp)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to send live location beacon_info state event")
		return
	}
	share := &liveLocationShare{InfoEventID: resp.EventID, Info: *content}
	mc.liveLocationLock.Lock()
	mc.liveLocations[liveLocationShareKey(portal, intent)] = share
	mc.liveLocationLock.Unlock()
	mc.sendLiveLocationBeacon(ctx, portal, intent, msg, info, share)
}

func (mc *MessageConverter) stopLiveLocationBeacon(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) {
	key := liveLocationShareKey(portal, intent)
	mc.liveLocationLock.Lock()
	share := mc.liveLocations[key]
	delete(mc.liveLocations, key)
	mc.liveLocationLock.Unlock()
	if share == nil {
		// Don't re-cache the recovered share here: a later start must be able
		// to create a fresh beacon_info event.
		share = recoverLiveLocationShare(ctx, portal, intent)
	}
	if share == nil {
		return
	}
	stopped := share.Info
	stopped.Live = false
	_, err := mc.Bridge.Bot.SendState(ctx, portal.MXID, event.StateUnstableBeaconInfo, intent.GetMXID().String(), &event.Content{Parsed: &stopped}, time.Now())
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to mark live location beacon_info as stopped")
	}
}

func noBridgePart() *bridgev2.ConvertedMessagePart {
	return &bridgev2.ConvertedMessagePart{
		Type:       event.EventMessage,
		Content:    &event.MessageEventContent{MsgType: event.MsgNotice, Body: "Live location update"},
		DontBridge: true,
	}
}

// convertLiveLocationMessage bridges WhatsApp live location messages to MSC3489
// beacons. The first message starts a beacon_info state event and sends a
// beacon; updates send further beacons; a message without coordinates stops the
// share (live: false, no further events). A plain m.location message is kept at
// the start as a fallback for clients that don't support beacons.
func (mc *MessageConverter) convertLiveLocationMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	intent bridgev2.MatrixAPI,
	msg *waE2E.LiveLocationMessage,
	info *types.MessageInfo,
	isBackfill bool,
) (*bridgev2.ConvertedMessagePart, *waE2E.ContextInfo) {
	if msg.DegreesLatitude == nil || msg.DegreesLongitude == nil {
		if !isBackfill {
			mc.stopLiveLocationBeacon(ctx, portal, intent)
		}
		return noBridgePart(), msg.GetContextInfo()
	}

	isStart := isBackfill
	if !isBackfill {
		if share := mc.getLiveLocationShare(ctx, portal, intent); share != nil {
			mc.sendLiveLocationBeacon(ctx, portal, intent, msg, info, share)
		} else {
			mc.startLiveLocationBeacon(ctx, portal, intent, msg, info)
			isStart = true
		}
	}
	if !isStart {
		// Updates are only represented by beacons; keep the plain m.location
		// fallback to the first message so clients don't get a message per update.
		return noBridgePart(), msg.GetContextInfo()
	}

	lat, long := msg.GetDegreesLatitude(), msg.GetDegreesLongitude()
	url := fmt.Sprintf("https://maps.google.com/?q=%.5f,%.5f", lat, long)
	name := formatCoordinates(lat, long)
	const note = "Live updates are only visible in the WhatsApp app."
	body := fmt.Sprintf("Live location: %s\n%s", name, url)
	formattedBody := fmt.Sprintf(`Live location: <a href="%s">%s</a>`, html.EscapeString(url), html.EscapeString(name))
	if caption := msg.GetCaption(); len(caption) > 0 {
		body += "\n" + caption
		formattedBody += "<br>" + html.EscapeString(caption)
	}
	content := &event.MessageEventContent{
		MsgType:       event.MsgLocation,
		Body:          body + "\n\n" + note,
		Format:        event.FormatHTML,
		FormattedBody: formattedBody + "<br><br>" + note,
		GeoURI:        fmt.Sprintf("geo:%.5f,%.5f", lat, long),
	}
	addLocationThumbnail(ctx, content, msg.GetJPEGThumbnail())

	return &bridgev2.ConvertedMessagePart{
		Type:    event.EventMessage,
		Content: content,
	}, msg.GetContextInfo()
}
