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
	"maps"
	"slices"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// PinExpiryKey is an unstable field in m.room.pinned_events mapping a pinned
// event ID to the Unix millisecond timestamp when the WhatsApp pin expires.
//
// The Matrix spec's m.room.pinned_events content has no way to represent a pin
// duration, so the duration is kept in this unstable field as a last resort.
const PinExpiryKey = "fi.mau.whatsapp.pin_expiry"

// PinInChatToMatrix applies a WhatsApp PinInChatMessage to the current
// m.room.pinned_events state event. eventID is the Matrix event ID of the
// message targeted by the pin. The boolean result is false when the current
// state already matches the requested pin state.
func PinInChatToMatrix(current *event.Event, msg *waE2E.Message, eventID id.EventID, ts time.Time) (*event.Content, bool) {
	pin := msg.GetPinInChatMessage()
	if pin == nil || eventID == "" {
		return nil, false
	}
	var content event.PinnedEventsEventContent
	if current != nil {
		content = *current.Content.AsPinnedEvents()
	}
	idx := slices.Index(content.Pinned, eventID)
	isPin := pin.GetType() == waE2E.PinInChatMessage_PIN_FOR_ALL
	if isPin {
		if idx >= 0 {
			return nil, false
		}
		content.Pinned = append(slices.Clone(content.Pinned), eventID)
	} else if pin.GetType() == waE2E.PinInChatMessage_UNPIN_FOR_ALL {
		if idx < 0 {
			return nil, false
		}
		content.Pinned = slices.Delete(slices.Clone(content.Pinned), idx, idx+1)
	} else {
		return nil, false
	}
	out := &event.Content{Parsed: &content}

	var expiries map[string]any
	if current != nil {
		expiries, _ = current.Content.Raw[PinExpiryKey].(map[string]any)
	}
	expiries = maps.Clone(expiries)
	if isPin {
		duration := msg.GetMessageContextInfo().GetMessageAddOnDurationInSecs()
		if duration > 0 {
			if expiries == nil {
				expiries = make(map[string]any)
			}
			expiries[string(eventID)] = ts.Add(time.Duration(duration) * time.Second).UnixMilli()
		}
	} else if expiries != nil {
		delete(expiries, string(eventID))
	}
	if len(expiries) > 0 {
		out.Raw = map[string]any{PinExpiryKey: expiries}
	}
	return out, true
}

// PinnedEventsChange is a single difference between two m.room.pinned_events
// contents.
type PinnedEventsChange struct {
	EventID id.EventID
	IsPin   bool
}

// PinnedEventsDiff returns the changes needed to turn old into current.
func PinnedEventsDiff(old, current event.PinnedEventsEventContent) []PinnedEventsChange {
	var changes []PinnedEventsChange
	for _, eventID := range current.Pinned {
		if !slices.Contains(old.Pinned, eventID) {
			changes = append(changes, PinnedEventsChange{EventID: eventID, IsPin: true})
		}
	}
	for _, eventID := range old.Pinned {
		if !slices.Contains(current.Pinned, eventID) {
			changes = append(changes, PinnedEventsChange{EventID: eventID, IsPin: false})
		}
	}
	return changes
}

// NewPinInChatMessage builds a WhatsApp message that pins or unpins the
// message identified by key. The key is built from the bridged message ID
// with whatsmeow's waCommon.MessageKey type.
func NewPinInChatMessage(key *waCommon.MessageKey, isPin bool, ts time.Time) *waE2E.Message {
	typ := waE2E.PinInChatMessage_UNPIN_FOR_ALL
	if isPin {
		typ = waE2E.PinInChatMessage_PIN_FOR_ALL
	}
	return &waE2E.Message{
		PinInChatMessage: &waE2E.PinInChatMessage{
			Key:               key,
			Type:              typ.Enum(),
			SenderTimestampMS: proto.Int64(ts.UnixMilli()),
		},
	}
}
