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
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/connector/wadb"
)

func newTestLiveLocationDB(t *testing.T) *wadb.Database {
	t.Helper()
	rawDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	db, err := dbutil.NewWithDB(rawDB, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(context.Background(), `
		CREATE TABLE whatsapp_live_location (
			bridge_id TEXT NOT NULL, user_login_id TEXT NOT NULL,
			portal_id TEXT NOT NULL, portal_receiver TEXT NOT NULL,
			direction INTEGER NOT NULL, matrix_user_id TEXT NOT NULL,
			info_event_id TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '',
			timeout BIGINT NOT NULL DEFAULT 0, started_ts BIGINT NOT NULL DEFAULT 0,
			wa_message_id TEXT NOT NULL DEFAULT '', sequence BIGINT NOT NULL DEFAULT 0,
			expiry_ts BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (bridge_id, user_login_id, portal_id, portal_receiver, direction, matrix_user_id)
		);
		CREATE TABLE whatsapp_live_location_sent_message (
			bridge_id TEXT NOT NULL, user_login_id TEXT NOT NULL,
			portal_id TEXT NOT NULL, portal_receiver TEXT NOT NULL,
			matrix_user_id TEXT NOT NULL, wa_message_id TEXT NOT NULL,
			PRIMARY KEY (bridge_id, user_login_id, portal_id, portal_receiver, wa_message_id)
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	return wadb.New("test", db, zerolog.Nop())
}

func seedLiveLocationShare(t *testing.T, db *wadb.Database, portalKey networkid.PortalKey, mxid id.UserID, info event.BeaconInfoEventContent) {
	t.Helper()
	err := db.LiveLocation.Put(context.Background(), &wadb.LiveLocationShare{
		PortalKey:    portalKey,
		Direction:    wadb.LiveLocationFromWhatsApp,
		MatrixUserID: mxid,
		InfoEventID:  "$previous-info",
		Description:  info.Description,
		Timeout:      info.Timeout,
		StartedTS:    info.GetTimestamp(),
		Expiry:       info.GetTimestamp() + info.Timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLiveLocationGeoURI(t *testing.T) {
	tests := []struct {
		name     string
		lat      float64
		long     float64
		accuracy uint32
		expected string
	}{
		{"no accuracy", 51.5008, -0.1247, 0, "geo:51.5008000,-0.1247000"},
		{"with accuracy", -36.24484561954707, 175.46884959563613, 35, "geo:-36.2448456,175.4688496;u=35"},
		{"zero coordinates", 0, 0, 0, "geo:0.0000000,0.0000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := liveLocationGeoURI(tt.lat, tt.long, tt.accuracy)
			if actual != tt.expected {
				t.Errorf("liveLocationGeoURI() = %q, want %q", actual, tt.expected)
			}
		})
	}
}

func TestLiveLocationDescription(t *testing.T) {
	lat, long := 51.5, -0.12
	tests := []struct {
		name     string
		caption  string
		expected string
	}{
		{"caption used as description", "At the pub", "At the pub"},
		{"no caption falls back to coordinates", "", formatCoordinates(lat, long)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &waE2E.LiveLocationMessage{
				DegreesLatitude:  &lat,
				DegreesLongitude: &long,
			}
			if tt.caption != "" {
				msg.Caption = &tt.caption
			}
			actual := liveLocationDescription(msg)
			if actual != tt.expected {
				t.Errorf("liveLocationDescription() = %q, want %q", actual, tt.expected)
			}
		})
	}
}

func TestBuildBeaconInfoContent(t *testing.T) {
	ts := time.UnixMilli(1_636_829_458_432)
	tests := []struct {
		name        string
		live        bool
		description string
		expected    string
	}{
		{
			name:        "live share",
			live:        true,
			description: "The Matthew Tracker",
			expected:    `{"description":"The Matthew Tracker","live":true,"timeout":3600000,"org.matrix.msc3488.ts":1636829458432,"org.matrix.msc3488.asset":{"type":"m.self"}}`,
		},
		{
			name:        "stopped share has no description",
			live:        false,
			description: "",
			expected:    `{"live":false,"timeout":3600000,"org.matrix.msc3488.ts":1636829458432,"org.matrix.msc3488.asset":{"type":"m.self"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := buildBeaconInfoContent(tt.live, ts, tt.description)
			if content.Live != tt.live {
				t.Errorf("Live = %v, want %v", content.Live, tt.live)
			}
			if content.Timeout != 3_600_000 {
				t.Errorf("Timeout = %d, want 3600000", content.Timeout)
			}
			if content.GetAsset() == nil || content.GetAsset().Type != event.LocationAssetTypeSelf {
				t.Errorf("Asset.Type = %v, want m.self", content.GetAsset())
			}
			actual, err := json.Marshal(&event.Content{Parsed: content})
			if err != nil {
				t.Fatalf("failed to marshal beacon_info: %v", err)
			}
			if string(actual) != tt.expected {
				t.Errorf("beacon_info JSON = %s, want %s", actual, tt.expected)
			}
			var parsed event.Content
			parsed.VeryRaw = actual
			if err := parsed.ParseRaw(event.StateUnstableBeaconInfo); err != nil {
				t.Fatalf("failed to parse beacon_info: %v", err)
			}
			info := parsed.AsBeaconInfo()
			if info.Live != tt.live || info.GetTimestamp() != ts.UnixMilli() {
				t.Errorf("AsBeaconInfo() = %+v, want live=%v ts=%d", info, tt.live, ts.UnixMilli())
			}
		})
	}
}

func TestBuildBeaconContent(t *testing.T) {
	ts := time.UnixMilli(1_636_829_458_432)
	tests := []struct {
		name        string
		infoEventID id.EventID
		geoURI      string
		description string
		expected    string
	}{
		{
			name:        "beacon with description",
			infoEventID: "$beacon-info",
			geoURI:      "geo:51.5008000,-0.1247000;u=35",
			description: "At the pub",
			expected:    `{"m.relates_to":{"rel_type":"m.reference","event_id":"$beacon-info"},"org.matrix.msc3488.location":{"uri":"geo:51.5008000,-0.1247000;u=35","description":"At the pub"},"org.matrix.msc3488.ts":1636829458432}`,
		},
		{
			name:        "beacon without description",
			infoEventID: "$other",
			geoURI:      "geo:0.0000000,0.0000000",
			expected:    `{"m.relates_to":{"rel_type":"m.reference","event_id":"$other"},"org.matrix.msc3488.location":{"uri":"geo:0.0000000,0.0000000"},"org.matrix.msc3488.ts":1636829458432}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := buildBeaconContent(tt.infoEventID, tt.geoURI, ts, tt.description)
			actual, err := json.Marshal(&event.Content{Parsed: content})
			if err != nil {
				t.Fatalf("failed to marshal beacon: %v", err)
			}
			if string(actual) != tt.expected {
				t.Errorf("beacon JSON = %s, want %s", actual, tt.expected)
			}
			var parsed event.Content
			parsed.VeryRaw = actual
			if err := parsed.ParseRaw(event.EventUnstableBeacon); err != nil {
				t.Fatalf("failed to parse beacon: %v", err)
			}
			beacon := parsed.AsBeacon()
			if beacon.GetTimestamp() != ts.UnixMilli() || beacon.GetLocation().URI != tt.geoURI {
				t.Errorf("AsBeacon() = %+v, want ts=%d uri=%q", beacon, ts.UnixMilli(), tt.geoURI)
			}
		})
	}
}

func TestNoBridgePart(t *testing.T) {
	part := noBridgePart()
	if !part.DontBridge {
		t.Error("update/stop part must not be bridged as a Matrix message")
	}
	if part.Type != event.EventMessage {
		t.Errorf("part type = %v, want m.room.message", part.Type)
	}
	if part.Content == nil {
		t.Fatal("part content is nil")
	}
}

type recordedState struct {
	Type     event.Type
	StateKey string
	Content  event.BeaconInfoEventContent
}

type mockMatrixAPI struct {
	bridgev2.MatrixAPI
	mxid        id.UserID
	states      []recordedState
	beaconCount int
}

func (m *mockMatrixAPI) GetMXID() id.UserID { return m.mxid }

func (m *mockMatrixAPI) SendState(_ context.Context, _ id.RoomID, eventType event.Type, stateKey string, content *event.Content, _ time.Time) (*mautrix.RespSendEvent, error) {
	info := content.AsBeaconInfo()
	m.states = append(m.states, recordedState{Type: eventType, StateKey: stateKey, Content: *info})
	return &mautrix.RespSendEvent{EventID: id.EventID("$beacon-info")}, nil
}

func (m *mockMatrixAPI) SendMessage(_ context.Context, _ id.RoomID, eventType event.Type, _ *event.Content, _ *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	if eventType == event.EventUnstableBeacon {
		m.beaconCount++
	}
	return &mautrix.RespSendEvent{EventID: id.EventID("$beacon")}, nil
}

func TestConvertLiveLocationMessage(t *testing.T) {
	const (
		roomID  = id.RoomID("!room:example.org")
		ghostID = id.UserID("@wa_ghost:example.org")
	)
	lat, long := 51.5, -0.12
	withCoords := &waE2E.LiveLocationMessage{
		DegreesLatitude:  &lat,
		DegreesLongitude: &long,
	}
	withoutCoords := &waE2E.LiveLocationMessage{}
	info := &types.MessageInfo{ID: "msg1", Timestamp: time.UnixMilli(1_636_829_458_432)}

	tests := []struct {
		name          string
		msg           *waE2E.LiveLocationMessage
		isBackfill    bool
		expectStates  int
		expectBeacons int
		expectLive    bool
		expectBridge  bool
		expectMsgType event.MessageType
	}{
		{"start", withCoords, false, 1, 1, true, true, event.MsgLocation},
		{"update after start", withCoords, false, 0, 1, false, false, ""},
		{"stop", withoutCoords, false, 1, 0, false, false, ""},
		{"backfill start", withCoords, true, 0, 0, false, true, event.MsgLocation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &mockMatrixAPI{mxid: ghostID}
			mc := &MessageConverter{
				Bridge: &bridgev2.Bridge{Bot: api},
				DB:     newTestLiveLocationDB(t),
			}
			portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
			ctx := context.Background()

			// Seed an active share for the update and stop cases. Use the current
			// time so the expiry check doesn't discard it before the test runs.
			if tt.name == "update after start" || tt.name == "stop" {
				seedLiveLocationShare(t, mc.DB, portal.PortalKey, ghostID, *buildBeaconInfoContent(true, time.Now(), "test"))
			}

			part, _ := mc.convertLiveLocationMessage(ctx, portal, api, tt.msg, info, tt.isBackfill)
			if len(api.states) != tt.expectStates {
				t.Errorf("sent %d state events, want %d", len(api.states), tt.expectStates)
			}
			if api.beaconCount != tt.expectBeacons {
				t.Errorf("sent %d beacons, want %d", api.beaconCount, tt.expectBeacons)
			}
			if len(api.states) > 0 && api.states[0].Content.Live != tt.expectLive {
				t.Errorf("beacon_info live = %v, want %v", api.states[0].Content.Live, tt.expectLive)
			}
			if part.DontBridge == tt.expectBridge {
				t.Errorf("DontBridge = %v, want %v", part.DontBridge, !tt.expectBridge)
			}
			if tt.expectMsgType != "" && part.Content.MsgType != tt.expectMsgType {
				t.Errorf("msgtype = %q, want %q", part.Content.MsgType, tt.expectMsgType)
			}
			for _, state := range api.states {
				if state.Type != event.StateUnstableBeaconInfo {
					t.Errorf("state event type = %v, want %v", state.Type, event.StateUnstableBeaconInfo)
				}
				if state.StateKey != ghostID.String() {
					t.Errorf("state key = %q, want %q", state.StateKey, ghostID)
				}
			}
		})
	}
}

func TestParseBeaconGeoURI(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		wantLat     float64
		wantLong    float64
		wantAcc     uint32
		wantErrText string
	}{
		{"coordinates only", "geo:51.5,-0.12", 51.5, -0.12, 0, ""},
		{"with accuracy", "geo:-36.2448456,175.4688496;u=35", -36.2448456, 175.4688496, 35, ""},
		{"with unrelated param", "geo:1,2;u=12;foo=bar", 1, 2, 12, ""},
		{"missing geo prefix", "51.5,-0.12", 0, 0, 0, "geo: prefix"},
		{"bad coordinates", "geo:a,b", 0, 0, 0, "latitude is not a number"},
		{"bad accuracy", "geo:1,2;u=abc", 0, 0, 0, "accuracy is not a number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lat, long, accuracy, err := parseBeaconGeoURI(tt.uri)
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("parseBeaconGeoURI(%q) error = %v, want %q", tt.uri, err, tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBeaconGeoURI(%q) error: %v", tt.uri, err)
			}
			if lat != tt.wantLat || long != tt.wantLong || accuracy != tt.wantAcc {
				t.Errorf("parseBeaconGeoURI(%q) = %v,%v,%v, want %v,%v,%v", tt.uri, lat, long, accuracy, tt.wantLat, tt.wantLong, tt.wantAcc)
			}
		})
	}
}

func TestBeaconToLiveLocationMessage(t *testing.T) {
	tests := []struct {
		name        string
		beacon      *event.BeaconEventContent
		description string
		sequence    int64
		wantLat     float64
		wantLong    float64
		wantAcc     uint32
		wantCaption string
		wantErr     bool
	}{
		{
			name:        "first beacon carries description",
			beacon:      buildBeaconContent("$info", "geo:51.5008000,-0.1247000;u=35", time.UnixMilli(1000), ""),
			description: "At the pub",
			sequence:    1,
			wantLat:     51.5008,
			wantLong:    -0.1247,
			wantAcc:     35,
			wantCaption: "At the pub",
		},
		{
			name:     "update only carries position, accuracy and sequence",
			beacon:   buildBeaconContent("$info", "geo:-36.2448456,175.4688496;u=12", time.UnixMilli(2000), "ignored"),
			sequence: 2,
			wantLat:  -36.2448456,
			wantLong: 175.4688496,
			wantAcc:  12,
		},
		{
			name:     "beacon without location is an error",
			beacon:   &event.BeaconEventContent{},
			sequence: 1,
			wantErr:  true,
		},
		{
			name:     "beacon with malformed geo URI is an error",
			beacon:   buildBeaconContent("$info", "not-a-geo-uri", time.UnixMilli(1000), ""),
			sequence: 1,
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := BeaconToLiveLocationMessage(tt.beacon, tt.description, tt.sequence)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("BeaconToLiveLocationMessage() error: %v", err)
			}
			llm := msg.GetLiveLocationMessage()
			if llm == nil {
				t.Fatal("message has no live location")
			}
			if llm.GetDegreesLatitude() != tt.wantLat || llm.GetDegreesLongitude() != tt.wantLong {
				t.Errorf("coordinates = %v,%v, want %v,%v", llm.GetDegreesLatitude(), llm.GetDegreesLongitude(), tt.wantLat, tt.wantLong)
			}
			if llm.GetAccuracyInMeters() != tt.wantAcc {
				t.Errorf("accuracy = %d, want %d", llm.GetAccuracyInMeters(), tt.wantAcc)
			}
			if llm.GetSequenceNumber() != tt.sequence {
				t.Errorf("sequence = %d, want %d", llm.GetSequenceNumber(), tt.sequence)
			}
			if llm.GetCaption() != tt.wantCaption {
				t.Errorf("caption = %q, want %q", llm.GetCaption(), tt.wantCaption)
			}
		})
	}
}

func TestLiveLocationStopMessage(t *testing.T) {
	for _, sequence := range []int64{1, 2, 10} {
		msg := LiveLocationStopMessage(sequence)
		llm := msg.GetLiveLocationMessage()
		if llm == nil {
			t.Fatalf("sequence %d: message has no live location", sequence)
		}
		if llm.GetSequenceNumber() != sequence {
			t.Errorf("sequence = %d, want %d", llm.GetSequenceNumber(), sequence)
		}
		if llm.DegreesLatitude != nil || llm.DegreesLongitude != nil {
			t.Errorf("stop message must have no coordinates, got %v/%v", llm.DegreesLatitude, llm.DegreesLongitude)
		}
	}
}

func TestLiveLocationSharePersistsAcrossRestart(t *testing.T) {
	const ghostID = id.UserID("@wa_ghost:example.org")
	portalKey := networkid.PortalKey{ID: "chat@example.org", Receiver: "login"}
	db := newTestLiveLocationDB(t)

	// "Before restart": a WhatsApp share creates a beacon_info anchor.
	first := &MessageConverter{DB: db}
	err := first.DB.LiveLocation.Put(context.Background(), &wadb.LiveLocationShare{
		PortalKey:    portalKey,
		Direction:    wadb.LiveLocationFromWhatsApp,
		MatrixUserID: ghostID,
		InfoEventID:  "$beacon-info",
		Description:  "At the pub",
		Timeout:      3_600_000,
		StartedTS:    time.Now().UnixMilli(),
		Expiry:       time.Now().Add(time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// "After restart": a fresh converter finds the same share and expiry.
	second := &MessageConverter{DB: db}
	share, err := second.DB.LiveLocation.Get(context.Background(), "", portalKey, wadb.LiveLocationFromWhatsApp, ghostID)
	if err != nil {
		t.Fatal(err)
	}
	if share == nil {
		t.Fatal("share was lost across restart")
	}
	if share.InfoEventID != "$beacon-info" || share.Description != "At the pub" {
		t.Errorf("recovered share = %+v, want beacon-info/At the pub", share)
	}

	// An expired share must be discarded by the expiry check.
	err = db.LiveLocation.Put(context.Background(), &wadb.LiveLocationShare{
		UserLoginID:  "login",
		PortalKey:    portalKey,
		Direction:    wadb.LiveLocationToWhatsApp,
		MatrixUserID: id.UserID("@own:example.org"),
		InfoEventID:  "$old-info",
		Expiry:       time.Now().Add(-time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := db.LiveLocation.Get(context.Background(), "login", portalKey, wadb.LiveLocationToWhatsApp, "@own:example.org")
	if err != nil {
		t.Fatal(err)
	}
	if expired != nil {
		t.Errorf("expired share was returned: %+v", expired)
	}
}

func TestConvertLiveLocationMessageSkipsWeaveEcho(t *testing.T) {
	const (
		roomID      = id.RoomID("!room:example.org")
		ghostID     = id.UserID("@wa_ghost:example.org")
		ownMessage  = "3EB0WEAVE-SENT"
		otherUserID = "3EB0SOMEONE-ELSE"
	)
	portalKey := networkid.PortalKey{ID: "chat@example.org", Receiver: "login"}
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: portalKey, MXID: roomID}}
	lat, long := 51.5, -0.12

	tests := []struct {
		name        string
		messageID   string
		isFromMe    bool
		stanzaID    string
		wantSkipped bool
	}{
		{name: "echo of recorded message ID is skipped", messageID: ownMessage, isFromMe: true, wantSkipped: true},
		{name: "echo referencing recorded original ID is skipped", messageID: "update-id", isFromMe: true, stanzaID: ownMessage, wantSkipped: true},
		{name: "unrelated from-me message is bridged", messageID: otherUserID, isFromMe: true},
		{name: "message from other user with our ID is bridged", messageID: ownMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestLiveLocationDB(t)
			if err := db.LiveLocationSentMessage.Put(context.Background(), "login", portalKey, "@own:example.org", ownMessage); err != nil {
				t.Fatal(err)
			}
			api := &mockMatrixAPI{mxid: ghostID}
			mc := &MessageConverter{Bridge: &bridgev2.Bridge{Bot: api}, DB: db}
			msg := &waE2E.LiveLocationMessage{DegreesLatitude: &lat, DegreesLongitude: &long}
			if tt.stanzaID != "" {
				msg.ContextInfo = &waE2E.ContextInfo{StanzaID: &tt.stanzaID}
			}
			info := &types.MessageInfo{ID: types.MessageID(tt.messageID), Timestamp: time.Now(), MessageSource: types.MessageSource{IsFromMe: tt.isFromMe}}

			part, _ := mc.convertLiveLocationMessage(context.Background(), portal, api, msg, info, false)
			if part.DontBridge != tt.wantSkipped {
				t.Errorf("DontBridge = %v, want %v", part.DontBridge, tt.wantSkipped)
			}
			bridged := len(api.states) > 0 || api.beaconCount > 0
			if bridged == tt.wantSkipped {
				t.Errorf("bridged to Matrix = %v, want %v", bridged, !tt.wantSkipped)
			}
		})
	}
}
