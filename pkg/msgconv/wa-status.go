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
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// StatusMetadataKey is the event content key with metadata for bridged status posts.
const StatusMetadataKey = "fi.mau.whatsapp.status"

// StatusLifetime is how long WhatsApp keeps a contact's status post before it disappears.
const StatusLifetime = 24 * time.Hour

// StatusMetadata is stored under StatusMetadataKey on bridged status posts.
type StatusMetadata struct {
	// Sender is the MXID of the ghost that posted the status.
	Sender id.UserID `json:"sender"`
	// ExpiresAt is the Unix timestamp in milliseconds at which the post expires.
	ExpiresAt int64 `json:"expiresAt"`
}

// IsStatusMessage reports whether info describes an incoming status@broadcast post.
// whatsmeow delivers every contact's status to the single status@broadcast chat
// (types.StatusBroadcastJID), so all posts land in one portal.
func IsStatusMessage(info *types.MessageInfo) bool {
	return info != nil && info.Chat == types.StatusBroadcastJID && !info.IsFromMe
}

// argbToHex converts WhatsApp's ARGB uint32 colour to the #RRGGBB form used by
// the Matrix data-mx-bg-color convention. The alpha channel is discarded.
func argbToHex(argb uint32) string {
	return fmt.Sprintf("#%06X", argb&0x00FFFFFF)
}

// applyStatusBackground wraps a text status' formatted body in a span carrying
// the background colour. Matrix clients render data-mx-bg-color, advertised as
// event.FmtTextBackgroundColor. The plain-text body is left untouched.
func applyStatusBackground(content *event.MessageEventContent, argb uint32) {
	if content == nil || argb == 0 {
		return
	}
	if content.FormattedBody == "" {
		content.FormattedBody = event.TextToHTML(content.Body)
	}
	content.FormattedBody = fmt.Sprintf(`<span data-mx-bg-color="%s">%s</span>`, argbToHex(argb), content.FormattedBody)
	content.Format = event.FormatHTML
}

// applyStatus adds status-specific data to a converted status post: the
// fi.mau.whatsapp.status metadata, a background colour on text posts, and a
// disappearing timer so bridgev2 redacts the post after StatusLifetime.
//
// bridgev2's disappearing loop is a bounded periodic sweep: it checks once an
// hour and handles at most 200 messages per pass (bridgev2/disappear.go).
func (mc *MessageConverter) applyStatus(cm *bridgev2.ConvertedMessage, msg *waE2E.Message, info *types.MessageInfo, intent bridgev2.MatrixAPI) {
	if !mc.BridgeStatus || !IsStatusMessage(info) || len(cm.Parts) == 0 {
		return
	}
	part := cm.Parts[0]
	if part.Content == nil {
		return
	}
	if part.Extra == nil {
		part.Extra = make(map[string]any)
	}
	part.Extra[StatusMetadataKey] = StatusMetadata{
		Sender:    intent.GetMXID(),
		ExpiresAt: info.Timestamp.Add(StatusLifetime).UnixMilli(),
	}
	if bg := msg.GetExtendedTextMessage().GetBackgroundArgb(); bg != 0 {
		applyStatusBackground(part.Content, bg)
	}
	cm.Disappear = database.DisappearingSetting{
		Type:  event.DisappearingTypeAfterSend,
		Timer: StatusLifetime,
	}
}
