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

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

// LabelEventType is the Weave-specific timeline event used to apply or remove
// a WhatsApp Business label on the portal's chat.
var LabelEventType = event.Type{
	Type:  "com.lukemulvaney.weave.label",
	Class: event.MessageEventType,
}

// LabelEventContent is the content of a LabelEventType event. Label is the
// human-readable name shown in WhatsApp, not a label ID.
type LabelEventContent struct {
	Label   string `json:"label"`
	Applied bool   `json:"applied"`
}

func init() {
	event.TypeMap[LabelEventType] = reflect.TypeFor[LabelEventContent]()
}

// defaultLabelColor is used when creating a label that doesn't exist yet.
// WhatsApp assigns the actual colour from its own palette.
const defaultLabelColor int32 = 0

// getLabelContent returns the parsed content of a label event.
func getLabelContent(evt *event.Event) (*LabelEventContent, error) {
	if content, ok := evt.Content.Parsed.(*LabelEventContent); ok {
		return content, nil
	}
	var content LabelEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		return nil, fmt.Errorf("failed to parse label content: %w", err)
	}
	return &content, nil
}

// isBusinessAccount reports whether the logged-in account can use labels.
// whatsmeow only stores a business name for WhatsApp Business accounts: it is
// set from the <biz> attribute of the pairing response and updated by
// events.BusinessName.
func (wa *WhatsAppClient) isBusinessAccount() bool {
	if wa.Device != nil && wa.Device.BusinessName != "" {
		return true
	}
	return wa.Client != nil && wa.Client.Store != nil && wa.Client.Store.BusinessName != ""
}

// findLabelID returns the ID of the label with the given name, or an empty
// string if it isn't tracked yet.
func findLabelID(labels map[string]waid.WALabel, name string) string {
	for id, label := range labels {
		if label.Name == name {
			return id
		}
	}
	return ""
}

// labelToAppState builds the app-state patches for applying or removing a
// label on a chat. A missing label is created with BuildLabelEdit before the
// association is sent, but only when it's being applied: unapplying an unknown
// label would just create an empty label. The labels map is updated in place
// when a new label is created; created reports whether that happened. The
// caller must hold the label lock and save the metadata when created is true.
func labelToAppState(labels map[string]waid.WALabel, chatJID types.JID, name string, applied bool, newLabelID func() string) (patches []appstate.PatchInfo, created bool) {
	labelID := findLabelID(labels, name)
	if labelID == "" {
		if !applied {
			return nil, false
		}
		labelID = newLabelID()
		patches = append(patches, appstate.BuildLabelEdit(labelID, name, defaultLabelColor, false))
		labels[labelID] = waid.WALabel{Name: name, Color: defaultLabelColor}
		created = true
	}
	return append(patches, appstate.BuildLabelChat(chatJID, labelID, applied)), created
}

// HandleLabel applies a Weave label event to WhatsApp. Labels are a WhatsApp
// Business feature and, like other per-account state, may only be changed by
// the login's own Matrix user.
func (wa *WhatsAppClient) HandleLabel(ctx context.Context, portal *bridgev2.Portal, evt *event.Event) (retErr error) {
	if wa.UserLogin == nil || evt.Sender != wa.UserLogin.UserMXID {
		zerolog.Ctx(ctx).Debug().
			Stringer("sender", evt.Sender).
			Msg("Ignoring label event from another user")
		return nil
	}
	content, err := getLabelContent(evt)
	if err != nil {
		return err
	} else if content.Label == "" {
		zerolog.Ctx(ctx).Debug().Msg("Ignoring label event without label name")
		return nil
	}
	chatJID, err := waid.ParsePortalID(portal.ID)
	if err != nil {
		return err
	}
	if !wa.isBusinessAccount() {
		zerolog.Ctx(ctx).Debug().Msg("Ignoring label event: account is not WhatsApp Business")
		return nil
	}
	meta, ok := wa.UserLogin.Metadata.(*waid.UserLoginMetadata)
	if !ok {
		return fmt.Errorf("unexpected user login metadata type %T", wa.UserLogin.Metadata)
	}
	wa.labelLock.Lock()
	if meta.Labels == nil {
		meta.Labels = make(map[string]waid.WALabel)
	}
	patches, created := labelToAppState(meta.Labels, chatJID, content.Label, content.Applied, uuid.NewString)
	if created {
		err = wa.UserLogin.Save(ctx)
	}
	wa.labelLock.Unlock()
	if err != nil {
		return fmt.Errorf("failed to save label metadata: %w", err)
	} else if len(patches) == 0 {
		return nil
	}
	defer wa.mcTrack(evt, time.Now(), &retErr)
	for _, patch := range patches {
		if err = wa.Client.SendAppState(ctx, patch); err != nil {
			return wrapWhatsAppError(err)
		}
	}
	return nil
}
