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

// vcardLine is a logical (unfolded) line with offsets into the original text.
// The offsets let callers send the original bytes unchanged instead of the
// unfolded form, which is only used for validation and name extraction.
type vcardLine struct {
	text      string
	start     int
	nextStart int
}

// splitVCardLines splits text into logical lines. Line endings are normalized
// for parsing, and folded lines are unfolded per RFC 6350 section 3.2: exactly
// one leading space or tab is removed from a continuation line before it is
// appended to the previous line. The offsets refer to the original text so
// callers can send that text byte-for-byte.
func splitVCardLines(text string) []vcardLine {
	var lines []vcardLine
	for start := 0; start < len(text); {
		end := start
		for end < len(text) && text[end] != '\r' && text[end] != '\n' {
			end++
		}
		content := text[start:end]
		nextStart := end
		if end < len(text) {
			if text[end] == '\r' && end+1 < len(text) && text[end+1] == '\n' {
				nextStart = end + 2
			} else {
				nextStart = end + 1
			}
		}
		if len(lines) > 0 && len(content) > 0 && (content[0] == ' ' || content[0] == '\t') {
			last := &lines[len(lines)-1]
			last.text += content[1:]
			last.nextStart = nextStart
		} else {
			lines = append(lines, vcardLine{text: content, start: start, nextStart: nextStart})
		}
		start = nextStart
	}
	return lines
}

// parseVCards splits vCard data into individual cards. The file must be valid
// UTF-8 and consist only of a sequence of complete cards (an optional UTF-8
// BOM and any whitespace outside cards are ignored). Any malformed structure -
// a nested BEGIN, an END without a BEGIN, an unclosed card, or non-whitespace
// content outside a card - makes the whole file fail to parse so the original
// file is sent as a document instead of a subset of cards.
func parseVCards(data []byte) ([]parsedVCard, bool) {
	if !utf8.Valid(data) {
		return nil, false
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	var cards []parsedVCard
	var current []string
	var cardStart int
	open := false
	for _, line := range splitVCardLines(text) {
		trimmed := strings.TrimSpace(line.text)
		switch {
		case strings.EqualFold(trimmed, "BEGIN:VCARD"):
			if open {
				return nil, false
			}
			open = true
			cardStart = line.start
			current = []string{trimmed}
		case strings.EqualFold(trimmed, "END:VCARD"):
			if !open {
				return nil, false
			}
			open = false
			current = append(current, trimmed)
			cards = append(cards, parsedVCard{
				raw:         text[cardStart:line.nextStart],
				displayName: displayNameFromVCard(current),
			})
			current = nil
		case open:
			current = append(current, trimmed)
		case trimmed != "":
			return nil, false
		}
	}
	if open || len(cards) == 0 {
		return nil, false
	}
	return cards, true
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
// data. Each card is sent byte-for-byte as it appeared in the input (with only
// a leading BOM removed); unfolding is used solely for parsing. The filename
// is used as a last-resort display name.
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
