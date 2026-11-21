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

package waid

import (
	"encoding/json"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestMessageMetadataCopyFromNewsletterServerID(t *testing.T) {
	tests := []struct {
		name string
		from types.MessageServerID
		want types.MessageServerID
	}{
		{name: "server ID is copied", from: 42, want: 42},
		{name: "zero server ID stays zero", from: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dst MessageMetadata
			dst.CopyFrom(&MessageMetadata{NewsletterServerID: tt.from})
			if dst.NewsletterServerID != tt.want {
				t.Errorf("NewsletterServerID = %d, want %d", dst.NewsletterServerID, tt.want)
			}
		})
	}
}

func TestMessageMetadataNewsletterServerIDJSON(t *testing.T) {
	tests := []struct {
		name string
		meta MessageMetadata
		want string
	}{
		{
			name: "server ID is serialized",
			meta: MessageMetadata{NewsletterServerID: 42},
			want: `{"newsletter_server_id":42}`,
		},
		{
			name: "zero server ID is omitted",
			meta: MessageMetadata{},
			want: `{}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(&tt.meta)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want {
				t.Errorf("JSON = %s, want %s", data, tt.want)
			}
		})
	}
}
