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

	"maunium.net/go/mautrix/event"
)

func TestViewOnceCapabilities(t *testing.T) {
	tests := []struct {
		name        string
		msgType     event.CapabilityMsgType
		limit       *event.BeeperViewLimitedMedia
		wantSupport bool
	}{
		{name: "image count one", msgType: event.MsgImage, limit: waViewOnceLimit, wantSupport: true},
		{name: "video count one", msgType: event.MsgVideo, limit: waViewOnceLimit, wantSupport: true},
		{name: "GIF count one", msgType: event.CapMsgGIF, limit: waViewOnceLimit, wantSupport: true},
		{name: "audio count one", msgType: event.MsgAudio, limit: waViewOnceLimit, wantSupport: true},
		{name: "voice count one", msgType: event.CapMsgVoice, limit: waViewOnceLimit, wantSupport: true},
		{name: "file count one", msgType: event.MsgFile, limit: waViewOnceLimit},
		{name: "sticker count one", msgType: event.CapMsgSticker, limit: waViewOnceLimit},
		{name: "image count two", msgType: event.MsgImage, limit: &event.BeeperViewLimitedMedia{Type: "count", Count: 2}},
		{name: "image time limit", msgType: event.MsgImage, limit: &event.BeeperViewLimitedMedia{Type: "time", Count: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			features, ok := whatsappCaps.File[tt.msgType]
			if !ok {
				t.Fatalf("no file features for %s", tt.msgType)
			}
			if supported := features.SupportsViewLimitedType(tt.limit); supported != tt.wantSupport {
				t.Errorf("SupportsViewLimitedType(%+v) = %v, want %v", tt.limit, supported, tt.wantSupport)
			}
		})
	}
}
