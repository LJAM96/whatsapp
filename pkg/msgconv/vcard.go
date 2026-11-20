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
	"context"
	"mime"
	"strings"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/event"
)

// WhatsApp only accepts fairly small vCards in contact messages. Anything
// bigger is sent as a regular document instead.
const maxVCardSize = 256 * 1024

// isVCardFile reports whether a Matrix file event looks like a vCard.
func isVCardFile(content *event.MessageEventContent) bool {
	if content.Info != nil {
		mimeType, _, err := mime.ParseMediaType(content.Info.MimeType)
		if err == nil {
			switch strings.ToLower(mimeType) {
			case "text/vcard", "text/x-vcard":
				return true
			}
		}
	}
	return strings.HasSuffix(strings.ToLower(content.GetFileName()), ".vcf")
}

type parsedVCard struct {
	raw         string
	displayName string
}

// parseVCards splits vCard data into individual cards. The data must be valid
// UTF-8 and start with a BEGIN:VCARD line (an optional UTF-8 BOM and leading
// whitespace are ignored).
func parseVCards(data []byte) ([]parsedVCard, bool) {
	if !utf8.Valid(data) {
		return nil, false
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if !strings.HasPrefix(strings.ToUpper(text), "BEGIN:VCARD") {
		return nil, false
	}
	var cards []parsedVCard
	var current []string
	for _, rawLine := range splitVCardLines(text) {
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.EqualFold(line, "BEGIN:VCARD"):
			current = []string{line}
		case strings.EqualFold(line, "END:VCARD"):
			if len(current) > 0 {
				current = append(current, line)
				cards = append(cards, parsedVCard{
					raw:         strings.Join(current, "\r\n"),
					displayName: displayNameFromVCard(current),
				})
				current = nil
			}
		case len(current) > 0:
			current = append(current, line)
		}
	}
	if len(cards) == 0 {
		return nil, false
	}
	return cards, true
}

// splitVCardLines normalizes line endings and unfolds folded vCard lines.
func splitVCardLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if len(lines) > 0 && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			lines[len(lines)-1] += strings.TrimLeft(line, " \t")
		} else {
			lines = append(lines, line)
		}
	}
	return lines
}

// displayNameFromVCard returns the FN value, falling back to N.
func displayNameFromVCard(lines []string) string {
	for _, line := range lines {
		if name, value := vCardProperty(line); name == "FN" {
			if value := strings.TrimSpace(unescapeVCard(value)); value != "" {
				return value
			}
		}
	}
	for _, line := range lines {
		if name, value := vCardProperty(line); name == "N" {
			parts := strings.Split(unescapeVCard(value), ";")
			var first, last string
			if len(parts) > 0 {
				last = strings.TrimSpace(parts[0])
			}
			if len(parts) > 1 {
				first = strings.TrimSpace(parts[1])
			}
			if name := strings.TrimSpace(first + " " + last); name != "" {
				return name
			}
		}
	}
	return ""
}

// vCardProperty splits a vCard content line into a normalized property name
// and its value. Property parameters (e.g. FN;CHARSET=UTF-8) are stripped.
func vCardProperty(line string) (name, value string) {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return "", ""
	}
	name = line[:colon]
	if semi := strings.IndexByte(name, ';'); semi >= 0 {
		name = name[:semi]
	}
	return strings.ToUpper(strings.TrimSpace(name)), line[colon+1:]
}

// unescapeVCard decodes the backslash escapes used in vCard text values.
func unescapeVCard(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			i++
			switch value[i] {
			case 'n', 'N':
				out.WriteByte(' ')
			default:
				out.WriteByte(value[i])
			}
			continue
		}
		out.WriteByte(value[i])
	}
	return out.String()
}

// constructVCardMessage builds a native WhatsApp contact message from vCard
// data. The filename is used as a last-resort display name.
func constructVCardMessage(data []byte, fileName string, contextInfo *waE2E.ContextInfo) (*waE2E.Message, bool) {
	if len(data) > maxVCardSize {
		return nil, false
	}
	cards, ok := parseVCards(data)
	if !ok {
		return nil, false
	}
	contacts := make([]*waE2E.ContactMessage, 0, len(cards))
	names := make([]string, 0, len(cards))
	for _, card := range cards {
		name := card.displayName
		if name == "" {
			name = fileName
		}
		names = append(names, name)
		contacts = append(contacts, &waE2E.ContactMessage{
			DisplayName: proto.String(name),
			Vcard:       proto.String(card.raw),
		})
	}
	if len(contacts) == 1 {
		contacts[0].ContextInfo = contextInfo
		return &waE2E.Message{ContactMessage: contacts[0]}, true
	}
	return &waE2E.Message{
		ContactsArrayMessage: &waE2E.ContactsArrayMessage{
			DisplayName: proto.String(strings.Join(names, ", ")),
			Contacts:    contacts,
			ContextInfo: contextInfo,
		},
	}, true
}

// tryVCardMessage tries to convert a Matrix file event into a native WhatsApp
// contact message. It returns false when the file should be sent as a regular
// document instead.
func (mc *MessageConverter) tryVCardMessage(
	ctx context.Context,
	content *event.MessageEventContent,
	contextInfo *waE2E.ContextInfo,
) (*waE2E.Message, bool) {
	if content.MsgType != event.MsgFile || !isVCardFile(content) {
		return nil, false
	}
	if content.Info != nil && content.Info.Size > maxVCardSize {
		return nil, false
	}
	data, err := mc.Bridge.Bot.DownloadMedia(ctx, content.URL, content.File)
	if err != nil {
		zerolog.Ctx(ctx).Debug().Err(err).Msg("Failed to download vCard, sending as document")
		return nil, false
	}
	return constructVCardMessage(data, content.GetFileName(), contextInfo)
}
