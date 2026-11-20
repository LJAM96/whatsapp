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
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

type presenceCall struct {
	jid   types.JID
	state types.ChatPresence
	media types.ChatPresenceMedia
}

type fakePresenceSender struct {
	lock  sync.Mutex
	calls []presenceCall
}

func (s *fakePresenceSender) SendChatPresence(_ context.Context, jid types.JID, state types.ChatPresence, media types.ChatPresenceMedia) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.calls = append(s.calls, presenceCall{jid: jid, state: state, media: media})
	return nil
}

func (s *fakePresenceSender) snapshot() []presenceCall {
	s.lock.Lock()
	defer s.lock.Unlock()
	return append([]presenceCall(nil), s.calls...)
}

type fakeRecordingTimer struct {
	clock   *fakeRecordingClock
	fireAt  time.Time
	fn      func()
	stopped bool
}

func (t *fakeRecordingTimer) Stop() bool {
	t.clock.lock.Lock()
	defer t.clock.lock.Unlock()
	wasActive := !t.stopped && t.clock.now.Before(t.fireAt)
	t.stopped = true
	return wasActive
}

// fakeRecordingClock fires AfterFunc callbacks only when advance is called,
// so the 30 s timeout can be tested deterministically.
type fakeRecordingClock struct {
	lock   sync.Mutex
	now    time.Time
	timers []*fakeRecordingTimer
}

func (c *fakeRecordingClock) AfterFunc(d time.Duration, fn func()) afterFuncTimer {
	c.lock.Lock()
	defer c.lock.Unlock()
	timer := &fakeRecordingTimer{clock: c, fireAt: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakeRecordingClock) advance(d time.Duration) {
	c.lock.Lock()
	c.now = c.now.Add(d)
	var due []func()
	for _, timer := range c.timers {
		if !timer.stopped && !timer.fireAt.After(c.now) {
			timer.stopped = true
			due = append(due, timer.fn)
		}
	}
	c.lock.Unlock()
	for _, fn := range due {
		fn()
	}
}

func TestRecordingToPresence(t *testing.T) {
	tests := []struct {
		name      string
		recording bool
		wantState types.ChatPresence
		wantMedia types.ChatPresenceMedia
	}{
		{
			name:      "recording is composing with audio",
			recording: true,
			wantState: types.ChatPresenceComposing,
			wantMedia: types.ChatPresenceMediaAudio,
		},
		{
			name:      "stopped is paused without media",
			recording: false,
			wantState: types.ChatPresencePaused,
			wantMedia: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, media := recordingToPresence(tt.recording)
			if state != tt.wantState || media != tt.wantMedia {
				t.Errorf("recordingToPresence(%v) = (%q, %q), want (%q, %q)",
					tt.recording, state, media, tt.wantState, tt.wantMedia)
			}
		})
	}
}

