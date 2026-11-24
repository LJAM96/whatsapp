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
	"errors"
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

// createdLabel describes the placeholder label an outgoing event invented.
// It stays local to the event until WhatsApp accepts the creation patch; it
// must never be put in the shared label map before that point.
type createdLabel struct {
	ID    string
	Label waid.WALabel
}

// labelToAppState builds the app-state patches for applying or removing a
// label on a chat. A missing label is created with BuildLabelEdit before the
// association is sent, but only when it's being applied: unapplying an unknown
// label would just create an empty label. labels is only read, never mutated:
// the caller adds the returned createdLabel to the shared map under labelLock
// after the creation patch succeeds.
func labelToAppState(labels map[string]waid.WALabel, chatJID types.JID, name string, applied bool, newLabelID func() string) (patches []appstate.PatchInfo, created *createdLabel) {
	labelID := findLabelID(labels, name)
	if labelID == "" {
		if !applied {
			return nil, nil
		}
		labelID = newLabelID()
		patches = append(patches, appstate.BuildLabelEdit(labelID, name, defaultLabelColor, false))
		created = &createdLabel{ID: labelID, Label: waid.WALabel{Name: name, Color: defaultLabelColor}}
	}
	return append(patches, appstate.BuildLabelChat(chatJID, labelID, applied)), created
}

// applyLabelEvent sends an outgoing label event from start to finish. The
// outbound lock is held across construction, sending and committing so two
// concurrent portals cannot both invent (or skip) the creation of the same
// label. labelLock is only taken for short map accesses, never across send:
// incoming app-state callbacks take it too and would deadlock otherwise.
func (wa *WhatsAppClient) applyLabelEvent(
	ctx context.Context,
	meta *waid.UserLoginMetadata,
	chatJID types.JID,
	content *LabelEventContent,
	newLabelID func() string,
	send func(context.Context, appstate.PatchInfo) error,
	save func(context.Context) error,
) error {
	wa.outboundLabelLock.Lock()
	defer wa.outboundLabelLock.Unlock()

	wa.labelLock.Lock()
	if meta.Labels == nil {
		meta.Labels = make(map[string]waid.WALabel)
	}
	patches, created := labelToAppState(meta.Labels, chatJID, content.Label, content.Applied, newLabelID)
	wa.labelLock.Unlock()
	if len(patches) == 0 {
		return nil
	}

	var saveErr error
	if created != nil {
		if err := send(ctx, patches[0]); err != nil {
			// Nothing was added to the shared map, so a retry will create the
			// label again instead of sending a dangling association.
			return wrapWhatsAppError(err)
		}
		patches = patches[1:]
		wa.labelLock.Lock()
		// An incoming echo may have stored confirmed metadata for this label
		// while the creation patch was in flight. Don't overwrite it with the
		// provisional colour, and don't add a second ID for the same name.
		if _, ok := meta.Labels[created.ID]; !ok && findLabelID(meta.Labels, created.Label.Name) == "" {
			meta.Labels[created.ID] = created.Label
		}
		err := save(ctx)
		wa.labelLock.Unlock()
		if err != nil {
			// The remote label now exists, so persist it before the
			// association and keep applying the association anyway.
			saveErr = fmt.Errorf("failed to save label metadata: %w", err)
		}
	}
	for _, patch := range patches {
		if err := send(ctx, patch); err != nil {
			if saveErr != nil {
				return errors.Join(saveErr, wrapWhatsAppError(err))
			}
			return wrapWhatsAppError(err)
		}
	}
	return saveErr
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
	defer wa.mcTrack(evt, time.Now(), &retErr)
	sendPatch := func(ctx context.Context, patch appstate.PatchInfo) error {
		return wa.Client.SendAppState(ctx, patch)
	}
	return wa.applyLabelEvent(ctx, meta, chatJID, content, uuid.NewString, sendPatch, wa.UserLogin.Save)
}
