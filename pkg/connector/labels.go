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
	"slices"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

const labelTagPrefix = "u.wa."

// labelRoomTag converts a WhatsApp business label name into the user-defined Matrix room tag
// used to represent it. u.* tags are user-defined in Matrix (event.RoomTag.IsUserDefined).
func labelRoomTag(name string) event.RoomTag {
	return event.RoomTag(labelTagPrefix + name)
}

type labelTagChange struct {
	Chat types.JID
	Tag  event.RoomTag
	Add  bool
}

// updateLabel applies a LabelEdit action to the tracked label state and returns the room tag
// changes that are needed. WhatsApp may send chat associations before the LabelEdit that gives
// the label its name, so a previously unnamed label is applied to all of its known chats here.
func updateLabel(labels map[string]waid.WALabel, id, name string, color int32, deleted bool) []labelTagChange {
	old := labels[id]
	var changes []labelTagChange
	switch {
	case deleted:
		if old.Name != "" {
			for _, chat := range old.Chats {
				changes = append(changes, labelTagChange{Chat: chat, Tag: labelRoomTag(old.Name)})
			}
		}
		delete(labels, id)
	case old.Name == "":
		labels[id] = waid.WALabel{Name: name, Color: color, Chats: old.Chats}
		for _, chat := range old.Chats {
			changes = append(changes, labelTagChange{Chat: chat, Tag: labelRoomTag(name), Add: true})
		}
	case old.Name != name:
		for _, chat := range old.Chats {
			changes = append(changes, labelTagChange{Chat: chat, Tag: labelRoomTag(old.Name)})
			changes = append(changes, labelTagChange{Chat: chat, Tag: labelRoomTag(name), Add: true})
		}
		labels[id] = waid.WALabel{Name: name, Color: color, Chats: old.Chats}
	default:
		labels[id] = waid.WALabel{Name: name, Color: color, Chats: old.Chats}
	}
	return changes
}

// updateLabelChat applies a LabelAssociationChat action to the tracked label state. The tag and
// true are only returned once the label's name is known (the caller decides add vs remove from
// the action); label_edit may arrive after the association, in which case updateLabel applies
// the tag retroactively.
func updateLabelChat(labels map[string]waid.WALabel, id string, chat types.JID, labeled bool) (event.RoomTag, bool) {
	label := labels[id]
	if labeled {
		if !slices.Contains(label.Chats, chat) {
			label.Chats = append(label.Chats, chat)
		}
	} else {
		label.Chats = slices.DeleteFunc(label.Chats, func(existing types.JID) bool {
			return existing == chat
		})
	}
	labels[id] = label
	if label.Name == "" {
		return "", false
	}
	return labelRoomTag(label.Name), true
}

func (wa *WhatsAppClient) handleWALabelEdit(ctx context.Context, evt *events.LabelEdit) bool {
	if evt.Action == nil {
		return true
	}
	wa.labelLock.Lock()
	meta := wa.UserLogin.Metadata.(*waid.UserLoginMetadata)
	if meta.Labels == nil {
		meta.Labels = make(map[string]waid.WALabel)
	}
	changes := updateLabel(meta.Labels, evt.LabelID, evt.Action.GetName(), evt.Action.GetColor(), evt.Action.GetDeleted())
	err := wa.UserLogin.Save(ctx)
	wa.labelLock.Unlock()
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to save label metadata")
	}
	for _, change := range changes {
		wa.setLabelPortalTag(ctx, change.Chat, change.Tag, change.Add)
	}
	return true
}

func (wa *WhatsAppClient) handleWALabelAssociationChat(ctx context.Context, evt *events.LabelAssociationChat) bool {
	if evt.Action == nil {
		return true
	}
	wa.labelLock.Lock()
	meta := wa.UserLogin.Metadata.(*waid.UserLoginMetadata)
	if meta.Labels == nil {
		meta.Labels = make(map[string]waid.WALabel)
	}
	tag, ok := updateLabelChat(meta.Labels, evt.LabelID, evt.JID, evt.Action.GetLabeled())
	err := wa.UserLogin.Save(ctx)
	wa.labelLock.Unlock()
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to save label metadata")
	}
	if ok {
		wa.setLabelPortalTag(ctx, evt.JID, tag, evt.Action.GetLabeled())
	}
	return true
}

// setLabelPortalTag applies a label tag to a portal using the user's double puppet, the same
// mechanism used for pinned/archive tags and mute sync in bridgev2.Portal.updateUserLocalInfo.
func (wa *WhatsAppClient) setLabelPortalTag(ctx context.Context, chatJID types.JID, tag event.RoomTag, isTagged bool) {
	portal, err := wa.UserLogin.Bridge.GetExistingPortalByKey(ctx, wa.makeWAPortalKey(chatJID))
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Stringer("chat_jid", chatJID).Msg("Failed to get portal for label tag")
		return
	} else if portal == nil || portal.MXID == "" {
		return
	}
	dp := wa.UserLogin.User.DoublePuppet(ctx)
	if dp == nil {
		return
	}
	if err = dp.TagRoom(ctx, portal.MXID, tag, isTagged); err != nil {
		zerolog.Ctx(ctx).Err(err).
			Stringer("chat_jid", chatJID).
			Str("tag", string(tag)).
			Msg("Failed to set label tag on portal")
	}
}
