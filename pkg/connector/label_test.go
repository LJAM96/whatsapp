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
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

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
		name          string
		labels        map[string]waid.WALabel
		applied       bool
		wantPatches   int
		wantCreatedID string
		wantLabels    map[string]waid.WALabel
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
			name:          "missing label is created when applying without mutating shared state",
			labels:        map[string]waid.WALabel{},
			applied:       true,
			wantPatches:   2,
			wantCreatedID: "NEW-ID",
			wantLabels:    map[string]waid.WALabel{},
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
			if tt.wantCreatedID == "" {
				if created != nil {
					t.Errorf("created = %+v, want nil", created)
				}
			} else if created == nil || created.ID != tt.wantCreatedID {
				t.Errorf("created = %+v, want ID %q", created, tt.wantCreatedID)
			} else if created.Label.Name != "Clients" || created.Label.Color != defaultLabelColor {
				t.Errorf("created label = %+v, want placeholder metadata", created.Label)
			}
			if len(patches) != tt.wantPatches {
				t.Fatalf("got %d patches, want %d", len(patches), tt.wantPatches)
			}
			if !reflect.DeepEqual(tt.labels, tt.wantLabels) {
				t.Errorf("labels = %+v, want %+v (patch construction must not mutate the map)", tt.labels, tt.wantLabels)
			}
			if tt.wantCreatedID == "" {
				if len(patches) == 1 {
					checkLabelChatPatch(t, patches[0], chatJID, "EXISTING", tt.applied)
				}
				return
			}
			checkLabelEditPatch(t, patches[0], tt.wantCreatedID, "Clients")
			checkLabelChatPatch(t, patches[1], chatJID, tt.wantCreatedID, tt.applied)
		})
	}
}

func isLabelCreationPatch(patch appstate.PatchInfo) bool {
	return len(patch.Mutations) == 1 && len(patch.Mutations[0].Index) > 0 && patch.Mutations[0].Index[0] == appstate.IndexLabelEdit
}

func labelPatchID(patch appstate.PatchInfo) string {
	if len(patch.Mutations) == 0 || len(patch.Mutations[0].Index) < 2 {
		return ""
	}
	return patch.Mutations[0].Index[1]
}

