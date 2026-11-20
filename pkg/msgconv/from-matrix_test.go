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
	"context"
	"encoding/json"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/event"
)

func TestConstructMediaMessageVideoNote(t *testing.T) {
	uploaded := &whatsmeow.UploadResponse{
		URL:           "mxc://example.com/media",
		DirectPath:    "/path/to/media",
		MediaKey:      []byte{1, 2, 3},
		FileEncSHA256: []byte{4, 5, 6},
		FileSHA256:    []byte{7, 8, 9},
		FileLength:    4096,
	}
	tests := []struct {
		name        string
		info        *event.FileInfo
		wantPTV     bool
		wantWidth   uint32
		wantHeight  uint32
		wantSeconds uint32
	}{
		{
			name: "marked video note is square",
			info: &event.FileInfo{
				MimeType: "video/mp4",
				Width:    480,
				Height:   270,
				Duration: 12000,
				Extra:    map[string]any{VideoNoteField: true},
			},
			wantPTV:     true,
			wantWidth:   480,
			wantHeight:  480,
			wantSeconds: 12,
		},
		{
			name: "marked video note with height larger than width",
			info: &event.FileInfo{
				MimeType: "video/mp4",
				Width:    270,
				Height:   480,
				Duration: 9000,
				Extra:    map[string]any{VideoNoteField: true},
			},
			wantPTV:     true,
			wantWidth:   480,
			wantHeight:  480,
			wantSeconds: 9,
		},
		{
			name: "normal video keeps dimensions",
			info: &event.FileInfo{
				MimeType: "video/mp4",
				Width:    480,
				Height:   270,
				Duration: 12000,
			},
			wantPTV:     false,
			wantWidth:   480,
			wantHeight:  270,
			wantSeconds: 12,
		},
		{
			name: "marker set to false is a normal video",
			info: &event.FileInfo{
				MimeType: "video/mp4",
				Width:    480,
				Height:   270,
				Duration: 12000,
				Extra:    map[string]any{VideoNoteField: false},
			},
			wantPTV:     false,
			wantWidth:   480,
			wantHeight:  270,
			wantSeconds: 12,
		},
		{
			name: "non-bool marker is a normal video",
			info: &event.FileInfo{
				MimeType: "video/mp4",
				Width:    480,
				Height:   270,
				Duration: 12000,
				Extra:    map[string]any{VideoNoteField: "true"},
			},
			wantPTV:     false,
			wantWidth:   480,
			wantHeight:  270,
			wantSeconds: 12,
		},
	}
	mc := &MessageConverter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := &event.MessageEventContent{
				MsgType:  event.MsgVideo,
				Body:     "video.mp4",
				FileName: "video.mp4",
				Info:     tt.info,
			}
			msg := mc.constructMediaMessage(context.Background(), content, &event.Event{}, uploaded, nil, &waE2E.ContextInfo{}, "video/mp4")
			var video *waE2E.VideoMessage
			if tt.wantPTV {
				if msg.PtvMessage == nil {
					t.Fatal("PtvMessage is nil, want a video note")
				}
				if msg.VideoMessage != nil {
					t.Error("VideoMessage is set, want only PtvMessage")
				}
				video = msg.PtvMessage
			} else {
				if msg.VideoMessage == nil {
					t.Fatal("VideoMessage is nil, want a normal video")
				}
				if msg.PtvMessage != nil {
					t.Error("PtvMessage is set, want only VideoMessage")
				}
				video = msg.VideoMessage
			}
			if video.GetWidth() != tt.wantWidth {
				t.Errorf("width = %d, want %d", video.GetWidth(), tt.wantWidth)
			}
			if video.GetHeight() != tt.wantHeight {
				t.Errorf("height = %d, want %d", video.GetHeight(), tt.wantHeight)
			}
			if video.GetSeconds() != tt.wantSeconds {
				t.Errorf("seconds = %d, want %d", video.GetSeconds(), tt.wantSeconds)
			}
			if video.GetMimetype() != "video/mp4" {
				t.Errorf("mimetype = %q, want video/mp4", video.GetMimetype())
			}
			if video.GetURL() != uploaded.URL {
				t.Errorf("url = %q, want %q", video.GetURL(), uploaded.URL)
			}
			if video.GetFileLength() != uploaded.FileLength {
				t.Errorf("file length = %d, want %d", video.GetFileLength(), uploaded.FileLength)
			}
			if video.GetGifPlayback() {
				t.Error("gif playback is set, want a normal video note")
			}
		})
	}
}

func TestVideoNoteRoundTrip(t *testing.T) {
	prepared := prepareMediaMessage(&waE2E.VideoMessage{
		Mimetype:   proto.String("video/mp4"),
		Seconds:    proto.Uint32(10),
		Width:      proto.Uint32(320),
		Height:     proto.Uint32(320),
		FileLength: proto.Uint64(2048),
	}, true)
	original := event.Content{Parsed: prepared.MessageEventContent, Raw: prepared.Extra}
	data, err := json.Marshal(&original)
	if err != nil {
		t.Fatal(err)
	}
	var received event.Content
	received.VeryRaw = data
	if err = received.ParseRaw(event.EventMessage); err != nil {
		t.Fatal(err)
	}
	content, ok := received.Parsed.(*event.MessageEventContent)
	if !ok {
		t.Fatalf("parsed content = %T, want *event.MessageEventContent", received.Parsed)
	}
	uploaded := &whatsmeow.UploadResponse{
		URL:           "mxc://example.com/media",
		DirectPath:    "/path/to/media",
		MediaKey:      []byte{1, 2, 3},
		FileEncSHA256: []byte{4, 5, 6},
		FileSHA256:    []byte{7, 8, 9},
		FileLength:    2048,
	}
	mc := &MessageConverter{}
	msg := mc.constructMediaMessage(context.Background(), content, &event.Event{}, uploaded, nil, &waE2E.ContextInfo{}, "video/mp4")
	if msg.PtvMessage == nil {
		t.Fatal("round-tripped video note was not converted to PtvMessage")
	}
	if msg.VideoMessage != nil {
		t.Error("round-tripped video note also set VideoMessage")
	}
	if msg.PtvMessage.GetWidth() != 320 || msg.PtvMessage.GetHeight() != 320 {
		t.Errorf("round-tripped dimensions = %dx%d, want 320x320", msg.PtvMessage.GetWidth(), msg.PtvMessage.GetHeight())
	}
	if msg.PtvMessage.GetSeconds() != 10 {
		t.Errorf("round-tripped duration = %d, want 10", msg.PtvMessage.GetSeconds())
	}
}
