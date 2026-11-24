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
	"reflect"
	"testing"
	"time"

	"go.mau.fi/util/jsontime"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestGroupCreateRequest(t *testing.T) {
	communityJID := types.NewJID("15551234567", types.GroupServer)
	week := 7 * 24 * time.Hour
	tests := []struct {
		name    string
		params  bridgev2.GroupCreateParams
		want    whatsmeow.ReqCreateGroup
		wantErr bool
	}{
		{
			name: "no optional fields",
		},
		{
			name:   "name",
			params: bridgev2.GroupCreateParams{Name: &event.RoomNameEventContent{Name: "Weave testers"}},
			want:   whatsmeow.ReqCreateGroup{Name: "Weave testers"},
		},
		{
			name: "empty name",
			params: bridgev2.GroupCreateParams{
				Name: &event.RoomNameEventContent{},
			},
		},
		{
			name: "linked parent",
			params: bridgev2.GroupCreateParams{
				Parent: &networkid.PortalKey{ID: waid.MakePortalID(communityJID)},
			},
			want: whatsmeow.ReqCreateGroup{
				GroupLinkedParent: types.GroupLinkedParent{LinkedParentJID: communityJID},
			},
		},
		{
			name: "invalid parent ID",
			params: bridgev2.GroupCreateParams{
				Parent: &networkid.PortalKey{ID: "a.b.c@invalid"},
			},
			wantErr: true,
		},
		{
			name: "disappearing timer is converted to seconds",
			params: bridgev2.GroupCreateParams{
				Disappear: &event.BeeperDisappearingTimer{Timer: jsontime.MS(week)},
			},
			want: whatsmeow.ReqCreateGroup{
				GroupEphemeral: types.GroupEphemeral{
					IsEphemeral:       true,
					DisappearingTimer: uint32(week.Seconds()),
				},
			},
		},
		{
			name: "zero disappearing timer still enables ephemeral mode",
			params: bridgev2.GroupCreateParams{
				Disappear: &event.BeeperDisappearingTimer{},
			},
			want: whatsmeow.ReqCreateGroup{
				GroupEphemeral: types.GroupEphemeral{IsEphemeral: true},
			},
		},
		{
			name: "name, parent and disappearing timer together",
			params: bridgev2.GroupCreateParams{
				Name:      &event.RoomNameEventContent{Name: "Community group"},
				Parent:    &networkid.PortalKey{ID: waid.MakePortalID(communityJID)},
				Disappear: &event.BeeperDisappearingTimer{Timer: jsontime.MS(24 * time.Hour)},
			},
			want: whatsmeow.ReqCreateGroup{
				Name:              "Community group",
				GroupLinkedParent: types.GroupLinkedParent{LinkedParentJID: communityJID},
				GroupEphemeral: types.GroupEphemeral{
					IsEphemeral:       true,
					DisappearingTimer: uint32((24 * time.Hour).Seconds()),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := groupCreateRequest(&tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("groupCreateRequest() error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("groupCreateRequest() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
