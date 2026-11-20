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
	"reflect"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/event"
)

func TestPresenceSharingAllowed(t *testing.T) {
	tests := []struct {
		name     string
		settings types.PrivacySettings
		expected bool
	}{
		{
			name:     "empty settings",
			settings: types.PrivacySettings{},
			expected: false,
		},
		{
			name: "last seen hidden",
			settings: types.PrivacySettings{
				LastSeen: types.PrivacySettingNone,
				Online:   types.PrivacySettingMatchLastSeen,
			},
			expected: false,
		},
		{
			name: "last seen everyone, online matching last seen",
			settings: types.PrivacySettings{
				LastSeen: types.PrivacySettingAll,
				Online:   types.PrivacySettingMatchLastSeen,
			},
			expected: true,
		},
		{
			name: "last seen contacts, online everyone",
			settings: types.PrivacySettings{
				LastSeen: types.PrivacySettingContacts,
				Online:   types.PrivacySettingAll,
			},
			expected: true,
		},
		{
			name: "last seen blacklist, online unset",
			settings: types.PrivacySettings{
				LastSeen: types.PrivacySettingContactBlacklist,
			},
			expected: true,
		},
		{
			name: "unknown last seen value",
			settings: types.PrivacySettings{
				LastSeen: types.PrivacySettingKnown,
				Online:   types.PrivacySettingAll,
			},
			expected: false,
		},
		{
			name: "unknown online value",
			settings: types.PrivacySettings{
				LastSeen: types.PrivacySettingAll,
				Online:   types.PrivacySettingNone,
			},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if actual := presenceSharingAllowed(tt.settings); actual != tt.expected {
				t.Errorf("presenceSharingAllowed(%+v) = %v, want %v", tt.settings, actual, tt.expected)
			}
		})
	}
}

func TestPresenceToMatrix(t *testing.T) {
	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		evt      events.Presence
		expected *event.PresenceEventContent
	}{
		{
			name: "available",
			evt:  events.Presence{Unavailable: false},
			expected: &event.PresenceEventContent{
				Presence:        event.PresenceOnline,
				CurrentlyActive: true,
			},
		},
		{
			name: "unavailable with last seen",
			evt: events.Presence{
				Unavailable: true,
				LastSeen:    now.Add(-5 * time.Minute),
			},
			expected: &event.PresenceEventContent{
				Presence:      event.PresenceOffline,
				LastActiveAgo: 5 * 60 * 1000,
			},
		},
		{
			name: "unavailable with hidden last seen",
			evt: events.Presence{
				Unavailable: true,
			},
			expected: &event.PresenceEventContent{
				Presence: event.PresenceUnavailable,
			},
		},
		{
			name: "unavailable with future last seen",
			evt: events.Presence{
				Unavailable: true,
				LastSeen:    now.Add(time.Minute),
			},
			expected: &event.PresenceEventContent{
				Presence: event.PresenceOffline,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := presenceToMatrix(&tt.evt, now)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("presenceToMatrix(%+v) = %+v, want %+v", tt.evt, actual, tt.expected)
			}
		})
	}
}

func TestIsPresenceContact(t *testing.T) {
	tests := []struct {
		name     string
		jid      types.JID
		expected bool
	}{
		{
			name:     "phone number DM",
			jid:      types.NewJID("1234567890", types.DefaultUserServer),
			expected: true,
		},
		{
			name:     "LID DM",
			jid:      types.NewJID("1234567890", types.HiddenUserServer),
			expected: true,
		},
		{
			name:     "phone number with device",
			jid:      types.JID{User: "1234567890", Device: 1, Server: types.DefaultUserServer},
			expected: true,
		},
		{
			name:     "group",
			jid:      types.NewJID("1234567890", types.GroupServer),
			expected: false,
		},
		{
			name:     "broadcast list",
			jid:      types.NewJID("1234567890", types.BroadcastServer),
			expected: false,
		},
		{
			name:     "newsletter",
			jid:      types.NewJID("1234567890", types.NewsletterServer),
			expected: false,
		},
		{
			name:     "empty JID",
			jid:      types.EmptyJID,
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if actual := isPresenceContact(tt.jid); actual != tt.expected {
				t.Errorf("isPresenceContact(%s) = %v, want %v", tt.jid, actual, tt.expected)
			}
		})
	}
}
