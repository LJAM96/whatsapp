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
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

// ChatSettingsEventType is the Weave-specific timeline event used to change
// settings of the portal's WhatsApp chat.
var ChatSettingsEventType = event.Type{
	Type:  "com.lukemulvaney.weave.chat_settings",
	Class: event.MessageEventType,
}

// ChatSettingsEventContent is the content of a ChatSettingsEventType event.
// Every field is a pointer so that an omitted key can be told apart from an
// explicit false or zero value; only present keys are applied on WhatsApp.
//
// mutedUntil follows the same convention as event.BeeperMuteEventContent:
// milliseconds since the epoch, a negative value meaning "forever".
type ChatSettingsEventContent struct {
	Archived     *bool  `json:"archived,omitempty"`
	Pinned       *bool  `json:"pinned,omitempty"`
	Muted        *bool  `json:"muted,omitempty"`
	MutedUntil   *int64 `json:"mutedUntil,omitempty"`
	MarkedUnread *bool  `json:"markedUnread,omitempty"`
}

func init() {
	// Register the custom content type with mautrix-go so event.Content.ParseRaw
	// fills evt.Content.Parsed. Other mautrix-go packages register their types
	// the same way (e.g. pushrules and crypto/ssss).
	event.TypeMap[ChatSettingsEventType] = reflect.TypeFor[ChatSettingsEventContent]()
}

var (
	_ bridgev2.CustomEventTypesProvider      = (*WhatsAppConnector)(nil)
	_ bridgev2.CustomEventHandlingNetworkAPI = (*WhatsAppClient)(nil)
)

// GetCustomMatrixEventTypes declares the event types that the Matrix connector
// should register and forward to HandleMatrixCustomEvent. Alongside the
// Weave-specific control events, this includes the MSC3672 live location
// events: a state type (beacon_info) and a message type (beacon). The hook
// takes event.Type values, whose Class already distinguishes state from
// timeline events, so both can be declared here.
func (wa *WhatsAppConnector) GetCustomMatrixEventTypes() []event.Type {
	return []event.Type{
		ChatSettingsEventType,
		event.StateUnstableBeaconInfo,
		event.EventUnstableBeacon,
	}
}

// HandleMatrixCustomEvent routes declared custom events to their handlers.
func (wa *WhatsAppClient) HandleMatrixCustomEvent(ctx context.Context, msg *bridgev2.MatrixCustomEvent) error {
	if msg == nil || msg.Event == nil {
		return nil
	}
	switch msg.Event.Type {
	case ChatSettingsEventType:
		return wa.HandleChatSettings(ctx, msg.Portal, msg.Event)
	case event.StateUnstableBeaconInfo:
		return wa.HandleMatrixBeaconInfo(ctx, msg.Portal, msg.Event)
	case event.EventUnstableBeacon:
		return wa.HandleMatrixBeacon(ctx, msg.Portal, msg.Event)
	default:
		return nil
	}
}

// getChatSettingsContent returns the parsed content of a chat settings event.
func getChatSettingsContent(evt *event.Event) (*ChatSettingsEventContent, error) {
	if content, ok := evt.Content.Parsed.(*ChatSettingsEventContent); ok {
		return content, nil
	}
	var content ChatSettingsEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		return nil, fmt.Errorf("failed to parse chat settings content: %w", err)
	}
	return &content, nil
}

// chatSettingsToAppState converts the present chat settings into app-state
// patches. There is one patch per changed setting because SendAppState takes a
// single PatchInfo; patches are ordered archive, pin, mute, mark-as-read so an
// explicit pinned value wins over the implicit unpin BuildArchive adds when
// archiving a chat.
func chatSettingsToAppState(chatJID types.JID, content *ChatSettingsEventContent, lastTS time.Time, lastKey *waCommon.MessageKey) []appstate.PatchInfo {
	var patches []appstate.PatchInfo
	if content.Archived != nil {
		patches = append(patches, appstate.BuildArchive(chatJID, *content.Archived, lastTS, lastKey))
	}
	if content.Pinned != nil {
		patches = append(patches, appstate.BuildPin(chatJID, *content.Pinned))
	}
	if content.Muted != nil || content.MutedUntil != nil {
		muted, mutedUntil := muteFromSettings(content)
		patches = append(patches, appstate.BuildMuteAbs(chatJID, muted, mutedUntil))
	}
	if content.MarkedUnread != nil {
		// BuildMarkChatAsRead takes a "read" flag, so the unread value is inverted.
		patches = append(patches, appstate.BuildMarkChatAsRead(chatJID, !*content.MarkedUnread, lastTS, lastKey))
	}
	return patches
}

// muteFromSettings maps the optional muted/mutedUntil pair onto the absolute
// mute arguments used by appstate.BuildMuteAbs. A mutedUntil value on its own
// implies muting; zero clears the mute; a negative value means forever.
func muteFromSettings(content *ChatSettingsEventContent) (muted bool, mutedUntil *int64) {
	if content.Muted != nil {
		muted = *content.Muted
	} else if content.MutedUntil != nil {
		muted = *content.MutedUntil != 0
	}
	if muted && content.MutedUntil != nil && *content.MutedUntil != 0 {
		mutedUntil = content.MutedUntil
	}
	return
}

// HandleChatSettings applies a Weave chat settings event to WhatsApp. Events
// from anyone other than this login's own Matrix user are ignored: chat state
// is per-account and must never be changed by another room member.
func (wa *WhatsAppClient) HandleChatSettings(ctx context.Context, portal *bridgev2.Portal, evt *event.Event) (retErr error) {
	if wa.UserLogin == nil || evt.Sender != wa.UserLogin.UserMXID {
		zerolog.Ctx(ctx).Debug().
			Stringer("sender", evt.Sender).
			Msg("Ignoring chat settings event from another user")
		return nil
	}
	content, err := getChatSettingsContent(evt)
	if err != nil {
		return err
	}
	chatJID, err := waid.ParsePortalID(portal.ID)
	if err != nil {
		return err
	}
	var lastTS time.Time
	var lastKey *waCommon.MessageKey
	if content.Archived != nil || content.MarkedUnread != nil {
		lastTS, lastKey, err = wa.getLastMessageInfo(ctx, chatJID, portal.PortalKey)
		if err != nil {
			return err
		}
	}
	patches := chatSettingsToAppState(chatJID, content, lastTS, lastKey)
	defer wa.mcTrack(evt, time.Now(), &retErr)
	for _, patch := range patches {
		if err = wa.Client.SendAppState(ctx, patch); err != nil {
			return wrapWhatsAppError(err)
		}
	}
	return nil
}
