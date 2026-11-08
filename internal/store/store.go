package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRoomNotFound = errors.New("room not found")

type Store struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*Store, error) {
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}
	c.ConnConfig.RuntimeParams["timezone"] = "UTC"
	c.ConnConfig.ConnectTimeout = 5 * time.Second
	c.MaxConns = 10
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, errors.New("cannot connect to PostgreSQL; check connection settings and server availability")
	}
	return &Store{Pool: p}, nil
}

const userSelect = `SELECT u.id, u.is_active, u.login, u.password, u.name, u.sname,
 u.kpd_score, u.role_id, r.name, COALESCE(r.acsess_level, 0), u.room_id, COALESCE(room.number, '')
 FROM usertable u LEFT JOIN roletable r ON r.id=u.role_id LEFT JOIN roomtable room ON room.id=u.room_id`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Active, &u.Login, &u.Password, &u.Name, &u.Surname, &u.KPD, &u.RoleID, &u.Role, &u.AccessLevel, &u.RoomID, &u.Room)
	return u, err
}
func (s *Store) UserByLogin(ctx context.Context, login string) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, userSelect+` WHERE u.login=$1`, login))
}
func (s *Store) UserByID(ctx context.Context, id int32) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, userSelect+` WHERE u.id=$1`, id))
}
func (s *Store) UpgradePassword(ctx context.Context, id int32, previous, hash string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE usertable SET password=$1 WHERE id=$2 AND password=$3`, hash, id, previous)
	return err
}
func (s *Store) RoomID(ctx context.Context, number string) (int32, error) {
	var id int32
	err := s.Pool.QueryRow(ctx, `SELECT id FROM roomtable WHERE number=$1 ORDER BY id LIMIT 1`, number).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRoomNotFound
	}
	return id, err
}
func (s *Store) CreateUser(ctx context.Context, u NewUser) error {
	room, err := s.RoomID(ctx, u.Room)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO usertable(id,is_active,tg_id,login,password,name,sname,kpd_score,role_id,room_id)
 VALUES($1,true,$2,$3,$4,$5,$6,$7,$8,$9)`, u.ID, u.TGID, u.Login, u.Password, u.Name, u.Surname, u.KPD, u.RoleID, room)
	return err
}
func (s *Store) SetActive(ctx context.Context, id int32, active bool) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE usertable SET is_active=$1 WHERE id=$2`, active, id)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}
func (s *Store) SetRoom(ctx context.Context, id int32, number string) error {
	room, err := s.RoomID(ctx, number)
	if err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE usertable SET room_id=$1 WHERE id=$2`, room, id)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}
func (s *Store) RoomUsers(ctx context.Context, room int32) ([]UserView, error) {
	rows, err := s.Pool.Query(ctx, userSelect+` WHERE u.room_id=$1 ORDER BY u.id`, room)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]UserView, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		v := u.View()
		v.Role = nil
		users = append(users, v)
	}
	return users, rows.Err()
}
func (s *Store) Events(ctx context.Context, target int32) ([]Event, error) {
	rows, err := s.Pool.Query(ctx, `SELECT e.message,e.kpd_diff,e.event_target_id,COALESCE(e.event_initiator_id,0),e.created_at,
 COALESCE((SELECT json_agg(json_build_object('data',i.image_id) ORDER BY i.id) FROM imagetable i WHERE i.event_id=e.id),'[]'::json)
 FROM eventlogtable e WHERE e.event_target_id=$1 ORDER BY e.created_at,e.id`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		var e Event
		var images []byte
		if err := rows.Scan(&e.Message, &e.Diff, &e.TargetID, &e.InitiatorID, &e.Date.Time, &images); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(images, &e.Images); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
func (s *Store) AddKPD(ctx context.Context, e Event, eventType int32) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	// An arithmetic UPDATE locks the row and prevents lost concurrent increments.
	tag, err := tx.Exec(ctx, `UPDATE usertable SET kpd_score=kpd_score+$1 WHERE id=$2`, e.Diff, e.TargetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	var id int32
	err = tx.QueryRow(ctx, `INSERT INTO eventlogtable(message,kpd_diff,event_target_id,event_initiator_id,event_type_id,created_at)
 VALUES($1,$2,$3,$4,$5,CURRENT_TIMESTAMP AT TIME ZONE 'UTC') RETURNING id`, e.Message, e.Diff, e.TargetID, e.InitiatorID, eventType).Scan(&id)
	if err != nil {
		return err
	}
	for _, image := range e.Images {
		if _, err := tx.Exec(ctx, `INSERT INTO imagetable(image_id,event_id) VALUES($1,$2)`, image.Data, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) AddMark(ctx context.Context, target, initiator, mark int32) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO sankomtable(user_id,initiator_id,mark,created_at) VALUES($1,$2,$3,CURRENT_TIMESTAMP AT TIME ZONE 'UTC')`, target, initiator, mark)
	return err
}
func (s *Store) Marks(ctx context.Context, user int32) ([]Mark, error) {
	rows, err := s.Pool.Query(ctx, `SELECT user_id,mark,initiator_id,created_at FROM sankomtable WHERE user_id=$1 ORDER BY created_at,id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	marks := make([]Mark, 0)
	for rows.Next() {
		var m Mark
		if err := rows.Scan(&m.TargetID, &m.Mark, &m.InitiatorID, &m.Date.Time); err != nil {
			return nil, err
		}
		marks = append(marks, m)
	}
	return marks, rows.Err()
}
func (s *Store) CreateNotification(ctx context.Context, n Notification) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO notificationtable(message,remind_hours,event_date,initiator_id,is_notificated,created_at)
 VALUES($1,$2,$3,$4,false,CURRENT_TIMESTAMP AT TIME ZONE 'UTC')`, n.Message, n.RemindHours, n.EventDate.UTC(), n.InitiatorID)
	return err
}
func (s *Store) Notifications(ctx context.Context, start, end *time.Time) ([]Notification, error) {
	query := `SELECT message,remind_hours,event_date,COALESCE(initiator_id,0),id,created_at,is_notificated FROM notificationtable`
	var args []any
	if start == nil {
		query += ` WHERE is_notificated=false`
	} else {
		query += ` WHERE event_date >= $1 AND event_date <= $2`
		args = []any{*start, *end}
	}
	rows, err := s.Pool.Query(ctx, query+` ORDER BY event_date,id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notifications := make([]Notification, 0)
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.Message, &n.RemindHours, &n.EventDate.Time, &n.InitiatorID, &n.ID, &n.CreatedAt.Time, &n.Notified); err != nil {
			return nil, err
		}
		notifications = append(notifications, n)
	}
	return notifications, rows.Err()
}

// BootstrapAdmin creates an initial room/account without replacing existing users.
func (s *Store) BootstrapAdmin(ctx context.Context, u NewUser) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var level int32
	if err = tx.QueryRow(ctx, `SELECT acsess_level FROM roletable WHERE id=$1`, u.RoleID).Scan(&level); err != nil {
		return err
	}
	if level < 4 {
		return fmt.Errorf("admin role must have access level >= 4")
	}
	var room int32
	err = tx.QueryRow(ctx, `SELECT id FROM roomtable WHERE number=$1 ORDER BY id LIMIT 1`, u.Room).Scan(&room)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO roomtable(number) VALUES($1) RETURNING id`, u.Room).Scan(&room)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO usertable(id,is_active,login,password,name,sname,kpd_score,role_id,room_id) VALUES($1,true,$2,$3,$4,$5,0,$6,$7)`, u.ID, u.Login, u.Password, u.Name, u.Surname, u.RoleID, room)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
