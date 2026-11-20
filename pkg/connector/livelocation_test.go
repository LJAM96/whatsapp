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
	"encoding/json"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
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
