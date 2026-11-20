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
	"slices"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestAdminPLAction(t *testing.T) {
	tests := []struct {
		name     string
		old, new int
		expected whatsmeow.ParticipantChange
	}{
		{"member stays member", 0, 0, ""},
		{"member promoted", 0, adminPL, whatsmeow.ParticipantChangePromote},
		{"member promoted to super admin", 0, superAdminPL, whatsmeow.ParticipantChangePromote},
		{"admin stays admin", adminPL, adminPL, ""},
		{"admin promoted to super admin", adminPL, superAdminPL, ""},
		{"super admin stays super admin", superAdminPL, superAdminPL, ""},
		{"super admin demoted to admin", superAdminPL, adminPL, ""},
		{"admin demoted", adminPL, 0, whatsmeow.ParticipantChangeDemote},
		{"super admin demoted", superAdminPL, 0, whatsmeow.ParticipantChangeDemote},
		{"level below admin boundary is ignored", 0, adminPL - 1, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if actual := adminPLAction(tc.old, tc.new); actual != tc.expected {
				t.Errorf("adminPLAction(%d, %d) = %q, want %q", tc.old, tc.new, actual, tc.expected)
			}
		})
	}
}

func TestSettingFromPLChange(t *testing.T) {
	tests := []struct {
		name     string
		change   *bridgev2.SinglePowerLevelChange
		expected *bool
	}{
		{"no change", nil, nil},
		{"level unchanged", &bridgev2.SinglePowerLevelChange{OrigLevel: 0, NewLevel: 0}, nil},
		{"restriction enabled", &bridgev2.SinglePowerLevelChange{OrigLevel: 0, NewLevel: adminPL}, ptrBool(true)},
		{"restriction disabled", &bridgev2.SinglePowerLevelChange{OrigLevel: adminPL, NewLevel: 0}, ptrBool(false)},
		{"both levels restricted", &bridgev2.SinglePowerLevelChange{OrigLevel: adminPL, NewLevel: superAdminPL}, nil},
		{"both levels unrestricted", &bridgev2.SinglePowerLevelChange{OrigLevel: 0, NewLevel: adminPL - 1}, nil},
		{"restriction stays enabled after super admin change", &bridgev2.SinglePowerLevelChange{OrigLevel: superAdminPL, NewLevel: adminPL}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if actual := settingFromPLChange(tc.change); !reflect.DeepEqual(actual, tc.expected) {
				t.Errorf("settingFromPLChange(%+v) = %v, want %v", tc.change, actual, tc.expected)
			}
		})
	}
}