func TestApplyLabelEvent(t *testing.T) {
	creationErr := errors.New("creation failed")
	associationErr := errors.New("association failed")
	chatJID := types.NewJID("15551234567", types.DefaultUserServer)
	content := &LabelEventContent{Label: "Clients", Applied: true}
	createdLabelMap := map[string]waid.WALabel{"NEW-ID": {Name: "Clients", Color: defaultLabelColor}}
	tests := []struct {
		name          string
		initialLabels map[string]waid.WALabel
		sendErrAt     map[int]error
		echoOnCreate  bool
		wantErr       bool
		wantLabels    map[string]waid.WALabel
		wantEvents    []string
	}{
		{
			name:          "creation is committed and saved before association",
			initialLabels: map[string]waid.WALabel{},
			wantLabels:    createdLabelMap,
			wantEvents:    []string{"create:NEW-ID", "save", "associate:NEW-ID"},
		},
		{
			name:          "failed creation leaves no metadata and no save",
			initialLabels: map[string]waid.WALabel{},
			sendErrAt:     map[int]error{0: creationErr},
			wantErr:       true,
			wantLabels:    map[string]waid.WALabel{},
			wantEvents:    []string{"create:NEW-ID"},
		},
		{
			name:          "failed association keeps the created label",
			initialLabels: map[string]waid.WALabel{},
			sendErrAt:     map[int]error{1: associationErr},
			wantErr:       true,
			wantLabels:    createdLabelMap,
			wantEvents:    []string{"create:NEW-ID", "save", "associate:NEW-ID"},
		},
		{
			name:          "concurrent confirmed echo is preserved",
			initialLabels: map[string]waid.WALabel{},
			echoOnCreate:  true,
			wantLabels:    map[string]waid.WALabel{"NEW-ID": {Name: "Clients", Color: 7}},
			wantEvents:    []string{"create:NEW-ID", "save", "associate:NEW-ID"},
		},
		{
			name:          "existing label sends only an association",
			initialLabels: map[string]waid.WALabel{"EXISTING": {Name: "Clients", Color: 3}},
			wantLabels:    map[string]waid.WALabel{"EXISTING": {Name: "Clients", Color: 3}},
			wantEvents:    []string{"associate:EXISTING"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := &waid.UserLoginMetadata{Labels: map[string]waid.WALabel{}}
			for id, label := range tt.initialLabels {
				meta.Labels[id] = label
			}
			wa := &WhatsAppClient{}
			var events []string
			sent := 0
			send := func(_ context.Context, patch appstate.PatchInfo) error {
				kind := "associate"
				if isLabelCreationPatch(patch) {
					kind = "create"
					if tt.echoOnCreate {
						// Simulate the incoming app-state callback landing while
						// the creation patch is in flight. It must not hold the
						// label lock across this send.
						wa.labelLock.Lock()
						updateLabel(meta.Labels, labelPatchID(patch), "Clients", 7, false)
						wa.labelLock.Unlock()
					}
				}
				events = append(events, kind+":"+labelPatchID(patch))
				err := tt.sendErrAt[sent]
				sent++
				return err
			}
			save := func(_ context.Context) error {
				events = append(events, "save")
				return nil
			}
			err := wa.applyLabelEvent(context.Background(), meta, chatJID, content, func() string { return "NEW-ID" }, send, save)
			if (err != nil) != tt.wantErr {
				t.Fatalf("applyLabelEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(events, tt.wantEvents) {
				t.Errorf("events = %v, want %v", events, tt.wantEvents)
			}
			if !reflect.DeepEqual(meta.Labels, tt.wantLabels) {
				t.Errorf("labels = %+v, want %+v", meta.Labels, tt.wantLabels)
			}
		})
	}
}

func TestApplyLabelEventSerializesConcurrentPortals(t *testing.T) {
	const labelName = "Clients"
	meta := &waid.UserLoginMetadata{}
	wa := &WhatsAppClient{}
	creationErr := errors.New("creation failed")
	firstSendStarted := make(chan struct{})
	releaseFirstSend := make(chan struct{})
	secondSendStarted := make(chan struct{})
	var mu sync.Mutex
	var patches []appstate.PatchInfo
	send := func(_ context.Context, patch appstate.PatchInfo) error {
		mu.Lock()
		patches = append(patches, patch)
		call := len(patches)
		mu.Unlock()
		switch call {
		case 1:
			close(firstSendStarted)
			<-releaseFirstSend
			return creationErr
		case 2:
			close(secondSendStarted)
		}
		return nil
	}
	chat1 := types.NewJID("15551111111", types.DefaultUserServer)
	chat2 := types.NewJID("15552222222", types.DefaultUserServer)
	content := &LabelEventContent{Label: labelName, Applied: true}
	newID := func() string { return "FIRST-ID" }

	errs := make(chan error, 2)
	go func() {
		errs <- wa.applyLabelEvent(context.Background(), meta, chat1, content, newID, send, func(context.Context) error { return nil })
	}()
	<-firstSendStarted

	// The provisional label must not be visible to an incoming event while
	// the creation patch is still in flight.
	wa.labelLock.Lock()
	visible := len(meta.Labels)
	wa.labelLock.Unlock()
	if visible != 0 {
		t.Errorf("incoming event can see %d provisional labels before WhatsApp accepts them", visible)
	}

	go func() {
		// A second portal applying the same label must wait for the first
		// outbound operation instead of sending an association for an
		// unaccepted (or failed) label.
		errs <- wa.applyLabelEvent(context.Background(), meta, chat2, content, func() string { return "SECOND-ID" }, send, func(context.Context) error { return nil })
	}()
	select {
	case <-secondSendStarted:
		t.Fatal("second portal sent a patch while the first creation was in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirstSend)
	if err := <-errs; err == nil {
		t.Error("first label event should report the creation failure")
	}
	if err := <-errs; err != nil {
		t.Errorf("second label event error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var creations int
	for _, patch := range patches {
		if isLabelCreationPatch(patch) {
			creations++
		}
	}
	if creations != 2 {
		t.Errorf("sent %d creation patches, want 2 (one per event)", creations)
	}
	want := map[string]waid.WALabel{"SECOND-ID": {Name: labelName, Color: defaultLabelColor}}
	if !reflect.DeepEqual(meta.Labels, want) {
		t.Errorf("labels = %+v, want %+v", meta.Labels, want)
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
