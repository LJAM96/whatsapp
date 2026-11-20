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
	"time"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestGetChatSettingsContent(t *testing.T) {
	preParsed := &ChatSettingsEventContent{Archived: ptr.Ptr(false)}
	tests := []struct {
		name    string
		raw     string
		parsed  any
		want    ChatSettingsEventContent
		wantErr bool
	}{
		{
			name: "empty content changes nothing",
			raw:  `{}`,
		},
		{
			name: "explicit false and zero are present",
			raw:  `{"archived":false,"pinned":false,"muted":false,"mutedUntil":0,"markedUnread":false}`,
			want: ChatSettingsEventContent{
				Archived:     ptr.Ptr(false),
				Pinned:       ptr.Ptr(false),
				Muted:        ptr.Ptr(false),
				MutedUntil:   ptr.Ptr(int64(0)),
				MarkedUnread: ptr.Ptr(false),
			},
		},
		{
			name: "subset of keys",
			raw:  `{"pinned":true,"mutedUntil":-1}`,
			want: ChatSettingsEventContent{
				Pinned:     ptr.Ptr(true),
				MutedUntil: ptr.Ptr(int64(-1)),
			},
		},
		{
			name: "unknown keys are ignored",
			raw:  `{"archived":true,"somethingElse":{"a":1}}`,
			want: ChatSettingsEventContent{Archived: ptr.Ptr(true)},
		},
		{
			name:   "parsed content is preferred",
			raw:    `{"archived":true}`,
			parsed: preParsed,
			want:   ChatSettingsEventContent{Archived: ptr.Ptr(false)},
		},
		{
			name:    "invalid JSON is an error",
			raw:     `{"archived":`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &event.Event{Content: event.Content{
				VeryRaw: json.RawMessage(tt.raw),
				Parsed:  tt.parsed,
			}}
			got, err := getChatSettingsContent(evt)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("getChatSettingsContent() error: %v", err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("getChatSettingsContent() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestMuteFromSettings(t *testing.T) {
	tests := []struct {
		name      string
		content   ChatSettingsEventContent
		wantMuted bool
		wantUntil *int64
	}{
		{name: "no keys", content: ChatSettingsEventContent{}},
		{
			name:      "muted true without until means forever",
			content:   ChatSettingsEventContent{Muted: ptr.Ptr(true)},
			wantMuted: true,
		},
		{
			name:      "muted false wins over until",
			content:   ChatSettingsEventContent{Muted: ptr.Ptr(false), MutedUntil: ptr.Ptr(int64(1))},
			wantUntil: nil,
		},
		{
			name:      "negative until means forever",
			content:   ChatSettingsEventContent{MutedUntil: ptr.Ptr(int64(-1))},
			wantMuted: true,
			wantUntil: ptr.Ptr(int64(-1)),
		},
		{
			name:      "positive until implies muted",
			content:   ChatSettingsEventContent{MutedUntil: ptr.Ptr(int64(1_700_000_000_000))},
			wantMuted: true,
			wantUntil: ptr.Ptr(int64(1_700_000_000_000)),
		},
		{
			name:    "zero until clears mute",
			content: ChatSettingsEventContent{MutedUntil: ptr.Ptr(int64(0))},
		},
		{
			name:      "muted true with zero until means forever",
			content:   ChatSettingsEventContent{Muted: ptr.Ptr(true), MutedUntil: ptr.Ptr(int64(0))},
			wantMuted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			muted, until := muteFromSettings(&tt.content)
			if muted != tt.wantMuted {
				t.Errorf("muted = %v, want %v", muted, tt.wantMuted)
			}
			if !reflect.DeepEqual(until, tt.wantUntil) {
				t.Errorf("mutedUntil = %v, want %v", until, tt.wantUntil)
			}
		})
	}
}

type expectedChatSettingsPatch struct {
	patchType        appstate.WAPatchName
	index            string
	mutationCount    int
	archived         *bool
	pinned           *bool
	muted            *bool
	muteEndTimestamp *int64
	read             *bool
}

func TestChatSettingsToAppState(t *testing.T) {
	chatJID := types.NewJID("15551234567", types.DefaultUserServer)
	lastTS := time.UnixMilli(1_700_000_000_000)
	lastKey := &waCommon.MessageKey{ID: ptr.Ptr("LAST-MSG")}
	tests := []struct {
		name    string
		content ChatSettingsEventContent
		want    []expectedChatSettingsPatch
	}{
		{
			name: "no present keys produces no patches",
		},
		{
			name:    "archive true also adds an unpin mutation",
			content: ChatSettingsEventContent{Archived: ptr.Ptr(true)},
			want: []expectedChatSettingsPatch{{
				patchType:     appstate.WAPatchRegularLow,
				index:         appstate.IndexArchive,
				mutationCount: 2,
				archived:      ptr.Ptr(true),
			}},
		},
		{
			name:    "archive false has a single mutation",
			content: ChatSettingsEventContent{Archived: ptr.Ptr(false)},
			want: []expectedChatSettingsPatch{{
				patchType:     appstate.WAPatchRegularLow,
				index:         appstate.IndexArchive,
				mutationCount: 1,
				archived:      ptr.Ptr(false),
			}},
		},
		{
			name:    "pin",
			content: ChatSettingsEventContent{Pinned: ptr.Ptr(true)},
			want: []expectedChatSettingsPatch{{
				patchType:     appstate.WAPatchRegularLow,
				index:         appstate.IndexPin,
				mutationCount: 1,
				pinned:        ptr.Ptr(true),
			}},
		},
		{
			name:    "mute forever without until",
			content: ChatSettingsEventContent{Muted: ptr.Ptr(true)},
			want: []expectedChatSettingsPatch{{
				patchType:        appstate.WAPatchRegularHigh,
				index:            appstate.IndexMute,
				mutationCount:    1,
				muted:            ptr.Ptr(true),
				muteEndTimestamp: ptr.Ptr(int64(-1)),
			}},
		},
		{
			name:    "mute with absolute timestamp",
			content: ChatSettingsEventContent{MutedUntil: ptr.Ptr(int64(1_700_000_000_000))},
			want: []expectedChatSettingsPatch{{
				patchType:        appstate.WAPatchRegularHigh,
				index:            appstate.IndexMute,
				mutationCount:    1,
				muted:            ptr.Ptr(true),
				muteEndTimestamp: ptr.Ptr(int64(1_700_000_000_000)),
			}},
		},
		{
			name:    "unmute ignores until",
			content: ChatSettingsEventContent{Muted: ptr.Ptr(false), MutedUntil: ptr.Ptr(int64(1))},
			want: []expectedChatSettingsPatch{{
				patchType:     appstate.WAPatchRegularHigh,
				index:         appstate.IndexMute,
				mutationCount: 1,
				muted:         ptr.Ptr(false),
			}},
		},
		{
			name:    "marked unread inverts to read=false",
			content: ChatSettingsEventContent{MarkedUnread: ptr.Ptr(true)},
			want: []expectedChatSettingsPatch{{
				patchType:     appstate.WAPatchRegularLow,
				index:         appstate.IndexMarkChatAsRead,
				mutationCount: 1,
				read:          ptr.Ptr(false),
			}},
		},
		{
			name:    "not marked unread inverts to read=true",
			content: ChatSettingsEventContent{MarkedUnread: ptr.Ptr(false)},
			want: []expectedChatSettingsPatch{{
				patchType:     appstate.WAPatchRegularLow,
				index:         appstate.IndexMarkChatAsRead,
				mutationCount: 1,
				read:          ptr.Ptr(true),
			}},
		},
		{
			name: "all keys are applied in archive, pin, mute, read order",
			content: ChatSettingsEventContent{
				Archived:     ptr.Ptr(true),
				Pinned:       ptr.Ptr(true),
				Muted:        ptr.Ptr(true),
				MutedUntil:   ptr.Ptr(int64(-1)),
				MarkedUnread: ptr.Ptr(true),
			},
			want: []expectedChatSettingsPatch{
				{patchType: appstate.WAPatchRegularLow, index: appstate.IndexArchive, mutationCount: 2, archived: ptr.Ptr(true)},
				{patchType: appstate.WAPatchRegularLow, index: appstate.IndexPin, mutationCount: 1, pinned: ptr.Ptr(true)},
				{patchType: appstate.WAPatchRegularHigh, index: appstate.IndexMute, mutationCount: 1, muted: ptr.Ptr(true), muteEndTimestamp: ptr.Ptr(int64(-1))},
				{patchType: appstate.WAPatchRegularLow, index: appstate.IndexMarkChatAsRead, mutationCount: 1, read: ptr.Ptr(false)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := chatSettingsToAppState(chatJID, &tt.content, lastTS, lastKey)
			if len(patches) != len(tt.want) {
				t.Fatalf("got %d patches, want %d", len(patches), len(tt.want))
			}
			for i, patch := range patches {
				want := tt.want[i]
				if patch.Type != want.patchType {
					t.Errorf("patch %d type = %q, want %q", i, patch.Type, want.patchType)
				}
				if len(patch.Mutations) != want.mutationCount {
					t.Fatalf("patch %d has %d mutations, want %d", i, len(patch.Mutations), want.mutationCount)
				}
				mutation := patch.Mutations[0]
				if len(mutation.Index) != 2 || mutation.Index[0] != want.index || mutation.Index[1] != chatJID.String() {
					t.Errorf("patch %d index = %v, want [%s %s]", i, mutation.Index, want.index, chatJID)
				}
				value := mutation.Value
				if want.archived != nil {
					if got := value.GetArchiveChatAction().GetArchived(); got != *want.archived {
						t.Errorf("patch %d archived = %v, want %v", i, got, *want.archived)
					}
					checkMessageRange(t, i, value.GetArchiveChatAction().GetMessageRange(), lastTS, lastKey)
				}
				if want.pinned != nil {
					if got := value.GetPinAction().GetPinned(); got != *want.pinned {
						t.Errorf("patch %d pinned = %v, want %v", i, got, *want.pinned)
					}
				}
				if want.muted != nil {
					action := value.GetMuteAction()
					if got := action.GetMuted(); got != *want.muted {
						t.Errorf("patch %d muted = %v, want %v", i, got, *want.muted)
					}
					if want.muteEndTimestamp == nil {
						if action.MuteEndTimestamp != nil {
							t.Errorf("patch %d mute end timestamp = %v, want nil", i, action.GetMuteEndTimestamp())
						}
					} else if got := action.GetMuteEndTimestamp(); got != *want.muteEndTimestamp {
						t.Errorf("patch %d mute end timestamp = %d, want %d", i, got, *want.muteEndTimestamp)
					}
				}
				if want.read != nil {
					if got := value.GetMarkChatAsReadAction().GetRead(); got != *want.read {
						t.Errorf("patch %d read = %v, want %v", i, got, *want.read)
					}
					checkMessageRange(t, i, value.GetMarkChatAsReadAction().GetMessageRange(), lastTS, lastKey)
				}
			}
		})
	}
}

func checkMessageRange(t *testing.T, patchIdx int, got *waSyncAction.SyncActionMessageRange, lastTS time.Time, lastKey *waCommon.MessageKey) {
	t.Helper()
	if got == nil {
		t.Errorf("patch %d has no message range", patchIdx)
		return
	}
	if got.GetLastMessageTimestamp() != lastTS.Unix() {
		t.Errorf("patch %d last message timestamp = %d, want %d", patchIdx, got.GetLastMessageTimestamp(), lastTS.Unix())
	}
	if len(got.GetMessages()) != 1 || got.GetMessages()[0].GetKey().GetID() != lastKey.GetID() {
		t.Errorf("patch %d last message key = %+v, want %q", patchIdx, got.GetMessages(), lastKey.GetID())
	}
}

func TestHandleChatSettingsIgnoresOtherSenders(t *testing.T) {
	ownMXID := id.UserID("@own:example.org")
	portalID := waid.MakePortalID(types.NewJID("15551234567", types.DefaultUserServer))
	wa := &WhatsAppClient{UserLogin: &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{UserMXID: ownMXID},
	}}
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{ID: portalID}}}
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
				Content: event.Content{VeryRaw: json.RawMessage(`{"archived":true}`)},
			}
			// Client is nil: any attempt to send an app-state patch would panic.
			if err := wa.HandleChatSettings(context.Background(), portal, evt); err != nil {
				t.Errorf("HandleChatSettings() error = %v, want nil", err)
			}
		})
	}
}

