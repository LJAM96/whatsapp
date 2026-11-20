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
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/event"
)

func TestPrepareMediaMessageVideoNote(t *testing.T) {
	tests := []struct {
		name        string
		msg         *waE2E.VideoMessage
		isVideoNote bool
		wantMarker  bool
		wantGIF     bool
	}{
		{
			name: "video note",
			msg: &waE2E.VideoMessage{
				Mimetype:   proto.String("video/mp4"),
				Seconds:    proto.Uint32(12),
				Width:      proto.Uint32(320),
				Height:     proto.Uint32(320),
				FileLength: proto.Uint64(4096),
			},
			isVideoNote: true,
			wantMarker:  true,
		},
		{
			name: "normal video",
			msg: &waE2E.VideoMessage{
				Mimetype:   proto.String("video/mp4"),
				Seconds:    proto.Uint32(12),
				Width:      proto.Uint32(320),
				Height:     proto.Uint32(320),
				FileLength: proto.Uint64(4096),
			},
			wantMarker: false,
		},
		{
			name: "GIF is not a video note",
			msg: &waE2E.VideoMessage{
				Mimetype:    proto.String("video/mp4"),
				Seconds:     proto.Uint32(12),
				GifPlayback: proto.Bool(true),
				FileLength:  proto.Uint64(4096),
			},
			wantMarker: false,
			wantGIF:    true,
		},
		{
			name: "video note keeps GIF markers if present",
			msg: &waE2E.VideoMessage{
				Mimetype:    proto.String("video/mp4"),
				Seconds:     proto.Uint32(12),
				GifPlayback: proto.Bool(true),
				FileLength:  proto.Uint64(4096),
			},
			isVideoNote: true,
			wantMarker:  true,
			wantGIF:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prepared := prepareMediaMessage(tt.msg, tt.isVideoNote)
			if prepared.Type != event.EventMessage {
				t.Errorf("event type = %v, want %v", prepared.Type, event.EventMessage)
			}
			if prepared.MsgType != event.MsgVideo {
				t.Errorf("msgtype = %v, want %v", prepared.MsgType, event.MsgVideo)
			}
			if prepared.Info.Duration != 12000 {
				t.Errorf("duration = %d, want 12000", prepared.Info.Duration)
			}
			extraInfo, ok := prepared.Extra["info"].(map[string]any)
			if !ok {
				t.Fatalf("info extra = %T, want map[string]any", prepared.Extra["info"])
			}
			marker, hasMarker := extraInfo[VideoNoteField]
			if hasMarker != tt.wantMarker {
				t.Errorf("video note marker present = %v, want %v", hasMarker, tt.wantMarker)
			} else if tt.wantMarker && marker != true {
				t.Errorf("video note marker = %v, want true", marker)
			}
			_, hasGIF := extraInfo["fi.mau.gif"]
			if hasGIF != tt.wantGIF {
				t.Errorf("GIF marker present = %v, want %v", hasGIF, tt.wantGIF)
			}
		})
	}
}

func TestVideoNoteMarkerInMatrixEvent(t *testing.T) {
	prepared := prepareMediaMessage(&waE2E.VideoMessage{
		Mimetype:   proto.String("video/mp4"),
		Seconds:    proto.Uint32(10),
		FileLength: proto.Uint64(2048),
	}, true)
	content := event.Content{Parsed: prepared.MessageEventContent, Raw: prepared.Extra}
	data, err := json.Marshal(&content)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err = json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["msgtype"] != "m.video" {
		t.Errorf("msgtype = %v, want m.video", parsed["msgtype"])
	}
	info, ok := parsed["info"].(map[string]any)
	if !ok {
		t.Fatalf("info = %T, want map[string]any", parsed["info"])
	}
	if info[VideoNoteField] != true {
		t.Errorf("info[%s] = %v, want true", VideoNoteField, info[VideoNoteField])
	}
	if _, ok = parsed[VideoNoteField]; ok {
		t.Errorf("%s should be in info, not at the top level", VideoNoteField)
	}
}
