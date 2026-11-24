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
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/connector/wadb"
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

// parseBeaconUncertainty parses an RFC 5870 uncertainty radius (uval) and
// rounds it to the integer meter accuracy used by WhatsApp. The uval grammar
// is a nonnegative decimal: 1*DIGIT ["." 1*DIGIT], so signs, exponents,
// empty values and malformed decimals are rejected rather than forwarded.
func parseBeaconUncertainty(value string) (uint32, error) {
	isDigits := func(s string) bool {
		if s == "" {
			return false
		}
		for _, r := range s {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	whole, frac, hasDot := strings.Cut(value, ".")
	if !isDigits(whole) || (hasDot && !isDigits(frac)) {
		return 0, fmt.Errorf("location accuracy is not a number: %q", value)
	}
	parsed, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil {
		return 0, fmt.Errorf("location accuracy is not a number: %w", parseErr)
	}
	rounded := math.Round(parsed)
	if rounded > math.MaxUint32 {
		return 0, fmt.Errorf("location accuracy is too large: %q", value)
	}
	return uint32(rounded), nil
}

// parseBeaconGeoURI parses an MSC3488 geo URI into coordinates and the
// optional uncertainty radius, which maps to WhatsApp's accuracy in meters.
func parseBeaconGeoURI(uri string) (lat, long float64, accuracy uint32, err error) {
	lat, long, err = parseGeoURI(uri)
	if err != nil {
		return
	}
	for _, param := range strings.Split(uri, ";") {
		key, value, ok := strings.Cut(param, "=")
		if !ok || key != "u" {
			continue
		}
		accuracy, err = parseBeaconUncertainty(value)
		if err != nil {
			return
		}
	}
	return
}

// BeaconToLiveLocationMessage converts an m.beacon to a WhatsApp live location
// message. sequence is 1 for the first beacon of a share and increases for
// every update; the description is only sent on the first message, with later
// updates carrying just the new position, accuracy and sequence.
func BeaconToLiveLocationMessage(beacon *event.BeaconEventContent, description string, sequence int64) (*waE2E.Message, error) {
	location := beacon.GetLocation()
	if location == nil {
		return nil, fmt.Errorf("beacon has no location")
	}
	lat, long, accuracy, err := parseBeaconGeoURI(location.URI)
	if err != nil {
		return nil, err
	}
	msg := &waE2E.LiveLocationMessage{
		DegreesLatitude:  &lat,
		DegreesLongitude: &long,
		SequenceNumber:   &sequence,
	}
	if accuracy > 0 {
		msg.AccuracyInMeters = &accuracy
	}
	if sequence == 1 && description != "" {
		msg.Caption = &description
	}
	return &waE2E.Message{LiveLocationMessage: msg}, nil
}

// LiveLocationStopMessage builds the final WhatsApp live location message. A
// live location without coordinates and with the next sequence number tells
// WhatsApp the share has ended.
func LiveLocationStopMessage(sequence int64) *waE2E.Message {
	return &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{
		SequenceNumber: &sequence,
	}}
}

func liveLocationDescription(msg *waE2E.LiveLocationMessage) string {
	if caption := msg.GetCaption(); caption != "" {
		return caption
	}
	return formatCoordinates(msg.GetDegreesLatitude(), msg.GetDegreesLongitude())
}

// liveLocationShare is the state needed to anchor further beacons to the
// beacon_info event sent when a WhatsApp share started.
type liveLocationShare struct {
	InfoEventID id.EventID
	Info        event.BeaconInfoEventContent
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
// used as a fallback after a bridge restart if the database has no row yet.
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

// shareFromDB converts a durable share row into the beacon anchor used by the
// converter. Get drops expired rows, so any row reaching here is still live.
func shareFromDB(row *wadb.LiveLocationShare) *liveLocationShare {
	return &liveLocationShare{
		InfoEventID: row.InfoEventID,
		Info: event.BeaconInfoEventContent{
			Live:        true,
			Timeout:     row.Timeout,
			Timestamp:   row.StartedTS,
			Description: row.Description,
			Asset:       &event.BeaconAsset{Type: event.LocationAssetTypeSelf},
		},
	}
}

func (mc *MessageConverter) getLiveLocationShare(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) *liveLocationShare {
	row, err := mc.DB.LiveLocation.Get(ctx, "", portal.PortalKey, wadb.LiveLocationFromWhatsApp, intent.GetMXID())
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to get live location share from database")
	}
	if row != nil {
		return shareFromDB(row)
	}
	share := recoverLiveLocationShare(ctx, portal, intent)
	if share != nil {
		mc.putLiveLocationShare(ctx, portal, intent, share)
	}
	return share
}

func (mc *MessageConverter) putLiveLocationShare(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, share *liveLocationShare) {
	err := mc.DB.LiveLocation.Put(ctx, &wadb.LiveLocationShare{
		UserLoginID:  networkid.UserLoginID(""),
		PortalKey:    portal.PortalKey,
		Direction:    wadb.LiveLocationFromWhatsApp,
		MatrixUserID: intent.GetMXID(),
		InfoEventID:  share.InfoEventID,
		Description:  share.Info.Description,
		Timeout:      share.Info.Timeout,
		StartedTS:    share.Info.GetTimestamp(),
		Expiry:       share.Info.GetTimestamp() + share.Info.Timeout,
	})
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to save live location share to database")
	}
}