func TestClassifyGroupAdminChanges(t *testing.T) {
	ghost := func(userID string) *bridgev2.Ghost {
		return &bridgev2.Ghost{Ghost: &database.Ghost{ID: networkid.UserID(userID)}}
	}
	userChange := func(target bridgev2.GhostOrUserLogin, old, new int) *bridgev2.UserPowerLevelChange {
		return &bridgev2.UserPowerLevelChange{
			Target:                 target,
			SinglePowerLevelChange: bridgev2.SinglePowerLevelChange{OrigLevel: old, NewLevel: new},
		}
	}
	tests := []struct {
		name     string
		input    *bridgev2.MatrixPowerLevelChange
		expected groupAdminChanges
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: groupAdminChanges{},
		},
		{
			name:     "empty change",
			input:    &bridgev2.MatrixPowerLevelChange{},
			expected: groupAdminChanges{},
		},
		{
			name: "promote and demote",
			input: &bridgev2.MatrixPowerLevelChange{
				Users: map[id.UserID]*bridgev2.UserPowerLevelChange{
					"@a:example.org": userChange(ghost("111"), 0, adminPL),
					"@b:example.org": userChange(ghost("222"), adminPL, 0),
					"@c:example.org": userChange(ghost("333"), adminPL, superAdminPL),
					"@d:example.org": nil,
				},
			},
			expected: groupAdminChanges{
				Promote: []networkid.UserID{"111"},
				Demote:  []networkid.UserID{"222"},
			},
		},
		{
			name: "local login promoted",
			input: &bridgev2.MatrixPowerLevelChange{
				Users: map[id.UserID]*bridgev2.UserPowerLevelChange{
					"@self:example.org": userChange(&bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "55555"}}, 0, adminPL),
				},
			},
			expected: groupAdminChanges{Promote: []networkid.UserID{"55555"}},
		},
		{
			name: "announce enabled from events default",
			input: &bridgev2.MatrixPowerLevelChange{
				EventsDefault: &bridgev2.SinglePowerLevelChange{OrigLevel: 0, NewLevel: adminPL},
			},
			expected: groupAdminChanges{Announce: ptrBool(true)},
		},
		{
			name: "announce disabled from events default",
			input: &bridgev2.MatrixPowerLevelChange{
				EventsDefault: &bridgev2.SinglePowerLevelChange{OrigLevel: adminPL, NewLevel: 0},
			},
			expected: groupAdminChanges{Announce: ptrBool(false)},
		},
		{
			name: "locked enabled from room name level",
			input: &bridgev2.MatrixPowerLevelChange{
				Events: map[string]*bridgev2.SinglePowerLevelChange{
					event.StateRoomName.Type: {OrigLevel: 0, NewLevel: adminPL},
				},
			},
			expected: groupAdminChanges{Locked: ptrBool(true)},
		},
		{
			name: "unrelated event level does not change locked",
			input: &bridgev2.MatrixPowerLevelChange{
				Events: map[string]*bridgev2.SinglePowerLevelChange{
					event.EventReaction.Type: {OrigLevel: 0, NewLevel: adminPL},
				},
			},
			expected: groupAdminChanges{},
		},
		{
			name: "all changes at once",
			input: &bridgev2.MatrixPowerLevelChange{
				Users: map[id.UserID]*bridgev2.UserPowerLevelChange{
					"@a:example.org": userChange(ghost("111"), 0, adminPL),
				},
				EventsDefault: &bridgev2.SinglePowerLevelChange{OrigLevel: 0, NewLevel: adminPL},
				Events: map[string]*bridgev2.SinglePowerLevelChange{
					event.StateRoomName.Type: {OrigLevel: 0, NewLevel: adminPL},
				},
			},
			expected: groupAdminChanges{
				Promote:  []networkid.UserID{"111"},
				Announce: ptrBool(true),
				Locked:   ptrBool(true),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := classifyGroupAdminChanges(tc.input)
			slices.Sort(actual.Promote)
			slices.Sort(actual.Demote)
			slices.Sort(tc.expected.Promote)
			slices.Sort(tc.expected.Demote)
			if !reflect.DeepEqual(actual, tc.expected) {
				t.Errorf("classifyGroupAdminChanges() = %+v, want %+v", actual, tc.expected)
			}
		})
	}
}

func TestJoinRuleConversion(t *testing.T) {
	tests := []struct {
		name     string
		required bool
		rule     event.JoinRule
		prev     event.JoinRule
	}{
		{"approval not required", false, event.JoinRuleInvite, event.JoinRuleKnock},
		{"approval required", true, event.JoinRuleKnock, event.JoinRuleInvite},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content := joinRuleForApprovalMode(tc.required)
			if content.JoinRule != tc.rule {
				t.Errorf("joinRuleForApprovalMode(%v) = %q, want %q", tc.required, content.JoinRule, tc.rule)
			}

			if actual := joinApprovalFromJoinRule(tc.prev, content.JoinRule); actual == nil || *actual != tc.required {
				t.Errorf("joinApprovalFromJoinRule(%q, %q) = %v, want %v", tc.prev, content.JoinRule, actual, tc.required)
			}
		})
	}

	toggleTests := []struct {
		name          string
		prev, current event.JoinRule
		expected      *bool
	}{
		{"invite to knock", event.JoinRuleInvite, event.JoinRuleKnock, ptrBool(true)},
		{"knock to invite", event.JoinRuleKnock, event.JoinRuleInvite, ptrBool(false)},
		{"invite to invite", event.JoinRuleInvite, event.JoinRuleInvite, nil},
		{"knock to knock", event.JoinRuleKnock, event.JoinRuleKnock, nil},
		{"public to knock", event.JoinRulePublic, event.JoinRuleKnock, ptrBool(true)},
		{"knock to public", event.JoinRuleKnock, event.JoinRulePublic, ptrBool(false)},
	}
	for _, tc := range toggleTests {
		t.Run(tc.name, func(t *testing.T) {
			if actual := joinApprovalFromJoinRule(tc.prev, tc.current); !reflect.DeepEqual(actual, tc.expected) {
				t.Errorf("joinApprovalFromJoinRule(%q, %q) = %v, want %v", tc.prev, tc.current, actual, tc.expected)
			}
		})
	}
}

