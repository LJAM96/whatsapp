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
)

func TestSkipStatusReceipt(t *testing.T) {
	tests := []struct {
		name     string
		portal   types.JID
		implicit bool
		want     bool
	}{
		{
			"implicit receipt in status room is dropped",
			types.StatusBroadcastJID,
			true,
			true,
		},
		{
			"explicit mark-read in status room is forwarded",
			types.StatusBroadcastJID,
			false,
			false,
		},
		{
			"implicit receipt in DM is forwarded",
			types.NewJID("123", types.DefaultUserServer),
			true,
			false,
		},
		{
			"explicit receipt in DM is forwarded",
			types.NewJID("123", types.DefaultUserServer),
			false,
			false,
		},
		{
			"broadcast list is not the status room",
			types.NewJID("list", types.BroadcastServer),
			true,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := skipStatusReceipt(tt.portal, tt.implicit); got != tt.want {
				t.Errorf("skipStatusReceipt(%s, %v) = %v, want %v", tt.portal, tt.implicit, got, tt.want)
			}
		})
	}
}
