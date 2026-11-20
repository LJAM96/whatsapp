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

package msgconv

import (
	"reflect"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func pinnedEvent(pinned ...id.EventID) *event.Event {
	return &event.Event{Content: event.Content{
		Parsed: &event.PinnedEventsEventContent{Pinned: pinned},
	}}
}

func pinMessage(typ waE2E.PinInChatMessage_Type, duration uint32) *waE2E.Message {
	msg := &waE2E.Message{
		PinInChatMessage: &waE2E.PinInChatMessage{Type: typ.Enum()},
	}
	if duration > 0 {
		msg.MessageContextInfo = &waE2E.MessageContextInfo{
			MessageAddOnDurationInSecs: proto.Uint32(duration),
		}
	}
	return msg
}

func TestPinInChatToMatrix(t *testing.T) {
	ts := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	const (
		a = id.EventID("$a")
		b = id.EventID("$b")
	)
	tests := []struct {
		name         string
		current      *event.Event
		msg          *waE2E.Message
		eventID      id.EventID
		wantPinned   []id.EventID
		wantExpiries map[string]any
		wantChanged  bool
	}{
		{
			name:        "pin new message in empty room",
			msg:         pinMessage(waE2E.PinInChatMessage_PIN_FOR_ALL, 0),
			eventID:     a,
			wantPinned:  []id.EventID{a},
			wantChanged: true,
		},
		{
			name:        "pin new message in existing list",
			current:     pinnedEvent(b),
			msg:         pinMessage(waE2E.PinInChatMessage_PIN_FOR_ALL, 0),
			eventID:     a,
			wantPinned:  []id.EventID{b, a},
			wantChanged: true,
		},
		{
			name:        "pin already pinned message",
			current:     pinnedEvent(a, b),
			msg:         pinMessage(waE2E.PinInChatMessage_PIN_FOR_ALL, 0),
			eventID:     a,
			wantChanged: false,
		},
		{
			name:        "unpin existing message",
			current:     pinnedEvent(a, b),
			msg:         pinMessage(waE2E.PinInChatMessage_UNPIN_FOR_ALL, 0),
			eventID:     a,
			wantPinned:  []id.EventID{b},
			wantChanged: true,
		},
		{
			name:        "unpin message that is not pinned",
			current:     pinnedEvent(b),
			msg:         pinMessage(waE2E.PinInChatMessage_UNPIN_FOR_ALL, 0),
			eventID:     a,
			wantChanged: false,
		},
		{
			name:        "unknown pin type is ignored",
			current:     pinnedEvent(b),
			msg:         pinMessage(waE2E.PinInChatMessage_UNKNOWN_TYPE, 0),
			eventID:     a,
			wantChanged: false,
		},
		{
			name:       "pin with duration records expiry",
			msg:        pinMessage(waE2E.PinInChatMessage_PIN_FOR_ALL, 3600),
			eventID:    a,
			wantPinned: []id.EventID{a},
			wantExpiries: map[string]any{
				string(a): ts.Add(time.Hour).UnixMilli(),
			},
			wantChanged: true,
		},
		{
			name: "pin with duration preserves other expiries",
			current: &event.Event{Content: event.Content{
				Parsed: &event.PinnedEventsEventContent{Pinned: []id.EventID{b}},
				Raw: map[string]any{PinExpiryKey: map[string]any{
					string(b): ts.Add(2 * time.Hour).UnixMilli(),
				}},
			}},
			msg:        pinMessage(waE2E.PinInChatMessage_PIN_FOR_ALL, 60),
			eventID:    a,
			wantPinned: []id.EventID{b, a},
			wantExpiries: map[string]any{
				string(b): ts.Add(2 * time.Hour).UnixMilli(),
				string(a): ts.Add(time.Minute).UnixMilli(),
			},
			wantChanged: true,
		},
		{
			name: "unpin removes expiry",
			current: &event.Event{Content: event.Content{
				Parsed: &event.PinnedEventsEventContent{Pinned: []id.EventID{a, b}},
				Raw: map[string]any{PinExpiryKey: map[string]any{
					string(a): ts.Add(time.Hour).UnixMilli(),
					string(b): ts.Add(2 * time.Hour).UnixMilli(),
				}},
			}},
			msg:        pinMessage(waE2E.PinInChatMessage_UNPIN_FOR_ALL, 0),
			eventID:    a,
			wantPinned: []id.EventID{b},
			wantExpiries: map[string]any{
				string(b): ts.Add(2 * time.Hour).UnixMilli(),
			},
			wantChanged: true,
		},
		{
			name:        "empty event ID is ignored",
			msg:         pinMessage(waE2E.PinInChatMessage_PIN_FOR_ALL, 0),
			eventID:     "",
			wantChanged: false,
		},
		{
			name:        "missing pin message is ignored",
			msg:         &waE2E.Message{Conversation: proto.String("hi")},
			eventID:     a,
			wantChanged: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, changed := PinInChatToMatrix(tt.current, tt.msg, tt.eventID, ts)
			if changed != tt.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if !changed {
				if content != nil {
					t.Errorf("content = %v, want nil", content)
				}
				return
			}
			parsed, ok := content.Parsed.(*event.PinnedEventsEventContent)
			if !ok {
				t.Fatalf("parsed content type = %T, want *event.PinnedEventsEventContent", content.Parsed)
			}
			if !reflect.DeepEqual(parsed.Pinned, tt.wantPinned) {
				t.Errorf("pinned = %v, want %v", parsed.Pinned, tt.wantPinned)
			}
			var expiries map[string]any
			if content.Raw != nil {
				expiries, _ = content.Raw[PinExpiryKey].(map[string]any)
			}
			if !reflect.DeepEqual(expiries, tt.wantExpiries) {
				t.Errorf("expiries = %v, want %v", expiries, tt.wantExpiries)
			}
		})
	}
}

