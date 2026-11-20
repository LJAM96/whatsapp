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
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

func TestEventResponseToEmoji(t *testing.T) {
	tests := []struct {
		name     string
		response waE2E.EventResponseMessage_EventResponseType
		want     string
	}{
		{"going", waE2E.EventResponseMessage_GOING, "👍"},
		{"not going", waE2E.EventResponseMessage_NOT_GOING, "👎"},
		{"maybe", waE2E.EventResponseMessage_MAYBE, "🤷"},
		{"unknown", waE2E.EventResponseMessage_UNKNOWN, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EventResponseToEmoji(tt.response); got != tt.want {
				t.Errorf("EventResponseToEmoji(%v) = %q, want %q", tt.response, got, tt.want)
			}
		})
	}
}

func TestEmojiToEventResponse(t *testing.T) {
	tests := []struct {
		name   string
		emoji  string
		want   waE2E.EventResponseMessage_EventResponseType
		wantOK bool
	}{
		{"going", "👍", waE2E.EventResponseMessage_GOING, true},
		{"not going", "👎", waE2E.EventResponseMessage_NOT_GOING, true},
		{"maybe", "🤷", waE2E.EventResponseMessage_MAYBE, true},
		{"going with variation selector", "👍️", waE2E.EventResponseMessage_GOING, true},
		{"maybe with variation selector", "🤷️", waE2E.EventResponseMessage_MAYBE, true},
		{"not an RSVP emoji", "❤️", waE2E.EventResponseMessage_UNKNOWN, false},
		{"empty", "", waE2E.EventResponseMessage_UNKNOWN, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := EmojiToEventResponse(tt.emoji)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("EmojiToEventResponse(%q) = (%v, %v), want (%v, %v)", tt.emoji, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
