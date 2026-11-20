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
	"slices"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// AccountDataStarred is the room account data event type used to keep track of
// starred messages in a portal room.
//
// Matrix has no standard (or Beeper) account data type for per-event stars:
// m.tag only applies to rooms. This fi.mau.* key is used as a last resort.
var AccountDataStarred = event.Type{Type: "fi.mau.whatsapp.starred", Class: event.AccountDataEventType}

// MaxStarredEvents is the maximum number of event IDs kept in the starred list.
const MaxStarredEvents = 1000

// StarredEventContent is the content of the fi.mau.whatsapp.starred room account
// data event. Matrix account data content must be a JSON object, so the list of
// event IDs is stored in a field rather than as a bare array.
type StarredEventContent struct {
	EventIDs []id.EventID `json:"event_ids"`
}

// UpdateStarredEvents applies a star or unstar action for a Matrix event ID to
// the given content and reports whether anything changed. The returned content
// is always a copy; the input is never mutated.
//
// The list is kept in the order stars were added (oldest first). When adding a
// star would exceed MaxStarredEvents, the oldest entries are dropped.
func UpdateStarredEvents(current StarredEventContent, eventID id.EventID, starred bool) (StarredEventContent, bool) {
	if eventID == "" {
		return current, false
	}
	idx := slices.Index(current.EventIDs, eventID)
	if starred {
		if idx >= 0 {
			return current, false
		}
		updated := StarredEventContent{EventIDs: append(slices.Clone(current.EventIDs), eventID)}
		if len(updated.EventIDs) > MaxStarredEvents {
			updated.EventIDs = updated.EventIDs[len(updated.EventIDs)-MaxStarredEvents:]
		}
		return updated, true
	}
	if idx < 0 {
		return current, false
	}
	updated := StarredEventContent{
		EventIDs: append(slices.Clone(current.EventIDs[:idx]), current.EventIDs[idx+1:]...),
	}
	return updated, true
}
