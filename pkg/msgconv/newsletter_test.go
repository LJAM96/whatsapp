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
	"context"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestToMatrixNewsletterServerID(t *testing.T) {
	channelJID := types.NewJID("123456789012345678", types.NewsletterServer)
	userJID := types.NewJID("1234567890", types.DefaultUserServer)
	tests := []struct {
		name         string
		chat         types.JID
		serverID     types.MessageServerID
		wantServerID types.MessageServerID
	}{
		{
			name:         "channel post keeps server ID",
			chat:         channelJID,
			serverID:     42,
			wantServerID: 42,
		},
		{
			name:         "channel post without server ID",
			chat:         channelJID,
			serverID:     0,
			wantServerID: 0,
		},
		{
			name:         "normal chat ignores server ID",
			chat:         userJID,
			serverID:     42,
			wantServerID: 0,
		},
	}
	mc := &MessageConverter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			portal := &bridgev2.Portal{
				Portal: &database.Portal{
					PortalKey: networkid.PortalKey{ID: networkid.PortalID(tt.chat.String())},
					Metadata:  &waid.PortalMetadata{},
				},
			}
			info := &types.MessageInfo{
				MessageSource: types.MessageSource{Chat: tt.chat, Sender: tt.chat},
				ID:            "ABCD1234",
				ServerID:      tt.serverID,
			}
			converted := mc.ToMatrix(context.Background(), portal, nil, nil,
				&waE2E.Message{Conversation: proto.String("channel post")}, nil, info, false, false, nil)
			if len(converted.Parts) != 1 {
				t.Fatalf("parts = %d, want 1", len(converted.Parts))
			}
			part := converted.Parts[0]
			if part.Content.Body != "channel post" {
				t.Errorf("body = %q, want %q", part.Content.Body, "channel post")
			}
			meta, ok := part.DBMetadata.(*waid.MessageMetadata)
			if !ok {
				t.Fatalf("DBMetadata = %T, want *waid.MessageMetadata", part.DBMetadata)
			}
			if meta.NewsletterServerID != tt.wantServerID {
				t.Errorf("NewsletterServerID = %d, want %d", meta.NewsletterServerID, tt.wantServerID)
			}
		})
	}
}
