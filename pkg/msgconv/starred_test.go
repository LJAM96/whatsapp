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
	"slices"
	"testing"

	"maunium.net/go/mautrix/id"
)

func TestUpdateStarredEvents(t *testing.T) {
	tests := []struct {
		name        string
		current     []id.EventID
		eventID     id.EventID
		starred     bool
		want        []id.EventID
		wantChanged bool
	}{
		{
			name:        "star empty list",
			eventID:     "$one",
			starred:     true,
			want:        []id.EventID{"$one"},
			wantChanged: true,
		},
		{
			name:        "star appends",
			current:     []id.EventID{"$one", "$two"},
			eventID:     "$three",
			starred:     true,
			want:        []id.EventID{"$one", "$two", "$three"},
			wantChanged: true,
		},
		{
			name:        "star already present is no-op",
			current:     []id.EventID{"$one", "$two"},
			eventID:     "$two",
			starred:     true,
			want:        []id.EventID{"$one", "$two"},
			wantChanged: false,
		},
		{
			name:        "unstar removes",
			current:     []id.EventID{"$one", "$two", "$three"},
			eventID:     "$two",
			starred:     false,
			want:        []id.EventID{"$one", "$three"},
			wantChanged: true,
		},
		{
			name:        "unstar first",
			current:     []id.EventID{"$one", "$two"},
			eventID:     "$one",
			starred:     false,
			want:        []id.EventID{"$two"},
			wantChanged: true,
		},
		{
			name:        "unstar last",
			current:     []id.EventID{"$one", "$two"},
			eventID:     "$two",
			starred:     false,
			want:        []id.EventID{"$one"},
			wantChanged: true,
		},
		{
			name:        "unstar absent is no-op",
			current:     []id.EventID{"$one"},
			eventID:     "$two",
			starred:     false,
			want:        []id.EventID{"$one"},
			wantChanged: false,
		},
		{
			name:        "empty event ID star is no-op",
			current:     []id.EventID{"$one"},
			eventID:     "",
			starred:     true,
			want:        []id.EventID{"$one"},
			wantChanged: false,
		},
		{
			name:        "empty event ID unstar is no-op",
			current:     []id.EventID{"$one"},
			eventID:     "",
			starred:     false,
			want:        []id.EventID{"$one"},
			wantChanged: false,
		},
		{
			name:        "unstar only entry gives empty list",
			current:     []id.EventID{"$one"},
			eventID:     "$one",
			starred:     false,
			want:        []id.EventID{},
			wantChanged: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := UpdateStarredEvents(StarredEventContent{EventIDs: tt.current}, tt.eventID, tt.starred)
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if !slices.Equal(got.EventIDs, tt.want) {
				t.Errorf("event IDs = %v, want %v", got.EventIDs, tt.want)
			}
		})
	}
}

func TestUpdateStarredEventsDoesNotMutateInput(t *testing.T) {
	original := []id.EventID{"$one", "$two"}
	_, _ = UpdateStarredEvents(StarredEventContent{EventIDs: original}, "$three", true)
	_, _ = UpdateStarredEvents(StarredEventContent{EventIDs: original}, "$one", false)
	if !slices.Equal(original, []id.EventID{"$one", "$two"}) {
		t.Errorf("input was mutated: %v", original)
	}
}

func TestUpdateStarredEventsBound(t *testing.T) {
	current := make([]id.EventID, 0, MaxStarredEvents)
	for i := range MaxStarredEvents {
		current = append(current, id.EventID(fmt.Sprintf("$old%d", i)))
	}
	updated, changed := UpdateStarredEvents(StarredEventContent{EventIDs: current}, "$new", true)
	if !changed {
		t.Fatal("expected content to change")
	}
	if len(updated.EventIDs) != MaxStarredEvents {
		t.Fatalf("got %d event IDs, want %d", len(updated.EventIDs), MaxStarredEvents)
	}
	if updated.EventIDs[0] != "$old1" || updated.EventIDs[len(updated.EventIDs)-1] != "$new" {
		t.Errorf("expected oldest entry to be evicted and new entry appended, got first %q and last %q",
			updated.EventIDs[0], updated.EventIDs[len(updated.EventIDs)-1])
	}
}
