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
	"encoding/json"
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

// MSC3489/MSC3672 live location beacons. mautrix-go has no event types for
// these yet, so the unstable (MSC) identifiers are defined here. Clients that
// implement live location today read the unstable names, so using them avoids
// relying on identifiers that have not been ratified into the spec.
var (
	EventBeaconInfo = event.Type{Type: "org.matrix.msc3672.beacon_info", Class: event.StateEventType}
	EventBeacon     = event.Type{Type: "org.matrix.msc3672.beacon", Class: event.MessageEventType}
)

const (
	// whatsmeow does not expose the share duration from the WhatsApp <enc>
	// element, so use the most common WhatsApp share duration as a fallback.
	// Clients stop considering a beacon live at ts+timeout when no new beacon
	// arrives, which also covers WhatsApp expiry without an explicit stop.
	liveLocationTimeout = time.Hour
)

// BeaconInfoContent is the content of an org.matrix.msc3672.beacon_info state
// event. The state key is the MXID of the user sharing the location.
type BeaconInfoContent struct {
	Live        bool        `json:"live"`
	Timeout     int64       `json:"timeout"`
	TS          int64       `json:"org.matrix.msc3488.ts"`
	Description string      `json:"description,omitempty"`
	Asset       BeaconAsset `json:"org.matrix.msc3488.asset"`
}

// BeaconAsset describes what is being tracked. A person sharing their own
// location is always an m.self asset (MSC3488).
type BeaconAsset struct {
	Type string `json:"type"`
}

// BeaconLocation is the location carried by an m.beacon event.
type BeaconLocation struct {
	URI         string `json:"uri"`
	Description string `json:"description,omitempty"`
}

// BeaconRelatesTo relates a beacon to its beacon_info state event.
type BeaconRelatesTo struct {
	RelType string `json:"rel_type"`
	EventID string `json:"event_id"`
}

// BeaconContent is the content of an org.matrix.msc3672.beacon timeline event.
type BeaconContent struct {
	RelatesTo BeaconRelatesTo `json:"m.relates_to"`
	Location  BeaconLocation  `json:"org.matrix.msc3488.location"`
	TS        int64           `json:"org.matrix.msc3488.ts"`
}

func buildBeaconInfoContent(live bool, ts time.Time, description string) *BeaconInfoContent {
	return &BeaconInfoContent{
		Live:        live,
		Timeout:     liveLocationTimeout.Milliseconds(),
		TS:          ts.UnixMilli(),
		Description: description,
		Asset:       BeaconAsset{Type: "m.self"},
	}
}

func buildBeaconContent(infoEventID id.EventID, geoURI string, ts time.Time, description string) *BeaconContent {
	return &BeaconContent{
		RelatesTo: BeaconRelatesTo{RelType: "m.reference", EventID: infoEventID.String()},
		Location:  BeaconLocation{URI: geoURI, Description: description},
		TS:        ts.UnixMilli(),
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
	Info        BeaconInfoContent
}

func liveLocationShareKey(portal *bridgev2.Portal, intent bridgev2.MatrixAPI) string {
	return portal.MXID.String() + "\x00" + intent.GetMXID().String()
}

// recoverLiveLocationShare reads an active share from the room state. This is
// needed after a bridge restart, when the in-memory map is empty. A stopped or
// expired beacon_info is ignored so a fresh start doesn't get attached to it.
func recoverLiveLocationShare(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) *liveLocationShare {
	stateAPI, ok := intent.(bridgev2.MatrixAPIWithArbitraryRoomState)
	if !ok {
		return nil
	}
	stateEvt, err := stateAPI.GetStateEvent(ctx, portal.MXID, EventBeaconInfo, intent.GetMXID().String())
	if err != nil || stateEvt == nil {
		return nil
	}
	raw, err := json.Marshal(stateEvt.Content.Raw)
	if err != nil {
		return nil
	}
	var info BeaconInfoContent
	if err = json.Unmarshal(raw, &info); err != nil || !info.Live || info.Timeout <= 0 || info.TS <= 0 {
		return nil
	}
	if expiresAt := time.UnixMilli(info.TS).Add(time.Duration(info.Timeout) * time.Millisecond); !time.Now().Before(expiresAt) {
		return nil
	}
	return &liveLocationShare{InfoEventID: stateEvt.ID, Info: info}
}

func (mc *MessageConverter) getLiveLocationShare(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) *liveLocationShare {
	key := liveLocationShareKey(portal, intent)
	mc.liveLocationLock.Lock()
	share := mc.liveLocations[key]
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
	_, err := intent.SendMessage(ctx, portal.MXID, EventBeacon, &event.Content{Parsed: content}, &bridgev2.MatrixSendExtra{
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
	resp, err := mc.Bridge.Bot.SendState(ctx, portal.MXID, EventBeaconInfo, intent.GetMXID().String(), &event.Content{Parsed: content}, info.Timestamp)
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
	_, err := mc.Bridge.Bot.SendState(ctx, portal.MXID, EventBeaconInfo, intent.GetMXID().String(), &event.Content{Parsed: &stopped}, time.Now())
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
