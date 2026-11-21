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
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/connector/wadb"
)

func TestLiveLocationEventTypesDeclared(t *testing.T) {
	types := (&WhatsAppConnector{}).GetCustomMatrixEventTypes()
	want := map[event.Type]bool{
		event.StateUnstableBeaconInfo: false,
		event.EventUnstableBeacon:     false,
	}
	for _, evtType := range types {
		if _, ok := want[evtType]; ok {
			want[evtType] = true
		}
	}
	for evtType, found := range want {
		if !found {
			t.Errorf("custom event types do not declare %v", evtType)
		}
	}
	if event.StateUnstableBeaconInfo.Class != event.StateEventType {
		t.Error("StateUnstableBeaconInfo is not a state event type")
	}
	if event.EventUnstableBeacon.Class != event.MessageEventType {
		t.Error("EventUnstableBeacon is not a message event type")
	}
}

func TestGetBeaconInfoContent(t *testing.T) {
	parsed := &event.BeaconInfoEventContent{Live: false, Description: "parsed"}
	tests := []struct {
		name    string
		raw     string
		parsed  any
		want    event.BeaconInfoEventContent
		wantErr bool
	}{
		{
			name: "raw live info",
			raw:  `{"live":true,"timeout":3600000,"org.matrix.msc3488.ts":1000,"description":"At the pub"}`,
			want: event.BeaconInfoEventContent{Live: true, Timeout: 3_600_000, Timestamp: 1000, Description: "At the pub"},
		},
		{
			name:   "parsed content is preferred",
			raw:    `{"live":true}`,
			parsed: parsed,
			want:   *parsed,
		},
		{
			name:    "invalid JSON is an error",
			raw:     `{"live":`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &event.Event{Content: event.Content{VeryRaw: json.RawMessage(tt.raw), Parsed: tt.parsed}}
			got, err := getBeaconInfoContent(evt)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("getBeaconInfoContent() error: %v", err)
			}
			if *got != tt.want {
				t.Errorf("getBeaconInfoContent() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestGetBeaconContent(t *testing.T) {
	parsed := &event.BeaconEventContent{RelatesTo: event.RelatesTo{EventID: "$parsed"}}
	tests := []struct {
		name    string
		raw     string
		parsed  any
		want    event.BeaconEventContent
		wantErr bool
	}{
		{
			name: "raw beacon",
			raw:  `{"m.relates_to":{"rel_type":"m.reference","event_id":"$info"},"org.matrix.msc3488.location":{"uri":"geo:1,2;u=5"},"org.matrix.msc3488.ts":1000}`,
			want: event.BeaconEventContent{
				RelatesTo: event.RelatesTo{Type: event.RelReference, EventID: "$info"},
				Location:  &event.BeaconLocation{URI: "geo:1,2;u=5"},
				Timestamp: 1000,
			},
		},
		{
			name:   "parsed content is preferred",
			raw:    `{}`,
			parsed: parsed,
			want:   *parsed,
		},
		{
			name:    "invalid JSON is an error",
			raw:     `{"m.relates_to":`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &event.Event{Content: event.Content{VeryRaw: json.RawMessage(tt.raw), Parsed: tt.parsed}}
			got, err := getBeaconContent(evt)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("getBeaconContent() error: %v", err)
			}
			if got.GetRelatesTo().EventID != tt.want.GetRelatesTo().EventID || got.GetTimestamp() != tt.want.GetTimestamp() {
				t.Errorf("getBeaconContent() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestIsOwnLiveLocationSender(t *testing.T) {
	const own = id.UserID("@own:example.org")
	tests := []struct {
		name   string
		login  *bridgev2.UserLogin
		sender id.UserID
		want   bool
	}{
		{"own user", &bridgev2.UserLogin{UserLogin: &database.UserLogin{UserMXID: own}}, own, true},
		{"other user", &bridgev2.UserLogin{UserLogin: &database.UserLogin{UserMXID: own}}, "@other:example.org", false},
		{"empty sender", &bridgev2.UserLogin{UserLogin: &database.UserLogin{UserMXID: own}}, "", false},
		{"no login", nil, own, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wa := &WhatsAppClient{UserLogin: tt.login}
			got := wa.isOwnLiveLocationSender(&event.Event{Sender: tt.sender})
			if got != tt.want {
				t.Errorf("isOwnLiveLocationSender() = %v, want %v", got, tt.want)
			}
		})
	}
}

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

func TestIsBridgeDoublePuppetEcho(t *testing.T) {
	tests := []struct {
		name string
		evt  *event.Event
		want bool
	}{
		{"nil event", nil, false},
		{"no marker", &event.Event{Content: event.Content{Raw: map[string]any{}}}, false},
		{"empty marker", &event.Event{Content: event.Content{Raw: map[string]any{appservice.DoublePuppetKey: ""}}}, false},
		{"non-string marker", &event.Event{Content: event.Content{Raw: map[string]any{appservice.DoublePuppetKey: 42}}}, false},
		{"bridge marker", &event.Event{Content: event.Content{Raw: map[string]any{appservice.DoublePuppetKey: "mautrix-whatsapp"}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBridgeDoublePuppetEcho(tt.evt); got != tt.want {
				t.Errorf("isBridgeDoublePuppetEcho() = %v, want %v", got, tt.want)
			}
		})
	}
}

// newLiveLocationTestClient builds a client with a configured share so the
// beacon handlers can be exercised without a connected WhatsApp client.
func newLiveLocationTestClient(t *testing.T, own id.UserID) (*WhatsAppClient, *bridgev2.Portal, *[]string) {
	t.Helper()
	const loginID = networkid.UserLoginID("login")
	portalKey := networkid.PortalKey{ID: "chat@example.org", Receiver: loginID}
	db := newTestLiveLocationDB(t)
	if err := db.LiveLocation.Put(context.Background(), &wadb.LiveLocationShare{
		UserLoginID:  loginID,
		PortalKey:    portalKey,
		Direction:    wadb.LiveLocationToWhatsApp,
		MatrixUserID: own,
		InfoEventID:  "$info",
		Description:  "At the pub",
		Timeout:      3_600_000,
		StartedTS:    time.Now().UnixMilli(),
		WAMessageID:  "original-message-id",
		Sequence:     1,
		Expiry:       time.Now().Add(time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	sent := &[]string{}
	wa := &WhatsAppClient{
		Main: &WhatsAppConnector{DB: db},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{
			ID: loginID, UserMXID: own,
		}},
		MC: noopMCInstance,
	}
	wa.sendLiveLocationHook = func(_ context.Context, _ *bridgev2.Portal, _ *waE2E.Message) (types.MessageID, error) {
		id := types.MessageID("sent-message-id")
		*sent = append(*sent, string(id))
		return id, nil
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: portalKey, MXID: "!room:example.org"}}
	return wa, portal, sent
}

func liveLocationBeaconEvent(sender id.UserID, withMarker bool) *event.Event {
	content := event.Content{Parsed: &event.BeaconEventContent{
		RelatesTo: event.RelatesTo{EventID: "$info"},
		Location:  &event.BeaconLocation{URI: "geo:51.5,-0.12;u=35"},
		Timestamp: time.Now().UnixMilli(),
	}}
	if withMarker {
		content.Raw = map[string]any{appservice.DoublePuppetKey: "mautrix-whatsapp"}
	}
	return &event.Event{Sender: sender, Type: event.EventUnstableBeacon, Content: content}
}

func TestHandleMatrixBeaconDoublePuppetEcho(t *testing.T) {
	const own = id.UserID("@own:example.org")
	tests := []struct {
		name       string
		withMarker bool
		wantSends  int
	}{
		{name: "double-puppeted echo is not sent to WhatsApp", withMarker: true},
		{name: "genuine Weave beacon is sent to WhatsApp", wantSends: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wa, portal, sent := newLiveLocationTestClient(t, own)
			evt := liveLocationBeaconEvent(own, tt.withMarker)
			if err := wa.HandleMatrixBeacon(context.Background(), portal, evt); err != nil {
				t.Fatalf("HandleMatrixBeacon() error: %v", err)
			}
			if len(*sent) != tt.wantSends {
				t.Fatalf("sent %d WhatsApp messages, want %d", len(*sent), tt.wantSends)
			}
			if tt.wantSends > 0 {
				exists, err := wa.Main.DB.LiveLocationSentMessage.Exists(context.Background(), portal.PortalKey, "sent-message-id")
				if err != nil {
					t.Fatal(err)
				}
				if !exists {
					t.Error("sent message ID was not recorded for echo detection")
				}
			}
		})
	}
}

func TestHandleMatrixBeaconInfoDoublePuppetEcho(t *testing.T) {
	const own = id.UserID("@own:example.org")
	content := event.Content{
		Raw: map[string]any{appservice.DoublePuppetKey: "mautrix-whatsapp"},
		Parsed: &event.BeaconInfoEventContent{
			Live: true, Timeout: 3_600_000, Timestamp: time.Now().UnixMilli(),
		},
	}
	stateKey := own.String()
	evt := &event.Event{Sender: own, Type: event.StateUnstableBeaconInfo, StateKey: &stateKey, Content: content}
	wa, portal, sent := newLiveLocationTestClient(t, own)
	if err := wa.HandleMatrixBeaconInfo(context.Background(), portal, evt); err != nil {
		t.Fatalf("HandleMatrixBeaconInfo() error: %v", err)
	}
	if len(*sent) != 0 {
		t.Errorf("double-puppeted beacon_info sent %d WhatsApp messages, want 0", len(*sent))
	}
}
