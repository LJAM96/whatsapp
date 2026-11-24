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
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

// StarEventType is the Weave-specific timeline event used to star or unstar a
// bridged WhatsApp message.
var StarEventType = event.Type{
	Type:  "com.lukemulvaney.weave.star",
	Class: event.MessageEventType,
}

// StarEventContent is the content of a StarEventType event. EventID is the
// Matrix event ID of the message to star, not the WhatsApp message ID.
type StarEventContent struct {
	EventID id.EventID `json:"eventId"`
	Starred bool       `json:"starred"`
}

func init() {
	event.TypeMap[StarEventType] = reflect.TypeFor[StarEventContent]()
}

// getStarContent returns the parsed content of a star event.
func getStarContent(evt *event.Event) (*StarEventContent, error) {
	if content, ok := evt.Content.Parsed.(*StarEventContent); ok {
		return content, nil
	}
	var content StarEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		return nil, fmt.Errorf("failed to parse star content: %w", err)
	}
	return &content, nil
}

// starEventToAppState resolves a Matrix event ID to a bridged WhatsApp message
// with getByMXID and builds the matching star app-state patch. ok is false when
// the event doesn't map to a bridged message; such events are silently skipped.
func (wa *WhatsAppClient) starEventToAppState(ctx context.Context, eventID id.EventID, starred bool, getByMXID messageByMXID) (patch appstate.PatchInfo, ok bool, err error) {
	part, err := getByMXID(ctx, eventID)
	if err != nil {
		return patch, false, fmt.Errorf("failed to look up starred message: %w", err)
	} else if part == nil {
		return patch, false, nil
	}
	parsed, err := waid.ParseMessageID(part.ID)
	if err != nil {
		zerolog.Ctx(ctx).Debug().Err(err).
			Str("event_id", string(eventID)).
			Msg("Ignoring star of an event without a bridged message ID")
		return patch, false, nil
	}
	return appstate.BuildStar(parsed.Chat, parsed.Sender, parsed.ID, wa.IsOwnJID(parsed.Sender), starred), true, nil
}

// HandleStar applies a Weave star event to WhatsApp. Like other per-account
// state, stars may only be changed by the login's own Matrix user.
func (wa *WhatsAppClient) HandleStar(ctx context.Context, _ *bridgev2.Portal, evt *event.Event) (retErr error) {
	if wa.UserLogin == nil || evt.Sender != wa.UserLogin.UserMXID {
		zerolog.Ctx(ctx).Debug().
			Stringer("sender", evt.Sender).
			Msg("Ignoring star event from another user")
		return nil
	}
	content, err := getStarContent(evt)
	if err != nil {
		return err
	} else if content.EventID == "" {
		zerolog.Ctx(ctx).Debug().Msg("Ignoring star event without event ID")
		return nil
	}
	patch, ok, err := wa.starEventToAppState(ctx, content.EventID, content.Starred, wa.Main.Bridge.DB.Message.GetPartByMXID)
	if err != nil {
		return err
	} else if !ok {
		return nil
	}
	defer wa.mcTrack(evt, time.Now(), &retErr)
	if err = wa.Client.SendAppState(ctx, patch); err != nil {
		return wrapWhatsAppError(err)
	}
	return nil
}
