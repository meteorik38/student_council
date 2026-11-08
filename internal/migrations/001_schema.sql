DO $$
DECLARE fresh_install boolean := to_regclass('usertable') IS NULL;
BEGIN
    CREATE TABLE IF NOT EXISTS roletable (
        id SERIAL PRIMARY KEY, name VARCHAR(32) NOT NULL, acsess_level INTEGER NOT NULL
    );
    CREATE TABLE IF NOT EXISTS roomtable (
        id SERIAL PRIMARY KEY, number VARCHAR(8) NOT NULL
    );
    CREATE TABLE IF NOT EXISTS usertable (
        id INTEGER PRIMARY KEY, is_active BOOLEAN NOT NULL, tg_id VARCHAR(32) UNIQUE,
        login VARCHAR(32) NOT NULL UNIQUE, password VARCHAR(255) NOT NULL,
        name VARCHAR(32) NOT NULL, sname VARCHAR(32) NOT NULL, kpd_score INTEGER NOT NULL,
        role_id INTEGER REFERENCES roletable(id), room_id INTEGER REFERENCES roomtable(id)
    );
    CREATE TABLE IF NOT EXISTS eventtypetable (
        id SERIAL PRIMARY KEY, name VARCHAR(32) NOT NULL
    );
    CREATE TABLE IF NOT EXISTS eventlogtable (
        id SERIAL PRIMARY KEY, created_at TIMESTAMP DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
        message TEXT NOT NULL, kpd_diff INTEGER NOT NULL, event_target_id INTEGER,
        event_initiator_id INTEGER REFERENCES usertable(id), event_type_id INTEGER REFERENCES eventtypetable(id)
    );
    CREATE TABLE IF NOT EXISTS imagetable (
        id SERIAL PRIMARY KEY, image_id VARCHAR(256) NOT NULL, event_id INTEGER REFERENCES eventlogtable(id)
    );
    CREATE TABLE IF NOT EXISTS notificationtable (
        id SERIAL PRIMARY KEY, created_at TIMESTAMP DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
        event_date TIMESTAMP DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
        remind_hours INTEGER NOT NULL, message TEXT NOT NULL, is_notificated BOOLEAN NOT NULL,
        initiator_id INTEGER REFERENCES usertable(id)
    );
    CREATE TABLE IF NOT EXISTS feedbacktable (
        id SERIAL PRIMARY KEY, created_at TIMESTAMP DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
        message TEXT NOT NULL, feedback_score INTEGER NOT NULL, initiator_id INTEGER REFERENCES usertable(id)
    );
    CREATE TABLE IF NOT EXISTS sankomtable (
        id SERIAL PRIMARY KEY, mark INTEGER NOT NULL, created_at TIMESTAMP DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
        initiator_id INTEGER NOT NULL, user_id INTEGER REFERENCES usertable(id)
    );
    CREATE TABLE IF NOT EXISTS worktickettable (
        id SERIAL PRIMARY KEY, deadline TIMESTAMP NOT NULL, kpd_rollback INTEGER NOT NULL,
        ticket_hash VARCHAR(256) NOT NULL, text_task TEXT NOT NULL, performer_id INTEGER REFERENCES usertable(id)
    );
    -- Starter names/levels for a NEW database, not a reconstruction of missing production data.
    IF fresh_install THEN
        INSERT INTO roletable(id,name,acsess_level) VALUES
            (1,'student',1),(2,'sanitary',2),(4,'admin',4),(5,'admin_alt',4)
            ON CONFLICT (id) DO NOTHING;
        INSERT INTO eventtypetable(id,name) VALUES
            (3,'award_by_role_4'),(4,'award_by_role_5'),(5,'award'),(6,'penalty_or_short_message')
            ON CONFLICT (id) DO NOTHING;
        PERFORM setval(pg_get_serial_sequence('roletable','id'), (SELECT max(id) FROM roletable));
        PERFORM setval(pg_get_serial_sequence('eventtypetable','id'), (SELECT max(id) FROM eventtypetable));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_kan_user_room ON usertable(room_id);
CREATE INDEX IF NOT EXISTS idx_kan_event_target ON eventlogtable(event_target_id,created_at,id);
CREATE INDEX IF NOT EXISTS idx_kan_image_event ON imagetable(event_id);
CREATE INDEX IF NOT EXISTS idx_kan_mark_user ON sankomtable(user_id,created_at,id);
CREATE INDEX IF NOT EXISTS idx_kan_notification_date ON notificationtable(event_date);