func TestJoinApprovalFromChange(t *testing.T) {
	dmID := waid.MakePortalID(types.NewJID("15551234567", types.DefaultUserServer))
	rule := func(r event.JoinRule) *event.JoinRulesEventContent {
		return &event.JoinRulesEventContent{JoinRule: r}
	}
	tests := []struct {
		name    string
		prev    *event.JoinRulesEventContent
		current event.JoinRule
		want    *bool
	}{
		{"invite to knock requires approval", nil, event.JoinRuleKnock, ptrBool(true)},
		{"knock to invite clears approval", rule(event.JoinRuleKnock), event.JoinRuleInvite, ptrBool(false)},
		{"missing prev defaults to invite", nil, event.JoinRuleInvite, nil},
		{"no rule change", rule(event.JoinRuleKnock), event.JoinRuleKnock, nil},
		{"public to knock requires approval", rule(event.JoinRulePublic), event.JoinRuleKnock, ptrBool(true)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := &bridgev2.MatrixJoinRuleChange{
				MatrixEventBase: bridgev2.MatrixEventBase[*event.JoinRulesEventContent]{
					Content: rule(tc.current),
					Portal:  &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{ID: dmID}}},
				},
				PrevContent: tc.prev,
			}
			if actual := joinApprovalFromChange(msg); !reflect.DeepEqual(actual, tc.want) {
				t.Errorf("joinApprovalFromChange() = %v, want %v", actual, tc.want)
			}
		})
	}
}

func TestHandleMatrixJoinRuleIgnoresUnhandledChanges(t *testing.T) {
	groupID := waid.MakePortalID(types.NewJID("15551234567", types.GroupServer))
	dmID := waid.MakePortalID(types.NewJID("15557654321", types.DefaultUserServer))
	portal := func(id networkid.PortalID) *bridgev2.Portal {
		return &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{ID: id}}}
	}
	tests := []struct {
		name    string
		msg     *bridgev2.MatrixJoinRuleChange
		wantErr bool
	}{
		{name: "nil message"},
		{name: "nil portal", msg: &bridgev2.MatrixJoinRuleChange{}},
		{name: "nil content", msg: &bridgev2.MatrixJoinRuleChange{
			MatrixEventBase: bridgev2.MatrixEventBase[*event.JoinRulesEventContent]{Portal: portal(groupID)},
		}},
		{name: "no rule change", msg: &bridgev2.MatrixJoinRuleChange{
			MatrixEventBase: bridgev2.MatrixEventBase[*event.JoinRulesEventContent]{
				Content: &event.JoinRulesEventContent{JoinRule: event.JoinRuleInvite},
				Portal:  portal(groupID),
			},
			PrevContent: &event.JoinRulesEventContent{JoinRule: event.JoinRuleInvite},
		}},
		{name: "non-group portal", msg: &bridgev2.MatrixJoinRuleChange{
			MatrixEventBase: bridgev2.MatrixEventBase[*event.JoinRulesEventContent]{
				Content: &event.JoinRulesEventContent{JoinRule: event.JoinRuleKnock},
				Portal:  portal(dmID),
			},
		}},
		{name: "invalid portal ID", msg: &bridgev2.MatrixJoinRuleChange{
			MatrixEventBase: bridgev2.MatrixEventBase[*event.JoinRulesEventContent]{
				Content: &event.JoinRulesEventContent{JoinRule: event.JoinRuleKnock},
				Portal:  portal("a.b.c@invalid"),
			},
		}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Client is nil: a call to SetGroupJoinApprovalMode would panic,
			// so only the early-return paths are exercised here.
			wa := &WhatsAppClient{MC: noopMCInstance}
			changed, err := wa.HandleMatrixJoinRule(context.Background(), tc.msg)
			if (err != nil) != tc.wantErr {
				t.Errorf("HandleMatrixJoinRule() error = %v, wantErr %v", err, tc.wantErr)
			}
			if changed {
				t.Error("HandleMatrixJoinRule() changed = true, want false")
			}
		})
	}
}

func ptrBool(value bool) *bool {
	return &value
}
