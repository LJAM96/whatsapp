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
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/event"
)

func TestIsVCardFile(t *testing.T) {
	tests := []struct {
		name     string
		content  *event.MessageEventContent
		expected bool
	}{
		{
			name:     "text/vcard mimetype",
			content:  &event.MessageEventContent{Info: &event.FileInfo{MimeType: "text/vcard"}},
			expected: true,
		},
		{
			name:     "text/x-vcard mimetype",
			content:  &event.MessageEventContent{Info: &event.FileInfo{MimeType: "text/x-vcard"}},
			expected: true,
		},
		{
			name:     "mimetype with parameters",
			content:  &event.MessageEventContent{Info: &event.FileInfo{MimeType: "text/vcard; charset=utf-8"}},
			expected: true,
		},
		{
			name:     "case insensitive mimetype",
			content:  &event.MessageEventContent{Info: &event.FileInfo{MimeType: "TEXT/VCARD"}},
			expected: true,
		},
		{
			name:     "vcf filename fallback",
			content:  &event.MessageEventContent{Info: &event.FileInfo{MimeType: "application/octet-stream"}, FileName: "Contacts.VCF"},
			expected: true,
		},
		{
			name:     "vcf body fallback",
			content:  &event.MessageEventContent{Body: "contacts.vcf"},
			expected: true,
		},
		{
			name:     "not a vcard",
			content:  &event.MessageEventContent{Info: &event.FileInfo{MimeType: "text/plain"}, FileName: "notes.txt"},
			expected: false,
		},
		{
			name:     "no info or name",
			content:  &event.MessageEventContent{},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if actual := isVCardFile(tt.content); actual != tt.expected {
				t.Errorf("isVCardFile() = %v, want %v", actual, tt.expected)
			}
		})
	}
}

func TestParseVCards(t *testing.T) {
	tests := []struct {
		name          string
		data          string
		wantOK        bool
		wantCount     int
		wantNames     []string
		wantRawPrefix string
	}{
		{
			name:          "single card CRLF",
			data:          "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:John Doe\r\nTEL:+123456789\r\nEND:VCARD\r\n",
			wantOK:        true,
			wantCount:     1,
			wantNames:     []string{"John Doe"},
			wantRawPrefix: "BEGIN:VCARD\r\nVERSION:3.0\r\n",
		},
		{
			name:      "single card LF",
			data:      "BEGIN:VCARD\nVERSION:3.0\nFN:Jane Roe\nEND:VCARD\n",
			wantOK:    true,
			wantCount: 1,
			wantNames: []string{"Jane Roe"},
		},
		{
			name:      "two cards",
			data:      "BEGIN:VCARD\r\nFN:John Doe\r\nEND:VCARD\r\nBEGIN:VCARD\r\nFN:Jane Roe\r\nEND:VCARD\r\n",
			wantOK:    true,
			wantCount: 2,
			wantNames: []string{"John Doe", "Jane Roe"},
		},
		{
			name:      "BOM and leading whitespace",
			data:      "\ufeff  BEGIN:VCARD\r\nFN:John Doe\r\nEND:VCARD\r\n",
			wantOK:    true,
			wantCount: 1,
			wantNames: []string{"John Doe"},
		},
		{
			name:      "folded FN line",
			data:      "BEGIN:VCARD\r\nFN:John \r\n Doe\r\nEND:VCARD\r\n",
			wantOK:    true,
			wantCount: 1,
			wantNames: []string{"John Doe"},
		},
		{
			name:      "FN with parameters and escapes",
			data:      "BEGIN:VCARD\r\nFN;CHARSET=UTF-8:John \\; Doe\\, Jr.\r\nEND:VCARD\r\n",
			wantOK:    true,
			wantCount: 1,
			wantNames: []string{"John ; Doe, Jr."},
		},
		{
			name:      "N fallback",
			data:      "BEGIN:VCARD\r\nN:Doe;John;Middle;Dr.;Jr.\r\nEND:VCARD\r\n",
			wantOK:    true,
			wantCount: 1,
			wantNames: []string{"John Doe"},
		},
		{
			name:      "no FN or N",
			data:      "BEGIN:VCARD\r\nTEL:+123456789\r\nEND:VCARD\r\n",
			wantOK:    true,
			wantCount: 1,
			wantNames: []string{""},
		},
		{
			name:      "missing END:VCARD",
			data:      "BEGIN:VCARD\r\nFN:John Doe\r\n",
			wantOK:    false,
			wantCount: 0,
		},
		{
			name:      "does not start with BEGIN:VCARD",
			data:      "junk\r\nBEGIN:VCARD\r\nFN:John Doe\r\nEND:VCARD\r\n",
			wantOK:    false,
			wantCount: 0,
		},
		{
			name:      "empty",
			data:      "",
			wantOK:    false,
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cards, ok := parseVCards([]byte(tt.data))
			if ok != tt.wantOK {
				t.Fatalf("parseVCards() ok = %v, want %v", ok, tt.wantOK)
			}
			if len(cards) != tt.wantCount {
				t.Fatalf("parseVCards() returned %d cards, want %d", len(cards), tt.wantCount)
			}
			if len(tt.wantNames) != len(cards) {
				t.Fatalf("test has %d expected names for %d cards", len(tt.wantNames), len(cards))
			}
			for i, card := range cards {
				if card.displayName != tt.wantNames[i] {
					t.Errorf("card %d displayName = %q, want %q", i, card.displayName, tt.wantNames[i])
				}
			}
			if tt.wantRawPrefix != "" && cards[0].raw != tt.wantRawPrefix+"FN:John Doe\r\nTEL:+123456789\r\nEND:VCARD" {
				t.Errorf("unexpected raw card: %q", cards[0].raw)
			}
		})
	}
}

