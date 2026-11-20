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
	"encoding/json"
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestGetLabelContent(t *testing.T) {
	preParsed := &LabelEventContent{Label: "parsed", Applied: true}
	tests := []struct {
		name    string
		raw     string
		parsed  any
		want    LabelEventContent
		wantErr bool
	}{
		{
			name: "apply",
			raw:  `{"label":"Clients","applied":true}`,
			want: LabelEventContent{Label: "Clients", Applied: true},
		},
		{
			name: "remove",
			raw:  `{"label":"Clients","applied":false}`,
			want: LabelEventContent{Label: "Clients"},
		},
		{
			name:   "parsed content is preferred",
			raw:    `{"label":"raw","applied":false}`,
			parsed: preParsed,
			want:   *preParsed,
		},
		{
			name:    "invalid JSON is an error",
			raw:     `{"label":`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &event.Event{Content: event.Content{
				VeryRaw: json.RawMessage(tt.raw),
				Parsed:  tt.parsed,
			}}
			got, err := getLabelContent(evt)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("getLabelContent() error: %v", err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("getLabelContent() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestLabelToAppState(t *testing.T) {
	chatJID := types.NewJID("15551234567", types.DefaultUserServer)
	newID := func() string { return "NEW-ID" }
	tests := []struct {
		name        string
		labels      map[string]waid.WALabel
		applied     bool
		wantPatches int
		wantCreated bool
		wantLabels  map[string]waid.WALabel
	}{
		{
			name:        "existing label applied",
			labels:      map[string]waid.WALabel{"EXISTING": {Name: "Clients", Color: 3}},
			applied:     true,
			wantPatches: 1,
			wantLabels:  map[string]waid.WALabel{"EXISTING": {Name: "Clients", Color: 3}},
		},
		{
			name:        "existing label removed",
			labels:      map[string]waid.WALabel{"EXISTING": {Name: "Clients", Color: 3}},
			applied:     false,
			wantPatches: 1,
			wantLabels:  map[string]waid.WALabel{"EXISTING": {Name: "Clients", Color: 3}},
		},
		{
			name:        "missing label is created when applying",
			labels:      map[string]waid.WALabel{},
			applied:     true,
			wantPatches: 2,
			wantCreated: true,
			wantLabels:  map[string]waid.WALabel{"NEW-ID": {Name: "Clients", Color: defaultLabelColor}},
		},
		{
			name:        "missing label is not created when removing",
			labels:      map[string]waid.WALabel{},
			applied:     false,
			wantPatches: 0,
			wantLabels:  map[string]waid.WALabel{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches, created := labelToAppState(tt.labels, chatJID, "Clients", tt.applied, newID)
			if created != tt.wantCreated {
				t.Errorf("created = %v, want %v", created, tt.wantCreated)
			}
			if len(patches) != tt.wantPatches {
				t.Fatalf("got %d patches, want %d", len(patches), tt.wantPatches)
			}
			if !reflect.DeepEqual(tt.labels, tt.wantLabels) {
				t.Errorf("labels = %+v, want %+v", tt.labels, tt.wantLabels)
			}
			if !tt.wantCreated {
				if len(patches) == 1 {
					checkLabelChatPatch(t, patches[0], chatJID, "EXISTING", tt.applied)
				}
				return
			}
			checkLabelEditPatch(t, patches[0], "NEW-ID", "Clients")
			checkLabelChatPatch(t, patches[1], chatJID, "NEW-ID", tt.applied)
		})
	}
}

func checkLabelEditPatch(t *testing.T, patch appstate.PatchInfo, labelID, name string) {
	t.Helper()
	if patch.Type != appstate.WAPatchRegular {
		t.Errorf("edit patch type = %q, want %q", patch.Type, appstate.WAPatchRegular)
	}
	if len(patch.Mutations) != 1 {
		t.Fatalf("edit patch has %d mutations, want 1", len(patch.Mutations))
	}
	mutation := patch.Mutations[0]
	if want := []string{appstate.IndexLabelEdit, labelID}; !reflect.DeepEqual(mutation.Index, want) {
		t.Errorf("edit index = %v, want %v", mutation.Index, want)
	}
	action := mutation.Value.GetLabelEditAction()
	if got := action.GetName(); got != name {
		t.Errorf("label name = %q, want %q", got, name)
	}
	if got := action.GetColor(); got != defaultLabelColor {
		t.Errorf("label color = %d, want %d", got, defaultLabelColor)
	}
	if got := action.GetDeleted(); got {
		t.Error("new label must not be deleted")
	}
}

func checkLabelChatPatch(t *testing.T, patch appstate.PatchInfo, chatJID types.JID, labelID string, applied bool) {
	t.Helper()
	if patch.Type != appstate.WAPatchRegular {
		t.Errorf("chat patch type = %q, want %q", patch.Type, appstate.WAPatchRegular)
	}
	if len(patch.Mutations) != 1 {
		t.Fatalf("chat patch has %d mutations, want 1", len(patch.Mutations))
	}
	mutation := patch.Mutations[0]
	wantIndex := []string{appstate.IndexLabelAssociationChat, labelID, chatJID.String()}
	if !reflect.DeepEqual(mutation.Index, wantIndex) {
		t.Errorf("chat index = %v, want %v", mutation.Index, wantIndex)
	}
	if got := mutation.Value.GetLabelAssociationAction().GetLabeled(); got != applied {
		t.Errorf("labeled = %v, want %v", got, applied)
	}
}

func TestHandleLabelIgnoresOtherSenders(t *testing.T) {
	ownMXID := id.UserID("@own:example.org")
	wa := &WhatsAppClient{UserLogin: &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{UserMXID: ownMXID},
	}}
	tests := []struct {
		name   string
		sender id.UserID
	}{
		{"other user", id.UserID("@other:example.org")},
		{"empty sender", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &event.Event{
				Sender:  tt.sender,
				Content: event.Content{VeryRaw: json.RawMessage(`{"label":"Clients","applied":true}`)},
			}
			// Main, Device and Client are nil: reaching any further step would panic.
			if err := wa.HandleLabel(context.Background(), nil, evt); err != nil {
				t.Errorf("HandleLabel() error = %v, want nil", err)
			}
		})
	}
}

func TestHandleLabelIgnoresNonBusinessAccount(t *testing.T) {
	ownMXID := id.UserID("@own:example.org")
	wa := &WhatsAppClient{
		Device: &store.Device{},
		UserLogin: &bridgev2.UserLogin{
			UserLogin: &database.UserLogin{UserMXID: ownMXID},
		},
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{
		PortalKey: networkid.PortalKey{ID: waid.MakePortalID(types.NewJID("15551234567", types.DefaultUserServer))},
	}}
	tests := []struct {
		name   string
		raw    string
		device *store.Device
	}{
		{"no business name", `{"label":"Clients","applied":true}`, &store.Device{}},
		{"nil device", `{"label":"Clients","applied":true}`, nil},
		{"empty label name", `{"label":"","applied":true}`, &store.Device{BusinessName: "Biz"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wa.Device = tt.device
			evt := &event.Event{
				Sender:  ownMXID,
				Content: event.Content{VeryRaw: json.RawMessage(tt.raw)},
			}
			// Client is nil: reaching the send would panic.
			if err := wa.HandleLabel(context.Background(), portal, evt); err != nil {
				t.Errorf("HandleLabel() error = %v, want nil", err)
			}
		})
	}
}

func TestIsBusinessAccount(t *testing.T) {
	tests := []struct {
		name string
		wa   *WhatsAppClient
		want bool
	}{
		{name: "device with business name", wa: &WhatsAppClient{Device: &store.Device{BusinessName: "Biz"}}, want: true},
		{name: "device without business name", wa: &WhatsAppClient{Device: &store.Device{}}},
		{name: "nil device", wa: &WhatsAppClient{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.wa.isBusinessAccount(); got != tt.want {
				t.Errorf("isBusinessAccount() = %v, want %v", got, tt.want)
			}
		})
	}
}
