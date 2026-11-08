package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"kan-db/internal/migrations"
	"kan-db/internal/store"
	"kan-db/internal/testutil"
)

func TestKPDAtomicityAndConcurrentIncrements(t *testing.T) {
	db := testutil.Database(t, true)
	ctx := context.Background()
	if err := db.BootstrapAdmin(ctx, store.NewUser{ID: 1, Name: "Admin", Surname: "Test", Room: "101A", Login: "admin", Password: "fixture-only", RoleID: 4}); err != nil {
		t.Fatal(err)
	}
	e := store.Event{Message: "Award", Diff: 10, TargetID: 1, InitiatorID: 1, Images: []store.Image{{Data: strings.Repeat("x", 257)}}}
	if err := db.AddKPD(ctx, e, 3); err == nil {
		t.Fatal("expected image length error")
	}
	u, err := db.UserByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	events, err := db.Events(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if u.KPD != 0 || len(events) != 0 {
		t.Fatalf("partial transaction persisted: balance %d, events %d", u.KPD, len(events))
	}
	e.Images = nil
	if err := db.AddKPD(ctx, e, 999); err == nil {
		t.Fatal("expected invalid event type error")
	}
	u, err = db.UserByID(ctx, 1)
	if err != nil || u.KPD != 0 {
		t.Fatalf("event failure changed balance: %+v %v", u, err)
	}
	const count = 24
	var wg sync.WaitGroup
	errors := make(chan error, count)
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- db.AddKPD(ctx, store.Event{Message: "Award", Diff: 1, TargetID: 1, InitiatorID: 1}, 3)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	u, err = db.UserByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	events, err = db.Events(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if u.KPD != count || len(events) != count {
		t.Fatalf("lost increments: balance %d, events %d", u.KPD, len(events))
	}
}
func TestAdoptLegacySchemaAndIdempotentMigrations(t *testing.T) {
	db := testutil.Database(t, false)
	ctx := context.Background()
	_, err := db.Pool.Exec(ctx, `CREATE TABLE roletable(id SERIAL PRIMARY KEY,name VARCHAR(32) NOT NULL,acsess_level INTEGER NOT NULL);
 CREATE TABLE roomtable(id SERIAL PRIMARY KEY,number VARCHAR(8) NOT NULL);
 CREATE TABLE usertable(id INTEGER PRIMARY KEY,is_active BOOLEAN NOT NULL,tg_id VARCHAR(32) UNIQUE,login VARCHAR(32) NOT NULL UNIQUE,password VARCHAR(64) NOT NULL,name VARCHAR(32) NOT NULL,sname VARCHAR(32) NOT NULL,kpd_score INTEGER NOT NULL,role_id INTEGER REFERENCES roletable(id),room_id INTEGER REFERENCES roomtable(id));
 CREATE TABLE eventtypetable(id SERIAL PRIMARY KEY,name VARCHAR(32) NOT NULL);
 INSERT INTO roletable VALUES(4,'Existing custom admin',9);
 INSERT INTO roomtable VALUES(7,'777A');
 INSERT INTO usertable VALUES(99,true,NULL,'existing','old password','Existing','User',42,4,7);
 INSERT INTO eventtypetable VALUES(3,'Existing custom event');`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = migrations.Apply(ctx, db.Pool); err != nil {
			t.Fatal(err)
		}
	}
	u, err := db.UserByID(ctx, 99)
	if err != nil {
		t.Fatal(err)
	}
	if u.KPD != 42 || u.Password != "old password" || u.Role == nil || *u.Role != "Existing custom admin" || u.AccessLevel != 9 {
		t.Fatalf("legacy data changed: %+v", u)
	}
	var size, count int
	if err = db.Pool.QueryRow(ctx, `SELECT character_maximum_length FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='usertable' AND column_name='password'`).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM roletable`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if size != 255 || count != 1 {
		t.Fatalf("unexpected schema/seed changes: size=%d roles=%d", size, count)
	}
}
func TestTimestampCompatibility(t *testing.T) {
	for _, raw := range []string{`"2025-01-24T12:00:00"`, `"2025-01-24T15:00:00+03:00"`, `"2025-01-24T12:00:00Z"`} {
		var v store.Timestamp
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(v)
		if err != nil || string(out) != `"2025-01-24T12:00:00"` {
			t.Fatalf("%s -> %s: %v", raw, out, err)
		}
	}
	if _, err := store.ParseTime("not a date"); err == nil {
		t.Fatal("invalid datetime accepted")
	}
}
