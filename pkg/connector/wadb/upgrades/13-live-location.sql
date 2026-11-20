-- v13: Persist active live location shares (both directions)
CREATE TABLE whatsapp_live_location (
    bridge_id       TEXT    NOT NULL,
    user_login_id   TEXT    NOT NULL,
    portal_id       TEXT    NOT NULL,
    portal_receiver TEXT    NOT NULL,
    direction       INTEGER NOT NULL,
    matrix_user_id  TEXT    NOT NULL,
    info_event_id   TEXT    NOT NULL DEFAULT '',
    description     TEXT    NOT NULL DEFAULT '',
    timeout         BIGINT  NOT NULL DEFAULT 0,
    started_ts      BIGINT  NOT NULL DEFAULT 0,
    wa_message_id   TEXT    NOT NULL DEFAULT '',
    sequence        BIGINT  NOT NULL DEFAULT 0,
    expiry_ts       BIGINT  NOT NULL DEFAULT 0,

    PRIMARY KEY (bridge_id, user_login_id, portal_id, portal_receiver, direction, matrix_user_id)
);
CREATE INDEX whatsapp_live_location_expiry_idx ON whatsapp_live_location (bridge_id, direction, expiry_ts);