func (mc *MessageConverter) deleteLiveLocationShare(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) {
	err := mc.DB.LiveLocation.Delete(ctx, "", portal.PortalKey, wadb.LiveLocationFromWhatsApp, intent.GetMXID())
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to delete live location share from database")
	}
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
	mc.putLiveLocationShare(ctx, portal, intent, share)
	mc.sendLiveLocationBeacon(ctx, portal, intent, msg, info, share)
}

func (mc *MessageConverter) stopLiveLocationBeacon(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) {
	row, err := mc.DB.LiveLocation.Get(ctx, "", portal.PortalKey, wadb.LiveLocationFromWhatsApp, intent.GetMXID())
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to get live location share from database")
	}
	mc.deleteLiveLocationShare(ctx, portal, intent)
	var share *liveLocationShare
	if row != nil {
		share = shareFromDB(row)
	} else {
		// Don't re-cache the recovered share here: a later start must be able
		// to create a fresh beacon_info event.
		share = recoverLiveLocationShare(ctx, portal, intent)
	}
	if share == nil {
		return
	}
	stopped := share.Info
	stopped.Live = false
	_, err = mc.Bridge.Bot.SendState(ctx, portal.MXID, event.StateUnstableBeaconInfo, intent.GetMXID().String(), &event.Content{Parsed: &stopped}, time.Now())
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

// isWeaveLiveLocationEcho reports whether a from-me live location message is
// the echo of a message the bridge sent to WhatsApp for a Weave-originated
// share. Every ID we send (start, each update and the final stop) is recorded
// in the sent-message table, so a direct ID match is enough. WhatsApp updates
// may also reference the original share message through contextInfo.stanza_id,
// so that ID is checked too. Bridging such an echo would create a loop: it
// would be sent back to Matrix under the user's own MXID and then picked up by
// the Matrix-to-WhatsApp live location handler.
func (mc *MessageConverter) isWeaveLiveLocationEcho(ctx context.Context, portal *bridgev2.Portal, msg *waE2E.LiveLocationMessage, info *types.MessageInfo) bool {
	if mc.DB == nil || mc.DB.LiveLocationSentMessage == nil || info == nil {
		return false
	}
	queries := mc.DB.LiveLocationSentMessage
	if exists, err := queries.Exists(ctx, portal.PortalKey, string(info.ID)); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Str("message_id", string(info.ID)).
			Msg("Failed to check if live location message is an echo")
	} else if exists {
		return true
	}
	if stanzaID := msg.GetContextInfo().GetStanzaID(); stanzaID != "" {
		if exists, err := queries.Exists(ctx, portal.PortalKey, stanzaID); err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Str("stanza_id", stanzaID).
				Msg("Failed to check if live location update references an echo")
		} else if exists {
			return true
		}
	}
	return false
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
	if info.IsFromMe && mc.isWeaveLiveLocationEcho(ctx, portal, msg, info) {
		zerolog.Ctx(ctx).Debug().
			Str("message_id", string(info.ID)).
			Msg("Ignoring echo of a live location message sent by the bridge")
		return noBridgePart(), msg.GetContextInfo()
	}
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
