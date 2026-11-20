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
	"reflect"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/msgconv"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestPinnedChangeToMessage(t *testing.T) {
	ts := time.UnixMilli(1_700_000_000_000)
	chat := types.NewJID("15551234567", types.GroupServer)
	sender := types.NewJID("15557654321", types.DefaultUserServer)
	messageID := waid.MakeMessageID(chat, sender, "ABCDEF")
	eventID := id.EventID("$bridged")
	errLookup := errors.New("lookup failed")
	wantKey := &waCommon.MessageKey{
		RemoteJID:   ptrString(chat.String()),
		ID:          ptrString("ABCDEF"),
		Participant: ptrString(sender.String()),
	}
	tests := []struct {
		name      string
		change    msgconv.PinnedEventsChange
		message   *database.Message
		lookupErr error
		wantNil   bool
		wantType  waE2E.PinInChatMessage_Type
		wantErr   error
	}{
		{
			name:     "pin a bridged message",
			change:   msgconv.PinnedEventsChange{EventID: eventID, IsPin: true},
			message:  &database.Message{ID: messageID},
			wantType: waE2E.PinInChatMessage_PIN_FOR_ALL,
		},
		{
			name:     "unpin a bridged message",
			change:   msgconv.PinnedEventsChange{EventID: eventID, IsPin: false},
			message:  &database.Message{ID: messageID},
			wantType: waE2E.PinInChatMessage_UNPIN_FOR_ALL,
		},
		{
			name:    "unbridged event is skipped",
			change:  msgconv.PinnedEventsChange{EventID: eventID, IsPin: true},
			wantNil: true,
		},
		{
			name:    "fake message ID is skipped",
			change:  msgconv.PinnedEventsChange{EventID: eventID, IsPin: true},
			message: &database.Message{ID: "fake:invalid:invalid"},
			wantNil: true,
		},
		{
			name:      "database error is returned",
			change:    msgconv.PinnedEventsChange{EventID: eventID, IsPin: true},
			lookupErr: errLookup,
			wantErr:   errLookup,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wa := &WhatsAppClient{}
			var queried id.EventID
			lookup := func(_ context.Context, mxid id.EventID) (*database.Message, error) {
				queried = mxid
				return tt.message, tt.lookupErr
			}
			msg, err := wa.pinnedChangeToMessage(context.Background(), tt.change, lookup, ts)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("pinnedChangeToMessage() error = %v, want %v", err, tt.wantErr)
			}
			if queried != tt.change.EventID {
				t.Errorf("looked up %q, want %q", queried, tt.change.EventID)
			}
			if tt.wantNil || tt.wantErr != nil {
				if msg != nil {
					t.Errorf("message = %+v, want nil", msg)
				}
				return
			}
			if msg == nil {
				t.Fatal("message = nil, want PinInChatMessage")
			}
			pin := msg.GetPinInChatMessage()
			if pin.GetType() != tt.wantType {
				t.Errorf("pin type = %v, want %v", pin.GetType(), tt.wantType)
			}
			if !reflect.DeepEqual(pin.GetKey(), wantKey) {
				t.Errorf("pin key = %+v, want %+v", pin.GetKey(), wantKey)
			}
			if pin.GetSenderTimestampMS() != ts.UnixMilli() {
				t.Errorf("pin timestamp = %d, want %d", pin.GetSenderTimestampMS(), ts.UnixMilli())
			}
		})
	}
}

func TestHandleMatrixRoomPinnedEventsNoop(t *testing.T) {
	groupID := waid.MakePortalID(types.NewJID("15551234567", types.GroupServer))
	one := id.EventID("$one")
	tests := []struct {
		name    string
		msg     *bridgev2.MatrixRoomPinnedEvents
		wantErr bool
	}{
		{name: "nil message"},
		{name: "nil portal", msg: &bridgev2.MatrixRoomPinnedEvents{}},
		{name: "nil content", msg: &bridgev2.MatrixRoomPinnedEvents{
			MatrixEventBase: bridgev2.MatrixEventBase[*event.PinnedEventsEventContent]{
				Portal: &bridgev2.Portal{},
			},
		}},
		{name: "no pinned changes", msg: &bridgev2.MatrixRoomPinnedEvents{
			MatrixEventBase: bridgev2.MatrixEventBase[*event.PinnedEventsEventContent]{
				Content: &event.PinnedEventsEventContent{Pinned: []id.EventID{one}},
				Portal:  &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{ID: groupID}}},
			},
			PrevContent: &event.PinnedEventsEventContent{Pinned: []id.EventID{one}},
		}},
		{name: "invalid portal ID", msg: &bridgev2.MatrixRoomPinnedEvents{
			MatrixEventBase: bridgev2.MatrixEventBase[*event.PinnedEventsEventContent]{
				Content: &event.PinnedEventsEventContent{Pinned: []id.EventID{one}},
				Portal:  &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{ID: "a.b.c@invalid"}}},
			},
		}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Main and Client are nil: any database lookup or WhatsApp send
			// would panic, so only early-return paths are exercised here.
			wa := &WhatsAppClient{MC: noopMCInstance}
			handled, err := wa.HandleMatrixRoomPinnedEvents(context.Background(), tt.msg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HandleMatrixRoomPinnedEvents() error = %v, wantErr %v", err, tt.wantErr)
			}
			if handled {
				t.Error("HandleMatrixRoomPinnedEvents() handled = true, want false")
			}
		})
	}
}

func ptrString(s string) *string {
	return &s
}
