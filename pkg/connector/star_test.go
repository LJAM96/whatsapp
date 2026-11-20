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
	"errors"
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestGetStarContent(t *testing.T) {
	preParsed := &StarEventContent{EventID: "$parsed", Starred: true}
	tests := []struct {
		name    string
		raw     string
		parsed  any
		want    StarEventContent
		wantErr bool
	}{
		{
			name: "star",
			raw:  `{"eventId":"$abc","starred":true}`,
			want: StarEventContent{EventID: "$abc", Starred: true},
		},
		{
			name: "unstar",
			raw:  `{"eventId":"$abc","starred":false}`,
			want: StarEventContent{EventID: "$abc"},
		},
		{
			name:   "parsed content is preferred",
			raw:    `{"eventId":"$raw","starred":false}`,
			parsed: preParsed,
			want:   *preParsed,
		},
		{
			name:    "invalid JSON is an error",
			raw:     `{"eventId":`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &event.Event{Content: event.Content{
				VeryRaw: json.RawMessage(tt.raw),
				Parsed:  tt.parsed,
			}}
			got, err := getStarContent(evt)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("getStarContent() error: %v", err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("getStarContent() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestStarEventToAppState(t *testing.T) {
	ownJID := types.NewJID("15550000000", types.DefaultUserServer)
	ownLID := types.NewJID("15550000000", types.HiddenUserServer)
	otherJID := types.NewJID("15557654321", types.DefaultUserServer)
	chat1 := types.NewJID("15551234567", types.DefaultUserServer)
	group := types.NewJID("120363000000000000", types.GroupServer)
	eventID := id.EventID("$bridged")
	errLookup := errors.New("lookup failed")
	wa := &WhatsAppClient{JID: ownJID, LID: ownLID}
	tests := []struct {
		name      string
		message   *database.Message
		lookupErr error
		starred   bool
		wantOK    bool
		wantIndex []string
		wantErr   error
	}{
		{
			name:      "star own message in a DM",
			message:   &database.Message{ID: waid.MakeMessageID(chat1, ownJID, "MSG1")},
			starred:   true,
			wantOK:    true,
			wantIndex: []string{appstate.IndexStar, chat1.String(), "MSG1", "1", ownJID.String()},
		},
		{
			name:      "star own message in a self-chat omits sender",
			message:   &database.Message{ID: waid.MakeMessageID(ownJID, ownJID, "MSG0")},
			starred:   true,
			wantOK:    true,
			wantIndex: []string{appstate.IndexStar, ownJID.String(), "MSG0", "1", "0"},
		},
		{
			name:      "unstar another user's group message",
			message:   &database.Message{ID: waid.MakeMessageID(group, otherJID, "MSG2")},
			starred:   false,
			wantOK:    true,
			wantIndex: []string{appstate.IndexStar, group.String(), "MSG2", "0", otherJID.String()},
		},
		{
			name: "event without a bridged message is skipped",
		},
		{
			name:    "fake message ID is skipped",
			message: &database.Message{ID: "fake:invalid:invalid"},
		},
		{
			name:      "database error is returned",
			lookupErr: errLookup,
			wantErr:   errLookup,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var queried id.EventID
			lookup := func(_ context.Context, mxid id.EventID) (*database.Message, error) {
				queried = mxid
				return tt.message, tt.lookupErr
			}
			patch, ok, err := wa.starEventToAppState(context.Background(), eventID, tt.starred, lookup)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("starEventToAppState() error = %v, want %v", err, tt.wantErr)
			}
			if queried != eventID {
				t.Errorf("looked up %q, want %q", queried, eventID)
			}
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				if len(patch.Mutations) != 0 {
					t.Errorf("patch = %+v, want empty", patch)
				}
				return
			}
			if patch.Type != appstate.WAPatchRegularHigh {
				t.Errorf("patch type = %q, want %q", patch.Type, appstate.WAPatchRegularHigh)
			}
			if len(patch.Mutations) != 1 {
				t.Fatalf("patch has %d mutations, want 1", len(patch.Mutations))
			}
			mutation := patch.Mutations[0]
			if !reflect.DeepEqual(mutation.Index, tt.wantIndex) {
				t.Errorf("index = %v, want %v", mutation.Index, tt.wantIndex)
			}
			if mutation.Version != 2 {
				t.Errorf("version = %d, want 2", mutation.Version)
			}
			if got := mutation.Value.GetStarAction().GetStarred(); got != tt.starred {
				t.Errorf("starred = %v, want %v", got, tt.starred)
			}
		})
	}
}

func TestHandleStarIgnoresOtherSenders(t *testing.T) {
	ownMXID := id.UserID("@own:example.org")
	wa := &WhatsAppClient{UserLogin: &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{UserMXID: ownMXID},
	}}
	tests := []struct {
		name   string
		sender id.UserID
	}{
		{"other user", id.UserID("@other:example.org")},
		{"empty sender", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &event.Event{
				Sender:  tt.sender,
				Content: event.Content{VeryRaw: json.RawMessage(`{"eventId":"$abc","starred":true}`)},
			}
			// Main and Client are nil: reaching the lookup or send would panic.
			if err := wa.HandleStar(context.Background(), nil, evt); err != nil {
				t.Errorf("HandleStar() error = %v, want nil", err)
			}
		})
	}
}
