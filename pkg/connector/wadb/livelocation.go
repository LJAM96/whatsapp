package wadb

import (
	"context"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

// LiveLocationDirection distinguishes the two directions of an active live
// location share. WhatsApp-originated shares need the beacon_info event ID to
// attach further beacons to; Matrix-originated shares need the WhatsApp
// message ID, sequence number and expiry to send updates and the final stop.
type LiveLocationDirection int

const (
	LiveLocationFromWhatsApp LiveLocationDirection = 1
	LiveLocationToWhatsApp   LiveLocationDirection = 2
)

type LiveLocationQuery struct {
	BridgeID networkid.BridgeID
	*dbutil.QueryHelper[*LiveLocationShare]
}

// LiveLocationShare is one active share. matrix_user_id is the state key of
// the beacon_info event: the WhatsApp ghost for shares coming from WhatsApp,
// and the real Matrix user for shares coming from Weave.
type LiveLocationShare struct {
	BridgeID     networkid.BridgeID
	UserLoginID  networkid.UserLoginID
	PortalKey    networkid.PortalKey
	Direction    LiveLocationDirection
	MatrixUserID id.UserID

	InfoEventID id.EventID
	Description string
	Timeout     int64
	StartedTS   int64

	WAMessageID string
	Sequence    int64
	Expiry      int64
}

const (
	getLiveLocationQuery = `
		SELECT bridge_id, user_login_id, portal_id, portal_receiver, direction, matrix_user_id,
		       info_event_id, description, timeout, started_ts, wa_message_id, sequence, expiry_ts
		FROM whatsapp_live_location
		WHERE bridge_id=$1 AND user_login_id=$2 AND portal_id=$3 AND portal_receiver=$4
		  AND direction=$5 AND matrix_user_id=$6
	`
	putLiveLocationQuery = `
		INSERT INTO whatsapp_live_location (
			bridge_id, user_login_id, portal_id, portal_receiver, direction, matrix_user_id,
			info_event_id, description, timeout, started_ts, wa_message_id, sequence, expiry_ts
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (bridge_id, user_login_id, portal_id, portal_receiver, direction, matrix_user_id) DO UPDATE SET
			info_event_id=excluded.info_event_id, description=excluded.description,
			timeout=excluded.timeout, started_ts=excluded.started_ts,
			wa_message_id=excluded.wa_message_id, sequence=excluded.sequence, expiry_ts=excluded.expiry_ts
	`
	deleteLiveLocationQuery = `
		DELETE FROM whatsapp_live_location
		WHERE bridge_id=$1 AND user_login_id=$2 AND portal_id=$3 AND portal_receiver=$4
		  AND direction=$5 AND matrix_user_id=$6
	`
	putLiveLocationSentMessageQuery = `
		INSERT INTO whatsapp_live_location_sent_message (
			bridge_id, user_login_id, portal_id, portal_receiver, matrix_user_id, wa_message_id
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (bridge_id, user_login_id, portal_id, portal_receiver, wa_message_id) DO UPDATE SET
			matrix_user_id=excluded.matrix_user_id
	`
	getLiveLocationSentMessageQuery = `
		SELECT wa_message_id FROM whatsapp_live_location_sent_message
		WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3 AND wa_message_id=$4
	`
)

// GetAny returns the stored share without checking expiry.
func (llq *LiveLocationQuery) GetAny(ctx context.Context, loginID networkid.UserLoginID, portalKey networkid.PortalKey, direction LiveLocationDirection, matrixUserID id.UserID) (*LiveLocationShare, error) {
	return llq.QueryOne(ctx, getLiveLocationQuery, llq.BridgeID, loginID, portalKey.ID, portalKey.Receiver, direction, matrixUserID)
}

// Get returns the share only if it is still within its live window. Expired
// shares are removed from the database.
func (llq *LiveLocationQuery) Get(ctx context.Context, loginID networkid.UserLoginID, portalKey networkid.PortalKey, direction LiveLocationDirection, matrixUserID id.UserID) (*LiveLocationShare, error) {
	share, err := llq.GetAny(ctx, loginID, portalKey, direction, matrixUserID)
	if err != nil || share == nil {
		return nil, err
	}
	if share.Expired() {
		err = llq.Delete(ctx, loginID, portalKey, direction, matrixUserID)
		return nil, err
	}
	return share, nil
}

func (llq *LiveLocationQuery) Put(ctx context.Context, share *LiveLocationShare) error {
	share.BridgeID = llq.BridgeID
	return llq.Exec(ctx, putLiveLocationQuery, share.sqlVariables()...)
}

func (llq *LiveLocationQuery) Delete(ctx context.Context, loginID networkid.UserLoginID, portalKey networkid.PortalKey, direction LiveLocationDirection, matrixUserID id.UserID) error {
	return llq.Exec(ctx, deleteLiveLocationQuery, llq.BridgeID, loginID, portalKey.ID, portalKey.Receiver, direction, matrixUserID)
}

// Expired reports whether the beacon is past its live window. Clients stop
// treating a beacon as live at started_ts + timeout, even without a stop.
func (lls *LiveLocationShare) Expired() bool {
	return lls == nil || (lls.Expiry > 0 && time.Now().UnixMilli() >= lls.Expiry)
}

func (lls *LiveLocationShare) Scan(row dbutil.Scannable) (*LiveLocationShare, error) {
	err := row.Scan(&lls.BridgeID, &lls.UserLoginID, &lls.PortalKey.ID, &lls.PortalKey.Receiver, &lls.Direction, &lls.MatrixUserID,
		&lls.InfoEventID, &lls.Description, &lls.Timeout, &lls.StartedTS, &lls.WAMessageID, &lls.Sequence, &lls.Expiry)
	if err != nil {
		return nil, err
	}
	return lls, nil
}

func (lls *LiveLocationShare) sqlVariables() []any {
	return []any{lls.BridgeID, lls.UserLoginID, lls.PortalKey.ID, lls.PortalKey.Receiver, lls.Direction, lls.MatrixUserID,
		lls.InfoEventID, lls.Description, lls.Timeout, lls.StartedTS, lls.WAMessageID, lls.Sequence, lls.Expiry}
}

// LiveLocationSentMessage records one WhatsApp message ID the bridge sent for
// a Weave-originated live location share. Every start, update and final stop
// gets its own row, and rows are kept after the share ends so a late echo of
// any message in the share is still recognised as ours.
type LiveLocationSentMessage struct {
	WAMessageID string
}

func (llsm *LiveLocationSentMessage) Scan(row dbutil.Scannable) (*LiveLocationSentMessage, error) {
	err := row.Scan(&llsm.WAMessageID)
	if err != nil {
		return nil, err
	}
	return llsm, nil
}

type LiveLocationSentMessageQuery struct {
	BridgeID networkid.BridgeID
	*dbutil.QueryHelper[*LiveLocationSentMessage]
}

// Put records a WhatsApp message ID sent for a Weave-originated share.
func (llmq *LiveLocationSentMessageQuery) Put(ctx context.Context, loginID networkid.UserLoginID, portalKey networkid.PortalKey, matrixUserID id.UserID, waMessageID string) error {
	return llmq.Exec(ctx, putLiveLocationSentMessageQuery, llmq.BridgeID, loginID, portalKey.ID, portalKey.Receiver, matrixUserID, waMessageID)
}

// Exists reports whether waMessageID was sent by the bridge for a
// Weave-originated share in this portal. Querying without the login ID is
// intentional: message IDs are unique, and the message converter does not know
// which user login handled the original Matrix event.
func (llmq *LiveLocationSentMessageQuery) Exists(ctx context.Context, portalKey networkid.PortalKey, waMessageID string) (bool, error) {
	row, err := llmq.QueryOne(ctx, getLiveLocationSentMessageQuery, llmq.BridgeID, portalKey.ID, portalKey.Receiver, waMessageID)
	if err != nil {
		return false, err
	}
	return row != nil, nil
}
