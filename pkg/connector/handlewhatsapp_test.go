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
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/bridgev2"
)

func TestChatPresenceToTyping(t *testing.T) {
	tests := []struct {
		name        string
		state       types.ChatPresence
		media       types.ChatPresenceMedia
		wantType    bridgev2.TypingType
		wantTimeout time.Duration
	}{
		{
			name:        "composing text",
			state:       types.ChatPresenceComposing,
			media:       types.ChatPresenceMediaText,
			wantType:    bridgev2.TypingTypeText,
			wantTimeout: 15 * time.Second,
		},
		{
			name:        "composing audio degrades to text typing",
			state:       types.ChatPresenceComposing,
			media:       types.ChatPresenceMediaAudio,
			wantType:    bridgev2.TypingTypeText,
			wantTimeout: 15 * time.Second,
		},
		{
			name:        "paused text",
			state:       types.ChatPresencePaused,
			media:       types.ChatPresenceMediaText,
			wantType:    bridgev2.TypingTypeText,
			wantTimeout: 0,
		},
		{
			name:        "paused audio",
			state:       types.ChatPresencePaused,
			media:       types.ChatPresenceMediaAudio,
			wantType:    bridgev2.TypingTypeText,
			wantTimeout: 0,
		},
		{
			name:        "unknown media",
			state:       types.ChatPresenceComposing,
			media:       types.ChatPresenceMedia("video"),
			wantType:    bridgev2.TypingTypeText,
			wantTimeout: 15 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotTimeout := chatPresenceToTyping(&events.ChatPresence{
				State: tt.state,
				Media: tt.media,
			})
			if gotType != tt.wantType {
				t.Errorf("chatPresenceToTyping() type = %v, want %v", gotType, tt.wantType)
			}
			if gotTimeout != tt.wantTimeout {
				t.Errorf("chatPresenceToTyping() timeout = %v, want %v", gotTimeout, tt.wantTimeout)
			}
		})
	}
}
