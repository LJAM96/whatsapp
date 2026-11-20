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
	"testing"
	"time"

	"go.mau.fi/util/exsync"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/event"
)

func TestCallTypeFromOfferData(t *testing.T) {
	tests := []struct {
		name string
		data *waBinary.Node
		want event.BeeperActionMessageCallType
	}{
		{
			name: "nil data",
			want: "",
		},
		{
			name: "audio only",
			data: &waBinary.Node{Content: []waBinary.Node{
				{Tag: "audio"},
				{Tag: "audio"},
			}},
			want: event.BeeperActionMessageCallTypeVoice,
		},
		{
			name: "video child",
			data: &waBinary.Node{Content: []waBinary.Node{
				{Tag: "audio"},
				{Tag: "video"},
			}},
			want: event.BeeperActionMessageCallTypeVideo,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := callTypeFromOfferData(tt.data); got != tt.want {
				t.Errorf("callTypeFromOfferData() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCallTypeFromMedia(t *testing.T) {
	tests := []struct {
		media string
		want  event.BeeperActionMessageCallType
	}{
		{media: "audio", want: event.BeeperActionMessageCallTypeVoice},
		{media: "video", want: event.BeeperActionMessageCallTypeVideo},
		{media: "", want: ""},
		{media: "group", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.media, func(t *testing.T) {
			if got := callTypeFromMedia(tt.media); got != tt.want {
				t.Errorf("callTypeFromMedia(%q) = %q, want %q", tt.media, got, tt.want)
			}
		})
	}
}

func TestFormatCallDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{duration: 0, want: "0:00"},
		{duration: 5 * time.Second, want: "0:05"},
		{duration: 65 * time.Second, want: "1:05"},
		{duration: 83*time.Second + 500*time.Millisecond, want: "1:23"},
		{duration: 59*time.Minute + 59*time.Second, want: "59:59"},
		{duration: time.Hour + 2*time.Minute + 3*time.Second, want: "1:02:03"},
		{duration: -time.Second, want: "0:00"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatCallDuration(tt.duration); got != tt.want {
				t.Errorf("formatCallDuration(%s) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestFormatCallNoticeBody(t *testing.T) {
	tests := []struct {
		name     string
		outcome  callOutcome
		callType event.BeeperActionMessageCallType
		duration time.Duration
		want     string
	}{
		{"missed voice", callOutcomeMissed, event.BeeperActionMessageCallTypeVoice, 0, "Missed voice call"},
		{"missed video", callOutcomeMissed, event.BeeperActionMessageCallTypeVideo, 0, "Missed video call"},
		{"missed unknown type", callOutcomeMissed, "", 0, "Missed voice call"},
		{"completed voice", callOutcomeCompleted, event.BeeperActionMessageCallTypeVoice, 83 * time.Second, "Voice call (1:23)"},
		{"completed video", callOutcomeCompleted, event.BeeperActionMessageCallTypeVideo, 125 * time.Second, "Video call (2:05)"},
		{"completed no duration", callOutcomeCompleted, event.BeeperActionMessageCallTypeVoice, 0, "Voice call (0:00)"},
		{"declined voice", callOutcomeDeclined, event.BeeperActionMessageCallTypeVoice, 0, "Declined voice call"},
		{"declined video", callOutcomeDeclined, event.BeeperActionMessageCallTypeVideo, 0, "Declined video call"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCallNoticeBody(tt.outcome, tt.callType, tt.duration); got != tt.want {
				t.Errorf("formatCallNoticeBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCallNoticeForFinishedCall(t *testing.T) {
	start := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	accept := start.Add(3 * time.Second)
	end := accept.Add(83 * time.Second)
	tests := []struct {
		name    string
		call    *trackedCall
		outcome callOutcome
		endTime time.Time
		want    callNoticeData
	}{
		{
			name:    "duration from accept and terminate",
			call:    &trackedCall{callType: event.BeeperActionMessageCallTypeVideo, startTime: start, acceptTime: accept, accepted: true},
			outcome: callOutcomeCompleted,
			endTime: end,
			want:    callNoticeData{Body: "Video call (1:23)", CallType: event.BeeperActionMessageCallTypeVideo},
		},
		{
			name:    "duration falls back to offer timestamp",
			call:    &trackedCall{callType: event.BeeperActionMessageCallTypeVoice, startTime: start},
			outcome: callOutcomeMissed,
			endTime: end,
			want:    callNoticeData{Body: "Missed voice call", CallType: event.BeeperActionMessageCallTypeVoice},
		},
		{
			name:    "missing end timestamp gives zero duration",
			call:    &trackedCall{callType: event.BeeperActionMessageCallTypeVoice, startTime: start, accepted: true},
			outcome: callOutcomeCompleted,
			want:    callNoticeData{Body: "Voice call (0:00)", CallType: event.BeeperActionMessageCallTypeVoice},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := callNoticeForFinishedCall(tt.call, tt.outcome, tt.endTime)
			if got != tt.want {
				t.Errorf("callNoticeForFinishedCall() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestConvertCallNotice(t *testing.T) {
	tests := []struct {
		name string
		data callNoticeData
		want string
	}{
		{
			name: "voice",
			data: callNoticeData{Body: "Missed voice call", CallType: event.BeeperActionMessageCallTypeVoice},
			want: `{"msgtype":"m.notice","body":"Missed voice call","com.beeper.action_message":{"type":"call","call_type":"voice"}}`,
		},
		{
			name: "video",
			data: callNoticeData{Body: "Video call (2:05)", CallType: event.BeeperActionMessageCallTypeVideo},
			want: `{"msgtype":"m.notice","body":"Video call (2:05)","com.beeper.action_message":{"type":"call","call_type":"video"}}`,
		},
		{
			name: "unknown type omits call_type",
			data: callNoticeData{Body: "Missed voice call"},
			want: `{"msgtype":"m.notice","body":"Missed voice call","com.beeper.action_message":{"type":"call"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted, err := convertCallNotice(context.Background(), nil, nil, tt.data)
			if err != nil {
				t.Fatalf("convertCallNotice() error: %v", err)
			}
			if len(converted.Parts) != 1 {
				t.Fatalf("got %d parts, want 1", len(converted.Parts))
			}
			part := converted.Parts[0]
			if part.Type != event.EventMessage {
				t.Errorf("part type = %q, want %q", part.Type, event.EventMessage)
			}
			marshaled, err := json.Marshal(part.Content)
			if err != nil {
				t.Fatalf("failed to marshal content: %v", err)
			}
			if string(marshaled) != tt.want {
				t.Errorf("content JSON = %s, want %s", marshaled, tt.want)
			}
		})
	}
}

func TestFinishedCallProducesOneNotice(t *testing.T) {
	wa := &WhatsAppClient{callNotices: make(map[string]*trackedCall)}
	meta := &types.BasicCallMeta{
		CallID:      "CALL-1",
		CallCreator: types.NewJID("15551234567", types.DefaultUserServer),
		Timestamp:   time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC),
	}
	wa.callNotices[meta.CallID] = &trackedCall{
		callID:    meta.CallID,
		chat:      meta.CallCreator,
		sender:    meta.CallCreator,
		callType:  event.BeeperActionMessageCallTypeVoice,
		startTime: meta.Timestamp,
		expiresAt: meta.Timestamp.Add(callEventMaxAge),
	}

	acceptMeta := *meta
	acceptMeta.Timestamp = meta.Timestamp.Add(3 * time.Second)
	wa.updateTrackedCall(&acceptMeta, true)

	endMeta := *meta
	endMeta.Timestamp = meta.Timestamp.Add(86 * time.Second)
	call := wa.callNotices[meta.CallID]
	if call == nil || !call.accepted {
		t.Fatal("call was not marked as accepted")
	}

	if got := callNoticeForFinishedCall(call, callOutcomeCompleted, endMeta.Timestamp).Body; got != "Voice call (1:23)" {
		t.Errorf("notice body = %q, want %q", got, "Voice call (1:23)")
	}
	// consumeTrackedCall removes the entry, so a duplicate terminate event
	// cannot produce a second notice for the same call ID.
	if got := wa.consumeTrackedCall(&endMeta); got == nil {
		t.Fatal("first final event did not consume the call")
	}
	if got := wa.consumeTrackedCall(&endMeta); got != nil {
		t.Errorf("duplicate final event returned a call: %+v", got)
	}
}

func TestIsDeclinedCallReason(t *testing.T) {
	tests := []struct {
		reason string
		want   bool
	}{
		{"declined", true},
		{"Declined", true},
		{"rejected", true},
		{"busy", true},
		{"timeout", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			if got := isDeclinedCallReason(tt.reason); got != tt.want {
				t.Errorf("isDeclinedCallReason(%q) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}

func TestConvertCallStart(t *testing.T) {
	tests := []struct {
		name     string
		callType string
		wantBody string
	}{
		{
			name:     "voice",
			callType: "",
			wantBody: "Incoming call. Use the WhatsApp app to answer.",
		},
		{
			name:     "audio call type",
			callType: "audio",
			wantBody: "Incoming audio call. Use the WhatsApp app to answer.",
		},
		{
			name:     "video call type",
			callType: "video",
			wantBody: "Incoming video call. Use the WhatsApp app to answer.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted, err := convertCallStart(context.Background(), nil, nil, tt.callType)
			if err != nil {
				t.Fatalf("convertCallStart() error: %v", err)
			}
			if len(converted.Parts) != 1 {
				t.Fatalf("got %d parts, want 1", len(converted.Parts))
			}
			content := converted.Parts[0].Content
			if content.MsgType != event.MsgText {
				t.Errorf("msgtype = %q, want %q", content.MsgType, event.MsgText)
			}
			if content.Body != tt.wantBody {
				t.Errorf("body = %q, want %q", content.Body, tt.wantBody)
			}
			if content.BeeperActionMessage == nil || content.BeeperActionMessage.Type != event.BeeperActionMessageCall {
				t.Fatalf("unexpected action message: %+v", content.BeeperActionMessage)
			}
			if got := content.BeeperActionMessage.CallType; got != event.BeeperActionMessageCallType(tt.callType) {
				t.Errorf("call type = %q, want %q", got, tt.callType)
			}
		})
	}
}

func TestCallNoticeConfigGates(t *testing.T) {
	recent := time.Now().Add(-time.Second)
	old := time.Now().Add(-2 * callEventMaxAge)
	tests := []struct {
		name           string
		startNotices   bool
		historyNotices bool
		ts             time.Time
		wantStart      bool
		wantHistory    bool
	}{
		{"both disabled", false, false, recent, false, false},
		{"only start notices", true, false, recent, true, false},
		{"only history notices", false, true, recent, false, true},
		{"both enabled", true, true, recent, true, true},
		{"stale calls are gated", true, true, old, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{CallStartNotices: tt.startNotices, CallHistoryNotices: tt.historyNotices}
			if got := shouldSendCallStartNotice(cfg, tt.ts); got != tt.wantStart {
				t.Errorf("shouldSendCallStartNotice() = %v, want %v", got, tt.wantStart)
			}
			meta := &types.BasicCallMeta{CallID: "CALL-1", Timestamp: tt.ts}
			if got := shouldTrackCallHistory(cfg, meta); got != tt.wantHistory {
				t.Errorf("shouldTrackCallHistory() = %v, want %v", got, tt.wantHistory)
			}
		})
	}
}

func TestTrackCallOfferFollowsHistoryGateIndependently(t *testing.T) {
	meta := &types.BasicCallMeta{
		CallID:      "CALL-1",
		CallCreator: types.NewJID("lid-user", types.HiddenUserServer),
		Timestamp:   time.Now(),
	}
	tests := []struct {
		name           string
		startNotices   bool
		historyNotices bool
		wantTracked    bool
	}{
		{"history on, start off", false, true, true},
		{"history on, start on", true, true, true},
		{"history off, start on", true, false, false},
		{"both off", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wa := &WhatsAppClient{
				Main:            &WhatsAppConnector{Config: Config{CallStartNotices: tt.startNotices, CallHistoryNotices: tt.historyNotices}},
				callNotices:     make(map[string]*trackedCall),
				previousOwnLIDs: exsync.NewSet[types.JID](),
			}
			wa.trackCallOffer(context.Background(), meta, event.BeeperActionMessageCallTypeVoice)
			tracked := wa.callNotices[meta.CallID]
			if tt.wantTracked && tracked == nil {
				t.Fatal("call offer was not tracked")
			}
			if !tt.wantTracked && tracked != nil {
				t.Fatalf("call offer was tracked despite call_history_notices being off: %+v", tracked)
			}
		})
	}
}
