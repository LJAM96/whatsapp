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
	"go.mau.fi/whatsmeow/types"
)

// skipStatusReceipt reports whether a Matrix read receipt should be dropped
// instead of being forwarded to WhatsApp as a "seen" receipt for a status.
//
// bridgev2 generates implicit receipts for incoming timeline events and for
// outgoing messages (MatrixReadReceipt.Implicit). Those must not leak when a
// contact's status was viewed. Only an explicit "mark as read" in the status
// room - which arrives with Implicit=false - is forwarded to WhatsApp.
func skipStatusReceipt(portalJID types.JID, implicit bool) bool {
	return portalJID == types.StatusBroadcastJID && implicit
}
