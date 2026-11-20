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
	"context"
	"time"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

var _ bridgev2.PowerLevelHandlingNetworkAPI = (*WhatsAppClient)(nil)

// groupAdminChanges are the WhatsApp group changes implied by a Matrix
// power-level event. Everything is expressed in terms of the levels used by
// wrapGroupInfo so the two directions use the same thresholds.
type groupAdminChanges struct {
	Promote  []networkid.UserID
	Demote   []networkid.UserID
	Announce *bool
	Locked   *bool
}

// targetUserID extracts the WhatsApp user ID of a power-level change target.
// bridgev2 resolves Matrix user IDs to ghosts (or to the local login) before
// calling HandleMatrixPowerLevels.
func targetUserID(target bridgev2.GhostOrUserLogin) (networkid.UserID, bool) {
	switch typed := target.(type) {
	case *bridgev2.Ghost:
		return typed.ID, true
	case *bridgev2.UserLogin:
		return networkid.UserID(typed.ID), true
	default:
		return "", false
	}
}

// adminPLAction returns the WhatsApp participant action needed when a user
// moves between normal member and admin. WhatsApp has no levels between the
// two, so changes that don't cross adminPL are ignored.
func adminPLAction(oldLevel, newLevel int) whatsmeow.ParticipantChange {
	wasAdmin := oldLevel >= adminPL
	isAdmin := newLevel >= adminPL
	switch {
	case isAdmin && !wasAdmin:
		return whatsmeow.ParticipantChangePromote
	case wasAdmin && !isAdmin:
		return whatsmeow.ParticipantChangeDemote
	default:
		return ""
	}
}

// settingFromPLChange interprets a power-level change for a binary group
// setting: a level at or above adminPL means the restriction is enabled.
// Changes that don't cross the threshold (or don't exist) produce nil.
func settingFromPLChange(change *bridgev2.SinglePowerLevelChange) *bool {
	if change == nil || (change.OrigLevel >= adminPL) == (change.NewLevel >= adminPL) {
		return nil
	}
	return ptr.Ptr(change.NewLevel >= adminPL)
}

// classifyGroupAdminChanges converts the diff of a Matrix power-level event
// into the participant and group setting changes to apply on WhatsApp. The
// announce-only setting is represented by events_default, while the locked
// setting is represented by the meta event levels; m.room.name is used as the
// representative meta event because wrapGroupInfoChange always changes the
// name, avatar and topic levels together.
func classifyGroupAdminChanges(change *bridgev2.MatrixPowerLevelChange) groupAdminChanges {
	var result groupAdminChanges
	if change == nil {
		return result
	}
	for _, userChange := range change.Users {
		if userChange == nil {
			continue
		}
		userID, ok := targetUserID(userChange.Target)
		if !ok {
			continue
		}
		switch adminPLAction(userChange.OrigLevel, userChange.NewLevel) {
		case whatsmeow.ParticipantChangePromote:
			result.Promote = append(result.Promote, userID)
		case whatsmeow.ParticipantChangeDemote:
			result.Demote = append(result.Demote, userID)
		}
	}
	result.Announce = settingFromPLChange(change.EventsDefault)
	result.Locked = settingFromPLChange(change.Events[event.StateRoomName.Type])
	return result
}

// HandleMatrixPowerLevels applies admin promotions/demotions and the
// announce-only and locked group settings from a Matrix power-level event.
func (wa *WhatsAppClient) HandleMatrixPowerLevels(ctx context.Context, msg *bridgev2.MatrixPowerLevelChange) (bool, error) {
	if msg == nil || msg.Portal == nil {
		return false, nil
	}
	portalJID, err := waid.ParsePortalID(msg.Portal.ID)
	if err != nil {
		return false, err
	} else if portalJID.Server != types.GroupServer {
		return false, nil
	}
	changes := classifyGroupAdminChanges(msg)
	if len(changes.Promote) == 0 && len(changes.Demote) == 0 && changes.Announce == nil && changes.Locked == nil {
		return false, nil
	}
	var retErr error
	defer wa.mcTrack(msg, time.Now(), &retErr)
	if retErr = wa.changeGroupAdmins(ctx, portalJID, changes.Promote, whatsmeow.ParticipantChangePromote); retErr != nil {
		return false, retErr
	}
	if retErr = wa.changeGroupAdmins(ctx, portalJID, changes.Demote, whatsmeow.ParticipantChangeDemote); retErr != nil {
		return false, retErr
	}
	if changes.Announce != nil {
		retErr = wa.Client.SetGroupAnnounce(ctx, portalJID, *changes.Announce)
		if retErr != nil {
			return false, wrapWhatsAppError(retErr)
		}
	}
	if changes.Locked != nil {
		retErr = wa.Client.SetGroupLocked(ctx, portalJID, *changes.Locked)
		if retErr != nil {
			return false, wrapWhatsAppError(retErr)
		}
	}
	return true, nil
}

func (wa *WhatsAppClient) changeGroupAdmins(ctx context.Context, portalJID types.JID, userIDs []networkid.UserID, action whatsmeow.ParticipantChange) error {
	if len(userIDs) == 0 {
		return nil
	}
	jids := make([]types.JID, len(userIDs))
	for i, userID := range userIDs {
		jids[i] = waid.ParseUserID(userID)
	}
	_, err := wa.Client.UpdateGroupParticipants(ctx, portalJID, jids, action)
	if err != nil {
		return wrapWhatsAppError(err)
	}
	return nil
}

var _ bridgev2.JoinRuleHandlingNetworkAPI = (*WhatsAppClient)(nil)

// joinRuleForApprovalMode is the Matrix representation of WhatsApp's group
// join approval mode. Knock means members can request to join and admins
// approve, which is the closest match to "approval required" in the spec.
func joinRuleForApprovalMode(required bool) *event.JoinRulesEventContent {
	rule := event.JoinRuleInvite
	if required {
		rule = event.JoinRuleKnock
	}
	return &event.JoinRulesEventContent{JoinRule: rule}
}

// joinApprovalFromJoinRule reports whether WhatsApp's join approval mode
// should be toggled when a room's join rule changes.
func joinApprovalFromJoinRule(prev, current event.JoinRule) *bool {
	wasRequired := prev == event.JoinRuleKnock
	isRequired := current == event.JoinRuleKnock
	if wasRequired == isRequired {
		return nil
	}
	return ptr.Ptr(isRequired)
}

// HandleMatrixJoinRule applies a Matrix join-rule change to WhatsApp's group
// join approval mode. Knock maps to approval being required.
func (wa *WhatsAppClient) HandleMatrixJoinRule(ctx context.Context, msg *bridgev2.MatrixJoinRuleChange) (bool, error) {
	if msg == nil || msg.Portal == nil || msg.Content == nil {
		return false, nil
	}
	prevRule := event.JoinRuleInvite
	if msg.PrevContent != nil {
		prevRule = msg.PrevContent.JoinRule
	}
	approval := joinApprovalFromJoinRule(prevRule, msg.Content.JoinRule)
	if approval == nil {
		return false, nil
	}
	portalJID, err := waid.ParsePortalID(msg.Portal.ID)
	if err != nil {
		return false, err
	} else if portalJID.Server != types.GroupServer {
		return false, nil
	}
	var retErr error
	defer wa.mcTrack(msg, time.Now(), &retErr)
	retErr = wa.Client.SetGroupJoinApprovalMode(ctx, portalJID, *approval)
	if retErr != nil {
		return false, wrapWhatsAppError(retErr)
	}
	return true, nil
}
