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
	"context"
	"reflect"
	"testing"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func communityTestClient() *WhatsAppClient {
	return &WhatsAppClient{
		Main: &WhatsAppConnector{
			Bridge: &bridgev2.Bridge{},
			Config: Config{MaxMemberSync: -1},
		},
		JID: types.NewJID("123456789", types.DefaultUserServer),
		LID: types.NewJID("987654321", types.HiddenUserServer),
	}
}

func TestWrapGroupInfoCommunity(t *testing.T) {
	groupJID := types.NewJID("111111111", types.GroupServer)
	parentJID := types.NewJID("222222222", types.GroupServer)
	tests := []struct {
		name              string
		info              types.GroupInfo
		wantType          database.RoomType
		wantParent        *networkid.PortalID
		wantIsFull        bool
		wantEventsDefault int
	}{
		{
			name:              "plain group",
			info:              types.GroupInfo{JID: groupJID, GroupName: types.GroupName{Name: "Group"}},
			wantType:          database.RoomTypeDefault,
			wantIsFull:        true,
			wantEventsDefault: defaultPL,
		},
		{
			name: "parent community is a space",
			info: types.GroupInfo{
				JID:         parentJID,
				GroupName:   types.GroupName{Name: "Community"},
				GroupParent: types.GroupParent{IsParent: true},
			},
			wantType:          database.RoomTypeSpace,
			wantIsFull:        false,
			wantEventsDefault: defaultPL,
		},
		{
			name: "linked group gets parent portal ID",
			info: types.GroupInfo{
				JID:               groupJID,
				GroupName:         types.GroupName{Name: "Linked"},
				GroupLinkedParent: types.GroupLinkedParent{LinkedParentJID: parentJID},
			},
			wantType:          database.RoomTypeDefault,
			wantParent:        ptr.Ptr(waid.MakePortalID(parentJID)),
			wantIsFull:        true,
			wantEventsDefault: defaultPL,
		},
		{
			name: "community announcement group stays a normal portal",
			info: types.GroupInfo{
				JID:               groupJID,
				GroupName:         types.GroupName{Name: "Announcements"},
				GroupAnnounce:     types.GroupAnnounce{IsAnnounce: true},
				GroupIsDefaultSub: types.GroupIsDefaultSub{IsDefaultSubGroup: true},
				GroupLinkedParent: types.GroupLinkedParent{LinkedParentJID: parentJID},
			},
			wantType:          database.RoomTypeDefault,
			wantParent:        ptr.Ptr(waid.MakePortalID(parentJID)),
			wantIsFull:        true,
			wantEventsDefault: defaultPL,
		},
		{
			name: "read-only announcement group restricts sends to admins",
			info: types.GroupInfo{
				JID:           groupJID,
				GroupName:     types.GroupName{Name: "ReadOnly"},
				GroupAnnounce: types.GroupAnnounce{IsAnnounce: true},
			},
			wantType:          database.RoomTypeDefault,
			wantIsFull:        true,
			wantEventsDefault: adminPL,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := communityTestClient().wrapGroupInfo(context.Background(), &tc.info)
			if info.Type == nil || *info.Type != tc.wantType {
				t.Errorf("Type = %v, want %v", info.Type, tc.wantType)
			}
			if !reflect.DeepEqual(info.ParentID, tc.wantParent) {
				t.Errorf("ParentID = %v, want %v", info.ParentID, tc.wantParent)
			}
			if info.Members == nil || info.Members.IsFull != tc.wantIsFull {
				t.Errorf("Members.IsFull = %v, want %v", info.Members, tc.wantIsFull)
			}
			if info.Members == nil || info.Members.PowerLevels == nil || info.Members.PowerLevels.EventsDefault == nil {
				t.Fatalf("missing EventsDefault power level: %+v", info.Members)
			}
			if actual := *info.Members.PowerLevels.EventsDefault; actual != tc.wantEventsDefault {
				t.Errorf("EventsDefault = %d, want %d", actual, tc.wantEventsDefault)
			}
		})
	}
}

func TestSetDefaultSubGroupFlag(t *testing.T) {
	tests := []struct {
		name       string
		initial    bool
		isCAG      bool
		want       bool
		wantChange bool
	}{
		{"normal group stays unflagged", false, false, false, false},
		{"normal group becomes CAG", false, true, true, true},
		{"CAG stays flagged", true, true, true, false},
		{"CAG flag cleared", true, false, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			portal := &bridgev2.Portal{
				Portal: &database.Portal{Metadata: &waid.PortalMetadata{CommunityAnnouncementGroup: tc.initial}},
			}
			changed := setDefaultSubGroupFlag(tc.isCAG)(context.Background(), portal)
			if changed != tc.wantChange {
				t.Errorf("changed = %v, want %v", changed, tc.wantChange)
			}
			if actual := portal.Metadata.(*waid.PortalMetadata).CommunityAnnouncementGroup; actual != tc.want {
				t.Errorf("CommunityAnnouncementGroup = %v, want %v", actual, tc.want)
			}
		})
	}
}

