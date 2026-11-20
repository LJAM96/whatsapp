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

	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
)

func TestMatrixTypingToChatPresence(t *testing.T) {
	tests := []struct {
		name        string
		isTyping    bool
		typingType  bridgev2.TypingType
		wantState   types.ChatPresence
		wantMedia   types.ChatPresenceMedia
		wantHandled bool
	}{
		{
			name:        "typing text",
			isTyping:    true,
			typingType:  bridgev2.TypingTypeText,
			wantState:   types.ChatPresenceComposing,
			wantMedia:   types.ChatPresenceMediaText,
			wantHandled: true,
		},
		{
			name:        "stopped typing text",
			isTyping:    false,
			typingType:  bridgev2.TypingTypeText,
			wantState:   types.ChatPresencePaused,
			wantMedia:   types.ChatPresenceMediaText,
			wantHandled: true,
		},
		{
			name:        "recording media sends audio presence",
			isTyping:    true,
			typingType:  bridgev2.TypingTypeRecordingMedia,
			wantState:   types.ChatPresenceComposing,
			wantMedia:   types.ChatPresenceMediaAudio,
			wantHandled: true,
		},
		{
			name:        "stopped recording media",
			isTyping:    false,
			typingType:  bridgev2.TypingTypeRecordingMedia,
			wantState:   types.ChatPresencePaused,
			wantMedia:   types.ChatPresenceMediaAudio,
			wantHandled: true,
		},
		{
			name:        "uploading media is ignored",
			isTyping:    true,
			typingType:  bridgev2.TypingTypeUploadingMedia,
			wantHandled: false,
		},
		{
			name:        "stopped uploading media is ignored",
			isTyping:    false,
			typingType:  bridgev2.TypingTypeUploadingMedia,
			wantHandled: false,
		},
		{
			name:        "unknown type falls back to text",
			isTyping:    true,
			typingType:  bridgev2.TypingType(99),
			wantState:   types.ChatPresenceComposing,
			wantMedia:   types.ChatPresenceMediaText,
			wantHandled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotState, gotMedia, gotHandled := matrixTypingToChatPresence(&bridgev2.MatrixTyping{
				IsTyping: tt.isTyping,
				Type:     tt.typingType,
			})
			if gotHandled != tt.wantHandled {
				t.Errorf("matrixTypingToChatPresence() handled = %v, want %v", gotHandled, tt.wantHandled)
			}
			if gotState != tt.wantState {
				t.Errorf("matrixTypingToChatPresence() state = %q, want %q", gotState, tt.wantState)
			}
			if gotMedia != tt.wantMedia {
				t.Errorf("matrixTypingToChatPresence() media = %q, want %q", gotMedia, tt.wantMedia)
			}
		})
	}
}
