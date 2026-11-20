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
	"time"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

func TestEscapeICalText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "hello world", "hello world"},
		{"backslash", `a\b`, `a\\b`},
		{"semicolon", "a;b", `a\;b`},
		{"comma", "a,b", `a\,b`},
		{"newline", "a\nb", `a\nb`},
		{"crlf", "a\r\nb", `a\nb`},
		{"lone cr", "a\rb", `a\nb`},
		{"all", `a\;b,c\nd`, `a\\\;b\,c\\nd`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeICalText(tt.input); got != tt.want {
				t.Errorf("escapeICalText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFoldICalLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "short",
			input: "SUMMARY:hi",
			want:  "SUMMARY:hi\r\n",
		},
		{
			name:  "exactly 75",
			input: strings.Repeat("a", 75),
			want:  strings.Repeat("a", 75) + "\r\n",
		},
		{
			name:  "80 octets",
			input: strings.Repeat("a", 80),
			want:  strings.Repeat("a", 75) + "\r\n " + strings.Repeat("a", 5) + "\r\n",
		},
		{
			name:  "does not split UTF-8",
			input: strings.Repeat("a", 74) + "世",
			want:  strings.Repeat("a", 74) + "\r\n 世\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := foldICalLine(tt.input)
			if got != tt.want {
				t.Errorf("foldICalLine() = %q, want %q", got, tt.want)
			}
			for _, line := range strings.Split(strings.TrimSuffix(got, "\r\n"), "\r\n") {
				if len(line) > icalFoldOctets {
					t.Errorf("folded line is %d octets, want <= %d: %q", len(line), icalFoldOctets, line)
				}
			}
		})
	}
}

func TestMakeEventICal(t *testing.T) {
	stamp := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	start := stamp
	end := start.Add(time.Hour)
	tests := []struct {
		name  string
		msg   *waE2E.EventMessage
		uid   string
		stamp time.Time
		want  string
	}{
		{
			name:  "full event",
			uid:   "msg123@whatsapp",
			stamp: stamp,
			msg: &waE2E.EventMessage{
				Name:        ptr.Ptr("Team Meeting"),
				Description: ptr.Ptr("Line 1\nLine 2, with; chars"),
				Location:    &waE2E.LocationMessage{Name: ptr.Ptr("Room 1")},
				JoinLink:    ptr.Ptr("https://example.com/join"),
				StartTime:   ptr.Ptr(start.Unix()),
				EndTime:     ptr.Ptr(end.Unix()),
			},
			want: strings.Join([]string{
				"BEGIN:VCALENDAR",
				"VERSION:2.0",
				"PRODID:-//mautrix//whatsapp//EN",
				"CALSCALE:GREGORIAN",
				"METHOD:PUBLISH",
				"BEGIN:VEVENT",
				"UID:msg123@whatsapp",
				"DTSTAMP:20260102T030405Z",
				"DTSTART:20260102T030405Z",
				"DTEND:20260102T040405Z",
				"SUMMARY:Team Meeting",
				`DESCRIPTION:Line 1\nLine 2\, with\; chars`,
				"LOCATION:Room 1",
				"URL:https://example.com/join",
				"END:VEVENT",
				"END:VCALENDAR",
				"",
			}, "\r\n"),
		},
		{
			name:  "minimal canceled event",
			uid:   "abc@whatsapp",
			stamp: stamp,
			msg: &waE2E.EventMessage{
				IsCanceled: ptr.Ptr(true),
			},
			want: strings.Join([]string{
				"BEGIN:VCALENDAR",
				"VERSION:2.0",
				"PRODID:-//mautrix//whatsapp//EN",
				"CALSCALE:GREGORIAN",
				"METHOD:PUBLISH",
				"BEGIN:VEVENT",
				"UID:abc@whatsapp",
				"DTSTAMP:20260102T030405Z",
				"SUMMARY:Event",
				"STATUS:CANCELLED",
				"END:VEVENT",
				"END:VCALENDAR",
				"",
			}, "\r\n"),
		},
		{
			name:  "zero timestamps are omitted",
			uid:   "zero@whatsapp",
			stamp: stamp,
			msg: &waE2E.EventMessage{
				Name:      ptr.Ptr("No time"),
				StartTime: ptr.Ptr(int64(0)),
				EndTime:   ptr.Ptr(int64(0)),
			},
			want: strings.Join([]string{
				"BEGIN:VCALENDAR",
				"VERSION:2.0",
				"PRODID:-//mautrix//whatsapp//EN",
				"CALSCALE:GREGORIAN",
				"METHOD:PUBLISH",
				"BEGIN:VEVENT",
				"UID:zero@whatsapp",
				"DTSTAMP:20260102T030405Z",
				"SUMMARY:No time",
				"END:VEVENT",
				"END:VCALENDAR",
				"",
			}, "\r\n"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeEventICal(tt.uid, tt.msg, tt.stamp)
			if got != tt.want {
				t.Errorf("makeEventICal() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestICalFileName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", "event.ics"},
		{"simple", "Team Meeting", "Team_Meeting.ics"},
		{"path separators", "../secret", ".._secret.ics"},
		{"quotes", `a"b`, "a_b.ics"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := icalFileName(tt.input); got != tt.want {
				t.Errorf("icalFileName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
