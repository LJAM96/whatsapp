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

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestParseNewsletterTarget(t *testing.T) {
	channelJID := types.NewJID("123456789012345678", types.NewsletterServer)
	tests := []struct {
		name       string
		arg        string
		wantJID    types.JID
		wantInvite string
		wantErr    bool
	}{
		{
			name:       "invite link",
			arg:        "https://whatsapp.com/channel/ABCDEF1234",
			wantInvite: "https://whatsapp.com/channel/ABCDEF1234",
		},
		{
			name:    "channel JID",
			arg:     channelJID.String(),
			wantJID: channelJID,
		},
		{
			name:    "group JID is rejected",
			arg:     "1234567890-1234567890@g.us",
			wantErr: true,
		},
		{
			name:    "user JID is rejected",
			arg:     "1234567890@s.whatsapp.net",
			wantErr: true,
		},
		{
			name:    "garbage is rejected",
			arg:     "not-a-channel",
			wantErr: true,
		},
		{
			name:    "empty is rejected",
			arg:     "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jid, invite, err := parseNewsletterTarget(tt.arg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseNewsletterTarget(%q) error = %v, wantErr %v", tt.arg, err, tt.wantErr)
			}
			if jid != tt.wantJID {
				t.Errorf("jid = %s, want %s", jid, tt.wantJID)
			}
			if invite != tt.wantInvite {
				t.Errorf("invite = %q, want %q", invite, tt.wantInvite)
			}
		})
	}
}

func TestGetNewsletterServerID(t *testing.T) {
	tests := []struct {
		name      string
		meta      any
		wantID    types.MessageServerID
		wantFound bool
	}{
		{
			name:      "message metadata",
			meta:      &waid.MessageMetadata{NewsletterServerID: 42},
			wantID:    42,
			wantFound: true,
		},
		{
			name:   "message metadata without server ID",
			meta:   &waid.MessageMetadata{},
			wantID: 0,
		},
		{
			name:      "reaction metadata",
			meta:      &waid.ReactionMetadata{NewsletterServerID: 7},
			wantID:    7,
			wantFound: true,
		},
		{
			name:   "reaction metadata without server ID",
			meta:   &waid.ReactionMetadata{},
			wantID: 0,
		},
		{
			name:   "nil metadata",
			meta:   nil,
			wantID: 0,
		},
		{
			name:   "unrelated metadata",
			meta:   &waid.PortalMetadata{},
			wantID: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, found := getNewsletterServerID(tt.meta)
			if id != tt.wantID || found != tt.wantFound {
				t.Errorf("getNewsletterServerID(%T) = (%d, %v), want (%d, %v)", tt.meta, id, found, tt.wantID, tt.wantFound)
			}
		})
	}
}
