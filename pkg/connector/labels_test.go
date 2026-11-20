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

	"go.mau.fi/whatsmeow/types"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestLabelRoomTag(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "Invoice", want: "u.wa.Invoice"},
		{name: "Clients A", want: "u.wa.Clients A"},
		{name: "u.wa.already", want: "u.wa.u.wa.already"},
		{name: "", want: "u.wa."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := labelRoomTag(tt.name); string(got) != tt.want {
				t.Errorf("labelRoomTag(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestUpdateLabel(t *testing.T) {
	chat1 := types.JID{User: "1234", Server: types.DefaultUserServer}
	chat2 := types.JID{User: "5678", Server: types.DefaultUserServer}
	tests := []struct {
		name    string
		initial map[string]waid.WALabel
		id      string
		label   string
		color   int32
		deleted bool
		want    map[string]waid.WALabel
		changes []labelTagChange
	}{
		{
			name:    "create label with no chats",
			initial: map[string]waid.WALabel{},
			id:      "1",
			label:   "Clients",
			color:   3,
			want:    map[string]waid.WALabel{"1": {Name: "Clients", Color: 3}},
		},
		{
			name: "label edit arrives after association",
			initial: map[string]waid.WALabel{
				"1": {Chats: []types.JID{chat1, chat2}},
			},
			id:    "1",
			label: "Clients",
			color: 3,
			want:  map[string]waid.WALabel{"1": {Name: "Clients", Color: 3, Chats: []types.JID{chat1, chat2}}},
			changes: []labelTagChange{
				{Chat: chat1, Tag: "u.wa.Clients", Add: true},
				{Chat: chat2, Tag: "u.wa.Clients", Add: true},
			},
		},
		{
			name: "rename label",
			initial: map[string]waid.WALabel{
				"1": {Name: "Clients", Color: 3, Chats: []types.JID{chat1}},
			},
			id:    "1",
			label: "Customers",
			color: 3,
			want:  map[string]waid.WALabel{"1": {Name: "Customers", Color: 3, Chats: []types.JID{chat1}}},
			changes: []labelTagChange{
				{Chat: chat1, Tag: "u.wa.Clients"},
				{Chat: chat1, Tag: "u.wa.Customers", Add: true},
			},
		},
		{
			name: "colour-only edit",
			initial: map[string]waid.WALabel{
				"1": {Name: "Clients", Color: 3, Chats: []types.JID{chat1}},
			},
			id:    "1",
			label: "Clients",
			color: 7,
			want:  map[string]waid.WALabel{"1": {Name: "Clients", Color: 7, Chats: []types.JID{chat1}}},
		},
		{
			name: "delete label",
			initial: map[string]waid.WALabel{
				"1": {Name: "Clients", Color: 3, Chats: []types.JID{chat1, chat2}},
			},
			id:      "1",
			label:   "Clients",
			deleted: true,
			want:    map[string]waid.WALabel{},
			changes: []labelTagChange{
				{Chat: chat1, Tag: "u.wa.Clients"},
				{Chat: chat2, Tag: "u.wa.Clients"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := tt.initial
			changes := updateLabel(labels, tt.id, tt.label, tt.color, tt.deleted)
			if !reflect.DeepEqual(changes, tt.changes) {
				t.Errorf("updateLabel() changes = %#v, want %#v", changes, tt.changes)
			}
			if !reflect.DeepEqual(labels, tt.want) {
				t.Errorf("updateLabel() state = %#v, want %#v", labels, tt.want)
			}
		})
	}
}

func TestUpdateLabelChat(t *testing.T) {
	chat1 := types.JID{User: "1234", Server: types.DefaultUserServer}
	chat2 := types.JID{User: "5678", Server: types.DefaultUserServer}
	tests := []struct {
		name       string
		initial    map[string]waid.WALabel
		id         string
		chat       types.JID
		labeled    bool
		want       map[string]waid.WALabel
		wantTag    string
		wantResult bool
	}{
		{
			name:       "add label to chat",
			initial:    map[string]waid.WALabel{"1": {Name: "Clients"}},
			id:         "1",
			chat:       chat1,
			labeled:    true,
			want:       map[string]waid.WALabel{"1": {Name: "Clients", Chats: []types.JID{chat1}}},
			wantTag:    "u.wa.Clients",
			wantResult: true,
		},
		{
			name:       "duplicate association is ignored",
			initial:    map[string]waid.WALabel{"1": {Name: "Clients", Chats: []types.JID{chat1}}},
			id:         "1",
			chat:       chat1,
			labeled:    true,
			want:       map[string]waid.WALabel{"1": {Name: "Clients", Chats: []types.JID{chat1}}},
			wantTag:    "u.wa.Clients",
			wantResult: true,
		},
		{
			name:       "remove label from chat",
			initial:    map[string]waid.WALabel{"1": {Name: "Clients", Chats: []types.JID{chat1, chat2}}},
			id:         "1",
			chat:       chat1,
			labeled:    false,
			want:       map[string]waid.WALabel{"1": {Name: "Clients", Chats: []types.JID{chat2}}},
			wantTag:    "u.wa.Clients",
			wantResult: true,
		},
		{
			name:       "unknown label still tracked",
			initial:    map[string]waid.WALabel{},
			id:         "1",
			chat:       chat1,
			labeled:    true,
			want:       map[string]waid.WALabel{"1": {Chats: []types.JID{chat1}}},
			wantResult: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := tt.initial
			tag, ok := updateLabelChat(labels, tt.id, tt.chat, tt.labeled)
			if ok != tt.wantResult {
				t.Errorf("updateLabelChat() ok = %v, want %v", ok, tt.wantResult)
			}
			if string(tag) != tt.wantTag {
				t.Errorf("updateLabelChat() tag = %q, want %q", tag, tt.wantTag)
			}
			if !reflect.DeepEqual(labels, tt.want) {
				t.Errorf("updateLabelChat() state = %#v, want %#v", labels, tt.want)
			}
		})
	}
}
