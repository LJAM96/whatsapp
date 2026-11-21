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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestArgbToHex(t *testing.T) {
	tests := []struct {
		name string
		argb uint32
		want string
	}{
		{"opaque red", 0xFFFF0000, "#FF0000"},
		{"alpha is dropped", 0x8012AB34, "#12AB34"},
		{"black", 0xFF000000, "#000000"},
		{"white", 0xFFFFFFFF, "#FFFFFF"},
		{"lowercase bits", 0xFF00aabb, "#00AABB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := argbToHex(tt.argb); got != tt.want {
				t.Errorf("argbToHex(%#x) = %q, want %q", tt.argb, got, tt.want)
			}
		})
	}
}

func TestIsStatusMessage(t *testing.T) {
	tests := []struct {
		name string
		info *types.MessageInfo
		want bool
	}{
		{"nil info", nil, false},
		{
			"incoming status",
			&types.MessageInfo{MessageSource: types.MessageSource{Chat: types.StatusBroadcastJID}},
			true,
		},
		{
			"own status is not a contact post",
			&types.MessageInfo{MessageSource: types.MessageSource{Chat: types.StatusBroadcastJID, IsFromMe: true}},
			false,
		},
		{
			"broadcast list is not a status",
			&types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("list", types.BroadcastServer)}},
			false,
		},
		{
			"normal DM",
			&types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("123", types.DefaultUserServer)}},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStatusMessage(tt.info); got != tt.want {
				t.Errorf("IsStatusMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatusMetadataJSON(t *testing.T) {
	meta := StatusMetadata{
		Sender:    id.UserID("@wa_123:ghost.example.org"),
		ExpiresAt: 1_700_086_400_000,
	}
	actual, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("failed to marshal status metadata: %v", err)
	}
	want := `{"sender":"@wa_123:ghost.example.org","expiresAt":1700086400000}`
	if string(actual) != want {
		t.Errorf("status metadata JSON = %s, want %s", actual, want)
	}
}

func TestApplyStatusBackground(t *testing.T) {
	tests := []struct {
		name       string
		argb       uint32
		body       string
		formatted  string
		wantBody   string
		wantFormat bool
		wantBg     string
	}{
		{
			name:     "no colour is a no-op",
			argb:     0,
			body:     "hello",
			wantBody: "hello",
		},
		{
			name:       "text without formatting gets HTML generated",
			argb:       0xFF12AB34,
			body:       "hello",
			wantBody:   "hello",
			wantFormat: true,
			wantBg:     "#12AB34",
		},
		{
			name:       "existing formatting is wrapped, not replaced",
			argb:       0xFFFF0000,
			body:       "*bold*",
			formatted:  "<strong>bold</strong>",
			wantBody:   "*bold*",
			wantFormat: true,
			wantBg:     "#FF0000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := &event.MessageEventContent{Body: tt.body, FormattedBody: tt.formatted}
			applyStatusBackground(content, tt.argb)
			if content.Body != tt.wantBody {
				t.Errorf("Body = %q, want %q", content.Body, tt.wantBody)
			}
			if (content.Format == event.FormatHTML) != tt.wantFormat {
				t.Errorf("Format = %q, want HTML=%v", content.Format, tt.wantFormat)
			}
			if tt.wantBg == "" {
				if content.FormattedBody != tt.formatted {
					t.Errorf("FormattedBody = %q, want unchanged %q", content.FormattedBody, tt.formatted)
				}
			} else {
				if !strings.Contains(content.FormattedBody, `data-mx-bg-color="`+tt.wantBg+`"`) {
					t.Errorf("FormattedBody = %q, want bg colour %q", content.FormattedBody, tt.wantBg)
				}
				if tt.formatted != "" && !strings.Contains(content.FormattedBody, tt.formatted) {
					t.Errorf("FormattedBody = %q, want it to keep %q", content.FormattedBody, tt.formatted)
				}
			}
		})
	}
}

func TestApplyStatus(t *testing.T) {
	const ghostID = id.UserID("@wa_123:ghost.example.org")
	ts := time.UnixMilli(1_700_000_000_000)
	textMsg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:           ptr.Ptr("status text"),
			BackgroundArgb: ptr.Ptr(uint32(0xFF00AACC)),
		},
	}
	mediaMsg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: ptr.Ptr("photo caption")}}
	statusInfo := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: types.StatusBroadcastJID},
		Timestamp:     ts,
	}
	dmInfo := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: types.NewJID("123", types.DefaultUserServer)},
		Timestamp:     ts,
	}

	tests := []struct {
		name        string
		enabled     bool
		info        *types.MessageInfo
		msg         *waE2E.Message
		body        string
		wantMeta    bool
		wantBg      string
		wantDisappe bool
	}{
		{"text status", true, statusInfo, textMsg, "status text", true, "#00AACC", true},
		{"media status keeps caption", true, statusInfo, mediaMsg, "photo caption", true, "", true},
		{"status bridging disabled", false, statusInfo, textMsg, "status text", false, "", false},
		{"non-status chat", true, dmInfo, textMsg, "status text", false, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &mockMatrixAPI{mxid: ghostID}
			mc := &MessageConverter{BridgeStatus: tt.enabled}
			part := &bridgev2.ConvertedMessagePart{
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{Body: tt.body, MsgType: event.MsgText},
			}
			cm := &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{part}}

			mc.applyStatus(cm, tt.msg, tt.info, api)

			metaRaw, hasMeta := part.Extra[StatusMetadataKey]
			if hasMeta != tt.wantMeta {
				t.Fatalf("has %s = %v, want %v (Extra=%v)", StatusMetadataKey, hasMeta, tt.wantMeta, part.Extra)
			}
			if tt.wantMeta {
				meta, ok := metaRaw.(StatusMetadata)
				if !ok {
					t.Fatalf("metadata type = %T, want StatusMetadata", metaRaw)
				}
				if meta.Sender != ghostID {
					t.Errorf("sender = %q, want %q", meta.Sender, ghostID)
				}
				if want := ts.Add(StatusLifetime).UnixMilli(); meta.ExpiresAt != want {
					t.Errorf("expiresAt = %d, want %d", meta.ExpiresAt, want)
				}
			}
			if tt.wantBg == "" {
				if part.Content.FormattedBody != "" {
					t.Errorf("unexpected formatted body %q", part.Content.FormattedBody)
				}
			} else if !strings.Contains(part.Content.FormattedBody, `data-mx-bg-color="`+tt.wantBg+`"`) {
				t.Errorf("formatted body = %q, want bg %q", part.Content.FormattedBody, tt.wantBg)
			}
			if part.Content.Body != tt.body {
				t.Errorf("plain body = %q, want %q", part.Content.Body, tt.body)
			}
			if (cm.Disappear.Timer == StatusLifetime) != tt.wantDisappe {
				t.Errorf("disappear timer = %v, want set=%v", cm.Disappear.Timer, tt.wantDisappe)
			}
		})
	}
}