func TestParseVCardsInvalidUTF8(t *testing.T) {
	valid := "BEGIN:VCARD\r\nFN:John Doe\r\nEND:VCARD\r\n"
	invalid := append([]byte(valid[:len(valid)-1]), 0xff)
	if cards, ok := parseVCards(invalid); ok || len(cards) != 0 {
		t.Errorf("parseVCards(invalid UTF-8) = (%v, %v), want (nil, false)", cards, ok)
	}
}

func TestConstructVCardMessage(t *testing.T) {
	singleCard := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:John Doe\r\nTEL:+123456789\r\nEND:VCARD\r\n"
	twoCards := singleCard + "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Jane Roe\r\nEND:VCARD\r\n"
	contextInfo := &waE2E.ContextInfo{Expiration: proto.Uint32(60)}

	tests := []struct {
		name         string
		data         string
		fileName     string
		wantOK       bool
		wantSingle   string
		wantContacts []string
		wantArray    string
	}{
		{
			name:       "single contact with FN",
			data:       singleCard,
			fileName:   "contacts.vcf",
			wantOK:     true,
			wantSingle: "John Doe",
		},
		{
			name:         "multiple contacts",
			data:         twoCards,
			fileName:     "contacts.vcf",
			wantOK:       true,
			wantContacts: []string{"John Doe", "Jane Roe"},
			wantArray:    "John Doe, Jane Roe",
		},
		{
			name:       "filename fallback",
			data:       "BEGIN:VCARD\r\nTEL:+123456789\r\nEND:VCARD\r\n",
			fileName:   "business-cards.vcf",
			wantOK:     true,
			wantSingle: "business-cards.vcf",
		},
		{
			name:     "invalid data",
			data:     "not a vcard",
			fileName: "contacts.vcf",
			wantOK:   false,
		},
		{
			name:     "too large",
			data:     strings.Repeat("a", maxVCardSize+1),
			fileName: "contacts.vcf",
			wantOK:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := constructVCardMessage([]byte(tt.data), tt.fileName, contextInfo)
			if ok != tt.wantOK {
				t.Fatalf("constructVCardMessage() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				if msg != nil {
					t.Errorf("constructVCardMessage() returned message on failure: %v", msg)
				}
				return
			}
			if tt.wantSingle != "" {
				if msg.GetContactMessage() == nil {
					t.Fatalf("expected ContactMessage, got %v", msg)
				}
				if msg.GetContactMessage().GetDisplayName() != tt.wantSingle {
					t.Errorf("displayName = %q, want %q", msg.GetContactMessage().GetDisplayName(), tt.wantSingle)
				}
				if msg.GetContactMessage().GetContextInfo() != contextInfo {
					t.Errorf("ContactMessage contextInfo was not set")
				}
				if !strings.HasPrefix(msg.GetContactMessage().GetVcard(), "BEGIN:VCARD\r\n") {
					t.Errorf("vcard field does not contain the original card: %q", msg.GetContactMessage().GetVcard())
				}
				if msg.GetContactsArrayMessage() != nil {
					t.Errorf("unexpected ContactsArrayMessage")
				}
				return
			}
			arr := msg.GetContactsArrayMessage()
			if arr == nil {
				t.Fatalf("expected ContactsArrayMessage, got %v", msg)
			}
			if len(arr.GetContacts()) != len(tt.wantContacts) {
				t.Fatalf("got %d contacts, want %d", len(arr.GetContacts()), len(tt.wantContacts))
			}
			for i, contact := range arr.GetContacts() {
				if contact.GetDisplayName() != tt.wantContacts[i] {
					t.Errorf("contact %d displayName = %q, want %q", i, contact.GetDisplayName(), tt.wantContacts[i])
				}
			}
			if arr.GetDisplayName() != tt.wantArray {
				t.Errorf("array displayName = %q, want %q", arr.GetDisplayName(), tt.wantArray)
			}
			if arr.GetContextInfo() != contextInfo {
				t.Errorf("ContactsArrayMessage contextInfo was not set")
			}
			if msg.GetContactMessage() != nil {
				t.Errorf("unexpected single ContactMessage")
			}
		})
	}
}
