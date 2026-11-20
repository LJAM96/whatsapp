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
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func TestMarkViewOnce(t *testing.T) {
	tests := []struct {
		name            string
		isViewOnce      bool
		disableViewOnce bool
		part            *bridgev2.ConvertedMessagePart
		wantMarker      bool
	}{
		{
			name:       "view-once media with URL",
			isViewOnce: true,
			part: &bridgev2.ConvertedMessagePart{Content: &event.MessageEventContent{
				URL: "mxc://example.org/media",
			}},
			wantMarker: true,
		},
		{
			name:       "view-once media with encrypted file",
			isViewOnce: true,
			part: &bridgev2.ConvertedMessagePart{Content: &event.MessageEventContent{
				File: &event.EncryptedFileInfo{},
			}},
			wantMarker: true,
		},
		{
			name:            "view-once with disabling config",
			isViewOnce:      true,
			disableViewOnce: true,
			part: &bridgev2.ConvertedMessagePart{Content: &event.MessageEventContent{
				URL: "mxc://example.org/media",
			}},
		},
		{
			name: "regular media",
			part: &bridgev2.ConvertedMessagePart{Content: &event.MessageEventContent{
				URL: "mxc://example.org/media",
			}},
		},
		{
			name:       "failure notice without media",
			isViewOnce: true,
			part: &bridgev2.ConvertedMessagePart{Content: &event.MessageEventContent{
				MsgType: event.MsgNotice,
				Body:    "Failed to bridge photo",
			}},
		},
		{
			name:       "nil part",
			isViewOnce: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markViewOnce(tt.part, tt.isViewOnce, tt.disableViewOnce)
			if tt.part == nil {
				return
			}
			limit := tt.part.Content.BeeperViewLimited
			if !tt.wantMarker {
				if limit != nil {
					t.Errorf("BeeperViewLimited = %+v, want nil", limit)
				}
				return
			}
			if limit == nil {
				t.Fatal("BeeperViewLimited is nil, want count=1 limit")
			}
			if limit.Type != "count" || limit.Count != 1 {
				t.Errorf("BeeperViewLimited = %+v, want {count 1}", limit)
			}
		})
	}
}

func TestApplyViewOnce(t *testing.T) {
	imageMsg := func() *waE2E.Message {
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: proto.String("caption"),
		}}
	}
	countLimit := &event.BeeperViewLimitedMedia{Type: "count", Count: 1}

	tests := []struct {
		name      string
		msg       *waE2E.Message
		limit     *event.BeeperViewLimitedMedia
		wantWrap  bool
		wantInner string
	}{
		{
			name:      "image",
			msg:       imageMsg(),
			limit:     countLimit,
			wantWrap:  true,
			wantInner: "image",
		},
		{
			name:      "video",
			msg:       &waE2E.Message{VideoMessage: &waE2E.VideoMessage{}},
			limit:     countLimit,
			wantWrap:  true,
			wantInner: "video",
		},
		{
			name: "voice message",
			msg: &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
				PTT: proto.Bool(true),
			}},
			limit:     countLimit,
			wantWrap:  true,
			wantInner: "audio",
		},
		{
			name:  "document is not view-once capable",
			msg:   &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{}},
			limit: countLimit,
		},
		{
			name:  "sticker is not view-once capable",
			msg:   &waE2E.Message{StickerMessage: &waE2E.StickerMessage{}},
			limit: countLimit,
		},
		{
			name: "no limit",
			msg:  imageMsg(),
		},
		{
			name:  "time-based limit is unsupported",
			msg:   imageMsg(),
			limit: &event.BeeperViewLimitedMedia{Type: "time", Count: 1},
		},
		{
			name:  "zero count",
			msg:   imageMsg(),
			limit: &event.BeeperViewLimitedMedia{Type: "count"},
		},
		{
			name:  "nil message",
			limit: countLimit,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyViewOnce(tt.msg, tt.limit)
			if !tt.wantWrap {
				if tt.msg == nil {
					if result != nil {
						t.Errorf("applyViewOnce(nil) = %v, want nil", result)
					}
					return
				}
				if result != tt.msg {
					t.Error("applyViewOnce replaced an unwrapped message")
				}
				if result != nil && result.GetViewOnceMessage() != nil {
					t.Error("applyViewOnce wrapped a message without a valid view-once limit")
				}
				return
			}
			wrapper := result.GetViewOnceMessage()
			if wrapper == nil || wrapper.GetMessage() == nil {
				t.Fatalf("applyViewOnce() = %v, want viewOnceMessage wrapper", result)
			}
			inner := wrapper.GetMessage()
			var viewOnce bool
			switch tt.wantInner {
			case "image":
				viewOnce = inner.GetImageMessage().GetViewOnce()
				if inner.GetImageMessage().GetCaption() != "caption" {
					t.Error("image caption was lost when wrapping")
				}
			case "video":
				viewOnce = inner.GetVideoMessage().GetViewOnce()
			case "audio":
				viewOnce = inner.GetAudioMessage().GetViewOnce()
				if !inner.GetAudioMessage().GetPTT() {
					t.Error("voice flag was lost when wrapping")
				}
			}
			if !viewOnce {
				t.Errorf("inner %s message does not have the inline viewOnce flag", tt.wantInner)
			}
		})
	}
}

func TestApplyViewOnceUnwrapsInWhatsmeow(t *testing.T) {
	msg := applyViewOnce(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption: proto.String("view once photo"),
	}}, &event.BeeperViewLimitedMedia{Type: "count", Count: 1})

	evt := (&events.Message{RawMessage: msg}).UnwrapRaw()
	if !evt.IsViewOnce {
		t.Error("whatsmeow did not recognise the message as view-once")
	}
	if evt.IsViewOnceV2 {
		t.Error("whatsmeow recognised the legacy wrapper as V2")
	}
	if evt.Message.GetImageMessage().GetCaption() != "view once photo" {
		t.Errorf("unwrapped caption = %q", evt.Message.GetImageMessage().GetCaption())
	}
}