func TestGetCustomMatrixEventTypes(t *testing.T) {
	got := (&WhatsAppConnector{}).GetCustomMatrixEventTypes()
	want := []event.Type{ChatSettingsEventType}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetCustomMatrixEventTypes() = %v, want %v", got, want)
	}
}

func TestHandleMatrixCustomEvent(t *testing.T) {
	ownMXID := id.UserID("@own:example.org")
	wa := &WhatsAppClient{UserLogin: &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{UserMXID: ownMXID},
	}}
	// An invalid portal ID makes HandleChatSettings fail after its sender
	// check, so a non-nil error proves the event was routed to that handler.
	portal := &bridgev2.Portal{Portal: &database.Portal{
		PortalKey: networkid.PortalKey{ID: "a.b.c@invalid"},
	}}
	tests := []struct {
		name      string
		eventType event.Type
		sender    id.UserID
		wantRoute bool
	}{
		{"chat settings from own user", ChatSettingsEventType, ownMXID, true},
		{"chat settings from other user", ChatSettingsEventType, id.UserID("@other:example.org"), false},
		{"unrelated custom type", event.Type{Type: "com.example.other", Class: event.MessageEventType}, ownMXID, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &event.Event{
				Type:    tt.eventType,
				Sender:  tt.sender,
				Content: event.Content{VeryRaw: json.RawMessage(`{"archived":true}`)},
			}
			err := wa.HandleMatrixCustomEvent(context.Background(), &bridgev2.MatrixCustomEvent{
				MatrixEventBase: bridgev2.MatrixEventBase[*event.Content]{
					Event:   evt,
					Content: &evt.Content,
					Portal:  portal,
				},
			})
			if tt.wantRoute && err == nil {
				t.Error("HandleMatrixCustomEvent() error = nil, want routing error")
			} else if !tt.wantRoute && err != nil {
				t.Errorf("HandleMatrixCustomEvent() error = %v, want nil", err)
			}
		})
	}
}
