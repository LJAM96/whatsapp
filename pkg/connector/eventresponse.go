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
	"fmt"
	"time"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"go.mau.fi/mautrix-whatsapp/pkg/msgconv"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

// decryptEventResponse decrypts the payload of an EncEventResponseMessage using
// whatsmeow's "Event Response" message secret (the same primitive the library
// exposes for poll votes, reactions and comments).
func decryptEventResponse(ctx context.Context, cli *whatsmeow.Client, evt *events.Message) (*waE2E.EventResponseMessage, *waCommon.MessageKey, error) {
	enc := evt.Message.GetEncEventResponseMessage()
	if enc == nil {
		return nil, nil, fmt.Errorf("message has no encrypted event response")
	}
	plaintext, err := cli.DangerousInternals().DecryptMsgSecret(ctx, evt, whatsmeow.EncSecretEventResponse, enc, enc.GetEventCreationMessageKey())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decrypt event response: %w", err)
	}
	var response waE2E.EventResponseMessage
	if err = proto.Unmarshal(plaintext, &response); err != nil {
		return nil, nil, fmt.Errorf("failed to decode event response protobuf: %w", err)
	}
	return &response, enc.GetEventCreationMessageKey(), nil
}

// buildEventResponseMessage builds an encrypted RSVP for the given event
// message. The result can be sent with Client.SendMessage.
func buildEventResponseMessage(
	ctx context.Context,
	wa *WhatsAppClient,
	target *waid.ParsedMessageID,
	response waE2E.EventResponseMessage_EventResponseType,
	timestamp time.Time,
) (*waE2E.Message, error) {
	plaintext, err := proto.Marshal(&waE2E.EventResponseMessage{
		Response:    ptr.Ptr(response),
		TimestampMS: ptr.Ptr(timestamp.UnixMilli()),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event response protobuf: %w", err)
	}
	info := msgconv.MessageIDToInfo(ctx, wa.Client, target)
	ownID := wa.GetLID()
	if target.Sender.Server == types.DefaultUserServer {
		ownID = wa.JID
	}
	ciphertext, iv, err := wa.Client.DangerousInternals().EncryptMsgSecret(
		ctx, ownID, info.Chat, info.Sender, info.ID, whatsmeow.EncSecretEventResponse, plaintext,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt event response: %w", err)
	}
	return &waE2E.Message{
		EncEventResponseMessage: &waE2E.EncEventResponseMessage{
			EventCreationMessageKey: wa.messageIDToKey(target),
			EncPayload:              ciphertext,
			EncIV:                   iv,
		},
	}, nil
}
