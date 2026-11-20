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
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

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
			expected:    `{"live":true,"timeout":3600000,"org.matrix.msc3488.ts":1636829458432,"description":"The Matthew Tracker","org.matrix.msc3488.asset":{"type":"m.self"}}`,
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
			if content.Asset.Type != "m.self" {
				t.Errorf("Asset.Type = %q, want m.self", content.Asset.Type)
			}
			actual, err := json.Marshal(&event.Content{Parsed: content})
			if err != nil {
				t.Fatalf("failed to marshal beacon_info: %v", err)
			}
			if string(actual) != tt.expected {
				t.Errorf("beacon_info JSON = %s, want %s", actual, tt.expected)
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
	Content  BeaconInfoContent
}

type mockMatrixAPI struct {
	bridgev2.MatrixAPI
	mxid        id.UserID
	states      []recordedState
	beaconCount int
}

func (m *mockMatrixAPI) GetMXID() id.UserID { return m.mxid }

func (m *mockMatrixAPI) SendState(_ context.Context, _ id.RoomID, eventType event.Type, stateKey string, content *event.Content, _ time.Time) (*mautrix.RespSendEvent, error) {
	info := content.Parsed.(*BeaconInfoContent)
	m.states = append(m.states, recordedState{Type: eventType, StateKey: stateKey, Content: *info})
	return &mautrix.RespSendEvent{EventID: id.EventID("$beacon-info")}, nil
}

func (m *mockMatrixAPI) SendMessage(_ context.Context, _ id.RoomID, eventType event.Type, _ *event.Content, _ *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	if eventType == EventBeacon {
		m.beaconCount++
	}
	return &mautrix.RespSendEvent{EventID: id.EventID("$beacon")}, nil
}

func TestLiveLocationShareActive(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		info BeaconInfoContent
		want bool
	}{
		{name: "active share", info: BeaconInfoContent{Live: true, Timeout: 60_000, TS: now.UnixMilli()}, want: true},
		{name: "expired share", info: BeaconInfoContent{Live: true, Timeout: 1_000, TS: now.Add(-time.Minute).UnixMilli()}},
		{name: "stopped share", info: BeaconInfoContent{Live: false, Timeout: 60_000, TS: now.UnixMilli()}},
		{name: "missing timeout", info: BeaconInfoContent{Live: true, TS: now.UnixMilli()}},
		{name: "missing ts", info: BeaconInfoContent{Live: true, Timeout: 60_000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := liveLocationShareActive(tt.info); got != tt.want {
				t.Errorf("liveLocationShareActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetLiveLocationShareExpired(t *testing.T) {
	const (
		roomID  = id.RoomID("!room:example.org")
		ghostID = id.UserID("@wa_ghost:example.org")
	)
	now := time.Now()
	tests := []struct {
		name      string
		info      BeaconInfoContent
		wantReuse bool
	}{
		{name: "active cached share is reused", info: *buildBeaconInfoContent(true, now, "test"), wantReuse: true},
		{name: "expired cached share is dropped", info: *buildBeaconInfoContent(true, now.Add(-2*time.Hour), "test")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &mockMatrixAPI{mxid: ghostID}
			mc := &MessageConverter{liveLocations: make(map[string]*liveLocationShare)}
			portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
			key := liveLocationShareKey(portal, api)
			mc.liveLocations[key] = &liveLocationShare{InfoEventID: "$beacon-info", Info: tt.info}

			share := mc.getLiveLocationShare(context.Background(), portal, api)
			if tt.wantReuse {
				if share == nil || share.InfoEventID != "$beacon-info" {
					t.Errorf("getLiveLocationShare() = %+v, want cached share", share)
				}
				return
			}
			if share != nil {
				t.Errorf("getLiveLocationShare() = %+v, want nil for expired share", share)
			}
			if _, ok := mc.liveLocations[key]; ok {
				t.Error("expired share was not removed from the cache")
			}
		})
	}
}

func TestConvertLiveLocationMessageExpiredShare(t *testing.T) {
	const (
		roomID  = id.RoomID("!room:example.org")
		ghostID = id.UserID("@wa_ghost:example.org")
	)
	lat, long := 51.5, -0.12
	msg := &waE2E.LiveLocationMessage{DegreesLatitude: &lat, DegreesLongitude: &long}
	info := &types.MessageInfo{ID: "msg1", Timestamp: time.Now()}
	api := &mockMatrixAPI{mxid: ghostID}
	mc := &MessageConverter{
		Bridge:        &bridgev2.Bridge{Bot: api},
		liveLocations: make(map[string]*liveLocationShare),
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	mc.liveLocations[liveLocationShareKey(portal, api)] = &liveLocationShare{
		InfoEventID: "$expired-info",
		Info:        *buildBeaconInfoContent(true, time.Now().Add(-2*time.Hour), "test"),
	}

	part, _ := mc.convertLiveLocationMessage(context.Background(), portal, api, msg, info, false)
	if len(api.states) != 1 {
		t.Fatalf("sent %d state events, want 1 fresh beacon_info", len(api.states))
	}
	if !api.states[0].Content.Live || api.states[0].Content.TS != info.Timestamp.UnixMilli() {
		t.Errorf("fresh beacon_info = %+v, want live share with current ts", api.states[0].Content)
	}
	if api.beaconCount != 1 {
		t.Errorf("sent %d beacons, want 1", api.beaconCount)
	}
	if part.DontBridge || part.Content.MsgType != event.MsgLocation {
		t.Errorf("start part = %+v, want m.location fallback", part)
	}
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
				Bridge:        &bridgev2.Bridge{Bot: api},
				liveLocations: make(map[string]*liveLocationShare),
			}
			portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
			ctx := context.Background()

			// Seed an active share for the update and stop cases. The share must
			// be recent or getLiveLocationShare correctly treats it as expired.
			if tt.name == "update after start" || tt.name == "stop" {
				mc.liveLocations[liveLocationShareKey(portal, api)] = &liveLocationShare{
					InfoEventID: "$previous-info",
					Info:        *buildBeaconInfoContent(true, time.Now(), "test"),
				}
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
				if state.Type != EventBeaconInfo {
					t.Errorf("state event type = %v, want %v", state.Type, EventBeaconInfo)
				}
				if state.StateKey != ghostID.String() {
					t.Errorf("state key = %q, want %q", state.StateKey, ghostID)
				}
			}
		})
	}
}