func TestPinnedEventsDiff(t *testing.T) {
	const (
		a = id.EventID("$a")
		b = id.EventID("$b")
		c = id.EventID("$c")
	)
	tests := []struct {
		name    string
		old     []id.EventID
		current []id.EventID
		want    []PinnedEventsChange
	}{
		{
			name:    "no changes",
			old:     []id.EventID{a, b},
			current: []id.EventID{a, b},
		},
		{
			name:    "reordering is not a change",
			old:     []id.EventID{a, b},
			current: []id.EventID{b, a},
		},
		{
			name:    "pin added",
			old:     []id.EventID{a},
			current: []id.EventID{a, b},
			want:    []PinnedEventsChange{{EventID: b, IsPin: true}},
		},
		{
			name:    "pin removed",
			old:     []id.EventID{a, b},
			current: []id.EventID{a},
			want:    []PinnedEventsChange{{EventID: b, IsPin: false}},
		},
		{
			name:    "pin added and removed",
			old:     []id.EventID{a, b},
			current: []id.EventID{a, c},
			want: []PinnedEventsChange{
				{EventID: c, IsPin: true},
				{EventID: b, IsPin: false},
			},
		},
		{
			name: "all pins removed",
			old:  []id.EventID{a, b},
			want: []PinnedEventsChange{{EventID: a, IsPin: false}, {EventID: b, IsPin: false}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := PinnedEventsDiff(
				event.PinnedEventsEventContent{Pinned: tt.old},
				event.PinnedEventsEventContent{Pinned: tt.current},
			)
			if !reflect.DeepEqual(actual, tt.want) {
				t.Errorf("PinnedEventsDiff() = %v, want %v", actual, tt.want)
			}
		})
	}
}

func TestNewPinInChatMessage(t *testing.T) {
	ts := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	key := &waCommon.MessageKey{
		RemoteJID: proto.String("123456789@g.us"),
		ID:        proto.String("ABCDEF"),
	}
	tests := []struct {
		name  string
		isPin bool
		want  waE2E.PinInChatMessage_Type
	}{
		{name: "pin for all", isPin: true, want: waE2E.PinInChatMessage_PIN_FOR_ALL},
		{name: "unpin for all", isPin: false, want: waE2E.PinInChatMessage_UNPIN_FOR_ALL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := NewPinInChatMessage(key, tt.isPin, ts)
			if msg.GetPinInChatMessage().GetType() != tt.want {
				t.Errorf("type = %v, want %v", msg.GetPinInChatMessage().GetType(), tt.want)
			}
			if msg.GetPinInChatMessage().GetKey() != key {
				t.Error("key was not set on the pin message")
			}
			if msg.GetPinInChatMessage().GetSenderTimestampMS() != ts.UnixMilli() {
				t.Errorf("sender timestamp = %d, want %d", msg.GetPinInChatMessage().GetSenderTimestampMS(), ts.UnixMilli())
			}
		})
	}
}
