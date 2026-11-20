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
	"go.mau.fi/util/variationselector"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

// Matrix has no standard event for RSVPs (see upstream mautrix/whatsapp#803),
// so WhatsApp event responses are bridged as reactions with the emojis proposed
// in that issue. Keep the mapping in one place for both directions.
const (
	eventResponseGoing    = "👍"
	eventResponseNotGoing = "👎"
	eventResponseMaybe    = "🤷"
)

// EventResponseToEmoji maps a WhatsApp event response to the Matrix reaction
// used to represent it. It returns an empty string for unknown responses.
func EventResponseToEmoji(response waE2E.EventResponseMessage_EventResponseType) string {
	switch response {
	case waE2E.EventResponseMessage_GOING:
		return eventResponseGoing
	case waE2E.EventResponseMessage_NOT_GOING:
		return eventResponseNotGoing
	case waE2E.EventResponseMessage_MAYBE:
		return eventResponseMaybe
	default:
		return ""
	}
}

// EmojiToEventResponse maps a Matrix reaction emoji to a WhatsApp event
// response. Variation selectors are ignored so both 👍 and 👍️ work.
func EmojiToEventResponse(emoji string) (waE2E.EventResponseMessage_EventResponseType, bool) {
	switch variationselector.Remove(emoji) {
	case eventResponseGoing:
		return waE2E.EventResponseMessage_GOING, true
	case eventResponseNotGoing:
		return waE2E.EventResponseMessage_NOT_GOING, true
	case eventResponseMaybe:
		return waE2E.EventResponseMessage_MAYBE, true
	default:
		return waE2E.EventResponseMessage_UNKNOWN, false
	}
}
