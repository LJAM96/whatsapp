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
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

var _ chatPresenceSender = (*WhatsAppClient)(nil)

// recordingPresenceTimeout is how long a true recording signal is honoured
// before the bridge sends paused on its own.
const recordingPresenceTimeout = 30 * time.Second

// RecordingEventType is the Weave-specific timeline event used to signal the
// start and end of a voice recording in a portal.
var RecordingEventType = event.Type{
	Type:  "com.lukemulvaney.weave.recording",
	Class: event.MessageEventType,
}

// RecordingEventContent is the content of a RecordingEventType event.
type RecordingEventContent struct {
	Recording bool `json:"recording"`
}

func init() {
	// As with chat settings, registering the content type lets event.Content.ParseRaw
	// fill evt.Content.Parsed for events delivered through the custom-event hook.
	event.TypeMap[RecordingEventType] = reflect.TypeFor[RecordingEventContent]()
}

// recordingToPresence maps the recording flag onto the whatsmeow chat presence
// arguments. A recording is composing with the audio media type; stopping (or
// the timeout) is paused, which never carries a media type.
func recordingToPresence(recording bool) (types.ChatPresence, types.ChatPresenceMedia) {
	if recording {
		return types.ChatPresenceComposing, types.ChatPresenceMediaAudio
	}
	return types.ChatPresencePaused, ""
}

func getRecordingContent(evt *event.Event) (*RecordingEventContent, error) {
	if content, ok := evt.Content.Parsed.(*RecordingEventContent); ok {
		return content, nil
	}
	var content RecordingEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		return nil, fmt.Errorf("failed to parse recording content: %w", err)
	}
	return &content, nil
}

// chatPresenceSender is the part of whatsmeow used for recording presence.
// Keeping it narrow lets tests substitute a fake sender.
type chatPresenceSender interface {
	SendChatPresence(ctx context.Context, jid types.JID, state types.ChatPresence, media types.ChatPresenceMedia) error
}

// afterFuncTimer is the part of time.Timer used for the auto-expire timeout.
type afterFuncTimer interface {
	Stop() bool
}

// recordingClock abstracts the timeout so tests can drive it with a fake clock.
type recordingClock interface {
	AfterFunc(d time.Duration, f func()) afterFuncTimer
}

type realRecordingClock struct{}

func (realRecordingClock) AfterFunc(d time.Duration, f func()) afterFuncTimer {
	return time.AfterFunc(d, f)
}

// recordingPresence tracks one pending auto-expire timer per portal. Every new
// event for a portal cancels that portal's timer before applying the new state:
// a fresh true restarts the 30 s window, a false stops it for good.
type recordingPresence struct {
	sender chatPresenceSender
	clock  recordingClock
	lock   sync.Mutex
	timers map[networkid.PortalKey]afterFuncTimer
}

func newRecordingPresence(sender chatPresenceSender, clock recordingClock) *recordingPresence {
	return &recordingPresence{
		sender: sender,
		clock:  clock,
		timers: make(map[networkid.PortalKey]afterFuncTimer),
	}
}

func (rp *recordingPresence) set(ctx context.Context, key networkid.PortalKey, jid types.JID, recording bool) error {
	state, media := recordingToPresence(recording)
	rp.lock.Lock()
	defer rp.lock.Unlock()

	if timer := rp.timers[key]; timer != nil {
		timer.Stop()
		delete(rp.timers, key)
	}
	if err := rp.sender.SendChatPresence(ctx, jid, state, media); err != nil {
		return err
	}
	if !recording {
		return nil
	}
	// The timeout outlives the Matrix event handler, so drop its cancellation
	// while keeping values such as the logger.
	timerCtx := context.WithoutCancel(ctx)
	var timer afterFuncTimer
	timer = rp.clock.AfterFunc(recordingPresenceTimeout, func() {
		rp.expire(timerCtx, key, jid, timer)
	})
	rp.timers[key] = timer
	return nil
}

// expire sends paused when a timer fires. It checks that the timer is still the
// active one for the portal so a callback racing a newer event can't cancel the
// newer state.
func (rp *recordingPresence) expire(ctx context.Context, key networkid.PortalKey, jid types.JID, timer afterFuncTimer) {
	rp.lock.Lock()
	active := rp.timers[key] == timer
	if active {
		delete(rp.timers, key)
	}
	rp.lock.Unlock()
	if !active {
		return
	}
	if err := rp.sender.SendChatPresence(ctx, jid, types.ChatPresencePaused, ""); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Stringer("chat", jid).
			Msg("Failed to expire recording presence")
	}
}

// SendChatPresence forwards a chat presence update to whatsmeow. Implementing
// chatPresenceSender on the client keeps production wiring simple while letting
// tests inject a fake sender into recordingPresence.
func (wa *WhatsAppClient) SendChatPresence(ctx context.Context, jid types.JID, state types.ChatPresence, media types.ChatPresenceMedia) error {
	if wa.Client == nil {
		return bridgev2.ErrNotLoggedIn
	}
	return wa.Client.SendChatPresence(ctx, jid, state, media)
}

// HandleRecording applies a Weave recording event to WhatsApp chat presence.
// As with chat settings, events from anyone other than this login's own Matrix
// user are ignored: chat state belongs to the account and must not be driven
// by another room member.
func (wa *WhatsAppClient) HandleRecording(ctx context.Context, portal *bridgev2.Portal, evt *event.Event) (retErr error) {
	if wa.UserLogin == nil || evt.Sender != wa.UserLogin.UserMXID {
		zerolog.Ctx(ctx).Debug().
			Stringer("sender", evt.Sender).
			Msg("Ignoring recording event from another user")
		return nil
	}
	content, err := getRecordingContent(evt)
	if err != nil {
		return err
	}
	chatJID, err := waid.ParsePortalID(portal.ID)
	if err != nil {
		return err
	}
	defer wa.mcTrack(evt, time.Now(), &retErr)
	return wa.recording.set(ctx, portal.PortalKey, chatJID, content.Recording)
}
