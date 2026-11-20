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
	"time"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestCallLogOutcomeFromProto(t *testing.T) {
	tests := []struct {
		outcome waE2E.CallLogMessage_CallOutcome
		want    callLogOutcome
	}{
		{waE2E.CallLogMessage_CONNECTED, callLogOutcomeCompleted},
		{waE2E.CallLogMessage_MISSED, callLogOutcomeMissed},
		{waE2E.CallLogMessage_FAILED, callLogOutcomeMissed},
		{waE2E.CallLogMessage_REJECTED, callLogOutcomeDeclined},
		{waE2E.CallLogMessage_ACCEPTED_ELSEWHERE, callLogOutcomeMissed},
		{waE2E.CallLogMessage_ONGOING, callLogOutcomeMissed},
		{waE2E.CallLogMessage_SILENCED_BY_DND, callLogOutcomeMissed},
		{waE2E.CallLogMessage_SILENCED_UNKNOWN_CALLER, callLogOutcomeMissed},
		{waE2E.CallLogMessage_CallOutcome(99), callLogOutcomeMissed},
	}
	for _, tt := range tests {
		t.Run(tt.outcome.String(), func(t *testing.T) {
			if got := callLogOutcomeFromProto(tt.outcome); got != tt.want {
				t.Errorf("callLogOutcomeFromProto(%s) = %d, want %d", tt.outcome, got, tt.want)
			}
		})
	}
}

