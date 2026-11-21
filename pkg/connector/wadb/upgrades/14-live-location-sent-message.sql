-- v14: Record WhatsApp message IDs sent for Weave-originated live location shares
CREATE TABLE whatsapp_live_location_sent_message (
    bridge_id       TEXT NOT NULL,
    user_login_id   TEXT NOT NULL,
    portal_id       TEXT NOT NULL,
    portal_receiver TEXT NOT NULL,
    matrix_user_id  TEXT NOT NULL,
    wa_message_id   TEXT NOT NULL,

    PRIMARY KEY (bridge_id, user_login_id, portal_id, portal_receiver, wa_message_id)
);
CREATE INDEX whatsapp_live_location_sent_message_portal_idx ON whatsapp_live_location_sent_message (bridge_id, portal_id, portal_receiver);
