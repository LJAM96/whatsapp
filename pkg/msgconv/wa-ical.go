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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

// RFC 5545 section 3.1 says content lines SHOULD be folded at 75 octets.
const icalFoldOctets = 75

const icalTimeFormat = "20060102T150405Z"

// escapeICalText escapes a TEXT property value as defined in RFC 5545 section 3.3.11.
func escapeICalText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	var out strings.Builder
	for _, r := range value {
		switch r {
		case '\\':
			out.WriteString(`\\`)
		case ';':
			out.WriteString(`\;`)
		case ',':
			out.WriteString(`\,`)
		case '\n':
			out.WriteString(`\n`)
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// foldICalLine folds a single content line to at most 75 octets per line,
// with continuation lines starting with a space. UTF-8 sequences are never split.
func foldICalLine(line string) string {
	if len(line) <= icalFoldOctets {
		return line + "\r\n"
	}
	var out strings.Builder
	pos := 0
	limit := icalFoldOctets
	for pos < len(line) {
		end := pos + limit
		if end >= len(line) {
			end = len(line)
		} else {
			for end > pos && !utf8.RuneStart(line[end]) {
				end--
			}
		}
		out.WriteString(line[pos:end])
		out.WriteString("\r\n")
		pos = end
		limit = icalFoldOctets - 1
		if pos < len(line) {
			out.WriteByte(' ')
		}
	}
	return out.String()
}

// makeEventICal builds an iCalendar file for a WhatsApp event message.
// uid should be stable for the event (the WhatsApp message ID is used);
// stamp is used for DTSTAMP.
func makeEventICal(uid string, msg *waE2E.EventMessage, stamp time.Time) string {
	name := msg.GetName()
	if name == "" {
		name = "Event"
	}
	lines := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//mautrix//whatsapp//EN",
		"CALSCALE:GREGORIAN",
		"METHOD:PUBLISH",
		"BEGIN:VEVENT",
		"UID:" + escapeICalText(uid),
		"DTSTAMP:" + stamp.UTC().Format(icalTimeFormat),
	}
	if msg.StartTime != nil && msg.GetStartTime() != 0 {
		lines = append(lines, "DTSTART:"+time.Unix(msg.GetStartTime(), 0).UTC().Format(icalTimeFormat))
	}
	if msg.EndTime != nil && msg.GetEndTime() != 0 {
		lines = append(lines, "DTEND:"+time.Unix(msg.GetEndTime(), 0).UTC().Format(icalTimeFormat))
	}
	lines = append(lines, "SUMMARY:"+escapeICalText(name))
	if description := msg.GetDescription(); description != "" {
		lines = append(lines, "DESCRIPTION:"+escapeICalText(description))
	}
	if location := msg.GetLocation().GetName(); location != "" {
		lines = append(lines, "LOCATION:"+escapeICalText(location))
	}
	if joinLink := msg.GetJoinLink(); joinLink != "" {
		lines = append(lines, "URL:"+joinLink)
	}
	if msg.GetIsCanceled() {
		lines = append(lines, "STATUS:CANCELLED")
	}
	lines = append(lines, "END:VEVENT", "END:VCALENDAR")
	var out strings.Builder
	for _, line := range lines {
		out.WriteString(foldICalLine(line))
	}
	return out.String()
}

func icalFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return '_'
		case strings.ContainsRune("/\\?%*:|\"<>`' ", r):
			return '_'
		default:
			return r
		}
	}, name)
	if name == "" {
		return "event.ics"
	}
	return name + ".ics"
}

// eventICalPart uploads a generated .ics file and returns it as a file part.
// It returns nil if the upload fails, so the event text is still bridged.
func (mc *MessageConverter) eventICalPart(ctx context.Context, portal *bridgev2.Portal, info *types.MessageInfo, msg *waE2E.EventMessage) *bridgev2.ConvertedMessagePart {
	uid := string(info.ID)
	if uid == "" {
		uid = "unknown"
	}
	data := makeEventICal(uid+"@whatsapp", msg, info.Timestamp)
	fileName := icalFileName(msg.GetName())
	url, file, err := getIntent(ctx).UploadMedia(ctx, portal.MXID, []byte(data), fileName, "text/calendar")
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Str("event_id", string(info.ID)).Msg("Failed to upload iCal file for event message")
		return nil
	}
	return &bridgev2.ConvertedMessagePart{
		ID:   networkid.PartID("ical"),
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			MsgType:  event.MsgFile,
			Body:     fileName,
			FileName: fileName,
			URL:      url,
			File:     file,
			Info: &event.FileInfo{
				MimeType: "text/calendar",
				Size:     len(data),
			},
		},
	}
}