func TestCallLogCallType(t *testing.T) {
	tests := []struct {
		name string
		msg  *waE2E.CallLogMessage
		want event.BeeperActionMessageCallType
	}{
		{"voice by default", &waE2E.CallLogMessage{}, event.BeeperActionMessageCallTypeVoice},
		{"voice flag", &waE2E.CallLogMessage{IsVideo: ptr.Ptr(false)}, event.BeeperActionMessageCallTypeVoice},
		{"video flag", &waE2E.CallLogMessage{IsVideo: ptr.Ptr(true)}, event.BeeperActionMessageCallTypeVideo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := callLogCallType(tt.msg); got != tt.want {
				t.Errorf("callLogCallType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatCallLogDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{0, "0:00"},
		{5 * time.Second, "0:05"},
		{83*time.Second + 500*time.Millisecond, "1:23"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1:02:03"},
		{-time.Second, "0:00"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatCallLogDuration(tt.duration); got != tt.want {
				t.Errorf("formatCallLogDuration(%s) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestFormatCallLogNoticeBody(t *testing.T) {
	tests := []struct {
		name     string
		outcome  callLogOutcome
		callType event.BeeperActionMessageCallType
		duration time.Duration
		outgoing bool
		want     string
	}{
		{"missed incoming voice", callLogOutcomeMissed, event.BeeperActionMessageCallTypeVoice, 0, false, "Missed incoming voice call"},
		{"missed incoming video", callLogOutcomeMissed, event.BeeperActionMessageCallTypeVideo, 0, false, "Missed incoming video call"},
		{"missed outgoing voice", callLogOutcomeMissed, event.BeeperActionMessageCallTypeVoice, 0, true, "Missed outgoing voice call"},
		{"declined incoming voice", callLogOutcomeDeclined, event.BeeperActionMessageCallTypeVoice, 0, false, "Declined incoming voice call"},
		{"declined outgoing video", callLogOutcomeDeclined, event.BeeperActionMessageCallTypeVideo, 0, true, "Declined outgoing video call"},
		{"completed incoming voice", callLogOutcomeCompleted, event.BeeperActionMessageCallTypeVoice, 83 * time.Second, false, "Incoming voice call (1:23)"},
		{"completed outgoing video", callLogOutcomeCompleted, event.BeeperActionMessageCallTypeVideo, 2*time.Minute + 5*time.Second, true, "Outgoing video call (2:05)"},
		{"completed zero duration", callLogOutcomeCompleted, event.BeeperActionMessageCallTypeVoice, 0, true, "Outgoing voice call (0:00)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCallLogNoticeBody(tt.outcome, tt.callType, tt.duration, tt.outgoing)
			if got != tt.want {
				t.Errorf("formatCallLogNoticeBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConvertCallLogMessage(t *testing.T) {
	tests := []struct {
		name     string
		msg      *waE2E.CallLogMessage
		fromMe   bool
		wantJSON string
	}{
		{
			name:     "missed incoming voice",
			msg:      &waE2E.CallLogMessage{CallOutcome: ptr.Ptr(waE2E.CallLogMessage_MISSED)},
			wantJSON: `{"msgtype":"m.notice","body":"Missed incoming voice call","com.beeper.action_message":{"type":"call","call_type":"voice"}}`,
		},
		{
			name:     "rejected outgoing video",
			msg:      &waE2E.CallLogMessage{IsVideo: ptr.Ptr(true), CallOutcome: ptr.Ptr(waE2E.CallLogMessage_REJECTED)},
			fromMe:   true,
			wantJSON: `{"msgtype":"m.notice","body":"Declined outgoing video call","com.beeper.action_message":{"type":"call","call_type":"video"}}`,
		},
		{
			name:     "connected voice with duration",
			msg:      &waE2E.CallLogMessage{CallOutcome: ptr.Ptr(waE2E.CallLogMessage_CONNECTED), DurationSecs: ptr.Ptr(int64(83))},
			fromMe:   true,
			wantJSON: `{"msgtype":"m.notice","body":"Outgoing voice call (1:23)","com.beeper.action_message":{"type":"call","call_type":"voice"}}`,
		},
		{
			name:     "unset outcome falls back to missed",
			msg:      &waE2E.CallLogMessage{},
			wantJSON: `{"msgtype":"m.notice","body":"Missed incoming voice call","com.beeper.action_message":{"type":"call","call_type":"voice"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &types.MessageInfo{MessageSource: types.MessageSource{IsFromMe: tt.fromMe}}
			part, contextInfo := (&MessageConverter{}).convertCallLogMessage(context.Background(), info, tt.msg)
			if contextInfo != nil {
				t.Errorf("contextInfo = %+v, want nil", contextInfo)
			}
			if part.Type != event.EventMessage {
				t.Errorf("part type = %q, want %q", part.Type, event.EventMessage)
			}
			marshaled, err := json.Marshal(part.Content)
			if err != nil {
				t.Fatalf("failed to marshal content: %v", err)
			}
			if string(marshaled) != tt.wantJSON {
				t.Errorf("content JSON = %s, want %s", marshaled, tt.wantJSON)
			}
		})
	}
}

func TestToMatrixCallLogMessage(t *testing.T) {
	portal := &bridgev2.Portal{Portal: &database.Portal{Metadata: &waid.PortalMetadata{}}}
	tests := []struct {
		name     string
		msg      *waE2E.CallLogMessage
		fromMe   bool
		wantBody string
		wantType event.BeeperActionMessageCallType
	}{
		{
			name:     "missed incoming voice",
			msg:      &waE2E.CallLogMessage{CallOutcome: ptr.Ptr(waE2E.CallLogMessage_MISSED)},
			wantBody: "Missed incoming voice call",
			wantType: event.BeeperActionMessageCallTypeVoice,
		},
		{
			name:     "connected outgoing video",
			msg:      &waE2E.CallLogMessage{IsVideo: ptr.Ptr(true), CallOutcome: ptr.Ptr(waE2E.CallLogMessage_CONNECTED), DurationSecs: ptr.Ptr(int64(125))},
			fromMe:   true,
			wantBody: "Outgoing video call (2:05)",
			wantType: event.BeeperActionMessageCallTypeVideo,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &types.MessageInfo{MessageSource: types.MessageSource{IsFromMe: tt.fromMe}}
			waMsg := &waE2E.Message{CallLogMesssage: tt.msg}
			converted := (&MessageConverter{}).ToMatrix(context.Background(), portal, nil, nil, waMsg, waMsg, info, false, true, nil)
			if len(converted.Parts) != 1 {
				t.Fatalf("got %d parts, want 1", len(converted.Parts))
			}
			content := converted.Parts[0].Content
			if content.Body != tt.wantBody {
				t.Errorf("body = %q, want %q", content.Body, tt.wantBody)
			}
			if content.MsgType != event.MsgNotice {
				t.Errorf("msgtype = %q, want %q", content.MsgType, event.MsgNotice)
			}
			action := content.BeeperActionMessage
			if action == nil || action.Type != event.BeeperActionMessageCall || action.CallType != tt.wantType {
				t.Errorf("action = %+v, want call/%s", action, tt.wantType)
			}
		})
	}
}