func TestWrapGroupInfoChangeLink(t *testing.T) {
	groupJID := types.NewJID("111111111", types.GroupServer)
	parentJID := types.NewJID("222222222", types.GroupServer)
	newParentJID := types.NewJID("333333333", types.GroupServer)
	tests := []struct {
		name       string
		evt        *events.GroupInfo
		wantChange bool
		wantParent *networkid.PortalID
	}{
		{
			name:       "unrelated change doesn't touch parent",
			evt:        &events.GroupInfo{JID: groupJID, Topic: &types.GroupTopic{Topic: "new topic"}},
			wantChange: true,
		},
		{
			name: "child linked to parent",
			evt: &events.GroupInfo{
				JID: groupJID,
				Link: &types.GroupLinkChange{
					Type:  types.GroupLinkChangeTypeParent,
					Group: types.GroupLinkTarget{JID: parentJID},
				},
			},
			wantChange: true,
			wantParent: ptr.Ptr(waid.MakePortalID(parentJID)),
		},
		{
			name: "sub group link on parent doesn't set parent",
			evt: &events.GroupInfo{
				JID: parentJID,
				Link: &types.GroupLinkChange{
					Type:  types.GroupLinkChangeTypeSub,
					Group: types.GroupLinkTarget{JID: groupJID},
				},
			},
			wantChange: true,
		},
		{
			name: "group unlinked from parent",
			evt: &events.GroupInfo{
				JID: groupJID,
				Unlink: &types.GroupLinkChange{
					Type:         types.GroupLinkChangeTypeParent,
					UnlinkReason: types.GroupUnlinkReasonDefault,
					Group:        types.GroupLinkTarget{JID: parentJID},
				},
			},
			wantChange: true,
			wantParent: ptr.Ptr(networkid.PortalID("")),
		},
		{
			name: "group relinked to new parent",
			evt: &events.GroupInfo{
				JID: groupJID,
				Link: &types.GroupLinkChange{
					Type:  types.GroupLinkChangeTypeParent,
					Group: types.GroupLinkTarget{JID: newParentJID},
				},
			},
			wantChange: true,
			wantParent: ptr.Ptr(waid.MakePortalID(newParentJID)),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			change := communityTestClient().wrapGroupInfoChange(context.Background(), tc.evt)
			if change == nil {
				t.Fatal("wrapGroupInfoChange returned nil")
			}
			if (change.ChatInfo != nil) != tc.wantChange {
				t.Fatalf("ChatInfo presence = %v, want %v", change.ChatInfo != nil, tc.wantChange)
			}
			if change.ChatInfo == nil {
				return
			}
			if !reflect.DeepEqual(change.ChatInfo.ParentID, tc.wantParent) {
				t.Errorf("ParentID = %v, want %v", change.ChatInfo.ParentID, tc.wantParent)
			}
		})
	}
}

func TestWrapGroupInfoChangeAnnounce(t *testing.T) {
	tests := []struct {
		name              string
		announce          *types.GroupAnnounce
		wantEventsDefault int
	}{
		{"announce on", &types.GroupAnnounce{IsAnnounce: true}, adminPL},
		{"announce off", &types.GroupAnnounce{IsAnnounce: false}, defaultPL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			change := communityTestClient().wrapGroupInfoChange(context.Background(), &events.GroupInfo{
				JID:      types.NewJID("111111111", types.GroupServer),
				Announce: tc.announce,
			})
			if change.MemberChanges == nil || change.MemberChanges.PowerLevels == nil {
				t.Fatalf("missing power level changes: %+v", change.MemberChanges)
			}
			actual := change.MemberChanges.PowerLevels.EventsDefault
			if actual == nil || *actual != tc.wantEventsDefault {
				t.Errorf("EventsDefault = %v, want %d", actual, tc.wantEventsDefault)
			}
			for _, evtType := range []event.Type{event.EventEncrypted, event.EventReaction, event.EventRedaction, event.EventUnstablePollResponse} {
				level, ok := change.MemberChanges.PowerLevels.Events[evtType]
				if !ok || level != defaultPL {
					t.Errorf("Events[%s] = %d (present %v), want %d", evtType.Type, level, ok, defaultPL)
				}
			}
		})
	}
}
