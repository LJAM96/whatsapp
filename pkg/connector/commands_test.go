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

import "testing"

func TestInviteLinkResetRequested(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"no arguments", nil, false},
		{"empty argument", []string{""}, false},
		{"documented flag", []string{"--reset"}, true},
		{"plain reset argument", []string{"reset"}, true},
		{"uppercase flag", []string{"--RESET"}, true},
		{"mixed case argument", []string{"ReSeT"}, true},
		{"flag with more arguments", []string{"--reset", "extra"}, true},
		{"unrelated first argument", []string{"other", "--reset"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inviteLinkResetRequested(tt.args); got != tt.want {
				t.Errorf("inviteLinkResetRequested(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