func TestGetRecordingContent(t *testing.T) {
	preParsed := &RecordingEventContent{Recording: true}
	tests := []struct {
		name    string
		raw     string
		parsed  any
		want    RecordingEventContent
		wantErr bool
	}{
		{name: "true", raw: `{"recording":true}`, want: RecordingEventContent{Recording: true}},
		{name: "false", raw: `{"recording":false}`, want: RecordingEventContent{Recording: false}},
		{name: "missing key defaults to false", raw: `{}`},
		{name: "unknown keys are ignored", raw: `{"recording":true,"extra":1}`, want: RecordingEventContent{Recording: true}},
		{
			name:   "parsed content is preferred",
			raw:    `{"recording":false}`,
			parsed: preParsed,
			want:   RecordingEventContent{Recording: true},
		},
		{name: "invalid JSON is an error", raw: `{"recording":`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &event.Event{Content: event.Content{
				VeryRaw: json.RawMessage(tt.raw),
				Parsed:  tt.parsed,
			}}
			got, err := getRecordingContent(evt)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("getRecordingContent() error: %v", err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("getRecordingContent() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

type recordingStep struct {
	action   string // "true", "false" or "advance"
	portal   string // "a" or "b"
	duration time.Duration
}

func TestRecordingPresence(t *testing.T) {
	chatA := types.NewJID("15551234567", types.DefaultUserServer)
	chatB := types.NewJID("15557654321", types.DefaultUserServer)
	portalKeys := map[string]networkid.PortalKey{
		"a": {ID: waid.MakePortalID(chatA)},
		"b": {ID: waid.MakePortalID(chatB)},
	}
	chatJIDs := map[string]types.JID{"a": chatA, "b": chatB}
	composing := presenceCall{jid: chatA, state: types.ChatPresenceComposing, media: types.ChatPresenceMediaAudio}
	paused := presenceCall{jid: chatA, state: types.ChatPresencePaused}

	tests := []struct {
		name  string
		steps []recordingStep
		want  []presenceCall
	}{
		{
			name:  "true sends composing with audio",
			steps: []recordingStep{{action: "true", portal: "a"}},
			want:  []presenceCall{composing},
		},
		{
			name:  "false sends paused without a timer",
			steps: []recordingStep{{action: "false", portal: "a"}},
			want:  []presenceCall{paused},
		},
		{
			name: "true does not expire before 30 s",
			steps: []recordingStep{
				{action: "true", portal: "a"},
				{action: "advance", duration: 29 * time.Second},
			},
			want: []presenceCall{composing},
		},
		{
			name: "true auto-expires after 30 s",
			steps: []recordingStep{
				{action: "true", portal: "a"},
				{action: "advance", duration: 29 * time.Second},
				{action: "advance", duration: time.Second},
			},
			want: []presenceCall{composing, paused},
		},
		{
			name: "a new true restarts the timeout",
			steps: []recordingStep{
				{action: "true", portal: "a"},
				{action: "advance", duration: 20 * time.Second},
				{action: "true", portal: "a"},
				{action: "advance", duration: 20 * time.Second},
				{action: "advance", duration: 10 * time.Second},
			},
			want: []presenceCall{composing, composing, paused},
		},
		{
			name: "a false cancels the pending timeout",
			steps: []recordingStep{
				{action: "true", portal: "a"},
				{action: "advance", duration: 10 * time.Second},
				{action: "false", portal: "a"},
				{action: "advance", duration: 60 * time.Second},
			},
			want: []presenceCall{composing, paused},
		},
		{
			name: "timers are tracked per portal",
			steps: []recordingStep{
				{action: "true", portal: "a"},
				{action: "true", portal: "b"},
				{action: "advance", duration: 30 * time.Second},
			},
			want: []presenceCall{
				{chatA, types.ChatPresenceComposing, types.ChatPresenceMediaAudio},
				{chatB, types.ChatPresenceComposing, types.ChatPresenceMediaAudio},
				paused,
				{jid: chatB, state: types.ChatPresencePaused},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakePresenceSender{}
			clock := &fakeRecordingClock{now: time.Unix(0, 0)}
			rp := newRecordingPresence(sender, clock)
			for _, step := range tt.steps {
				switch step.action {
				case "advance":
					clock.advance(step.duration)
				case "true", "false":
					if err := rp.set(context.Background(), portalKeys[step.portal], chatJIDs[step.portal], step.action == "true"); err != nil {
						t.Fatalf("set(%s) error: %v", step.action, err)
					}
				default:
					t.Fatalf("unknown step action %q", step.action)
				}
			}
			if got := sender.snapshot(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("presence calls = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHandleRecording(t *testing.T) {
	ownMXID := id.UserID("@own:example.org")
	chatJID := types.NewJID("15551234567", types.DefaultUserServer)
	portal := &bridgev2.Portal{Portal: &database.Portal{
		PortalKey: networkid.PortalKey{ID: waid.MakePortalID(chatJID)},
	}}
	composing := presenceCall{jid: chatJID, state: types.ChatPresenceComposing, media: types.ChatPresenceMediaAudio}
	paused := presenceCall{jid: chatJID, state: types.ChatPresencePaused}
	tests := []struct {
		name    string
		sender  id.UserID
		raw     string
		advance time.Duration
		want    []presenceCall
		wantErr bool
	}{
		{
			name:   "true from own user",
			sender: ownMXID,
			raw:    `{"recording":true}`,
			want:   []presenceCall{composing},
		},
		{
			name:   "false from own user",
			sender: ownMXID,
			raw:    `{"recording":false}`,
			want:   []presenceCall{paused},
		},
		{
			name:    "true from own user auto-expires after 30 s",
			sender:  ownMXID,
			raw:     `{"recording":true}`,
			advance: recordingPresenceTimeout,
			want:    []presenceCall{composing, paused},
		},
		{
			name:   "event from another user is ignored",
			sender: id.UserID("@other:example.org"),
			raw:    `{"recording":true}`,
		},
		{
			name:    "invalid content is an error",
			sender:  ownMXID,
			raw:     `{"recording":`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakePresenceSender{}
			clock := &fakeRecordingClock{now: time.Unix(0, 0)}
			wa := &WhatsAppClient{
				UserLogin: &bridgev2.UserLogin{
					UserLogin: &database.UserLogin{UserMXID: ownMXID},
				},
				MC:        noopMCInstance,
				recording: newRecordingPresence(sender, clock),
			}
			evt := &event.Event{
				Sender:  tt.sender,
				Content: event.Content{VeryRaw: json.RawMessage(tt.raw)},
			}
			err := wa.HandleRecording(context.Background(), portal, evt)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			} else if err != nil {
				t.Fatalf("HandleRecording() error: %v", err)
			}
			clock.advance(tt.advance)
			if got := sender.snapshot(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("presence calls = %+v, want %+v", got, tt.want)
			}
		})
	}
}
