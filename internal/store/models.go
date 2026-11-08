package store

import (
	"encoding/json"
	"fmt"
	"time"
)

type Timestamp struct{ time.Time }

func (t Timestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.UTC().Format("2006-01-02T15:04:05.999999"))
}
func (t *Timestamp) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v, err := ParseTime(raw)
	if err != nil {
		return err
	}
	t.Time = v
	return nil
}
func ParseTime(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
		if v, err := time.Parse(layout, raw); err == nil {
			return v.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid datetime: expected ISO 8601")
}

type User struct {
	ID          int32
	Active      bool
	Login       string
	Password    string
	Name        string
	Surname     string
	KPD         int32
	RoleID      *int32
	Role        *string
	AccessLevel int32
	RoomID      *int32
	Room        string
}
type UserView struct {
	Name    string  `json:"name"`
	Surname string  `json:"sname"`
	KPD     int32   `json:"kpd"`
	Role    *string `json:"role"`
	ID      int32   `json:"stud_id"`
	Room    string  `json:"room"`
}

func (u User) View() UserView { return UserView{u.Name, u.Surname, u.KPD, u.Role, u.ID, u.Room} }

type NewUser struct {
	ID                                   int32
	Name, Surname, Room, Login, Password string
	RoleID                               int32
	TGID                                 *string
	KPD                                  int32
}
type Image struct {
	Data string `json:"data"`
}
type Event struct {
	Message     string    `json:"message"`
	Diff        int32     `json:"kpd_diff"`
	TargetID    int32     `json:"target_stud_id"`
	Images      []Image   `json:"images"`
	InitiatorID int32     `json:"initiator_id"`
	Date        Timestamp `json:"date"`
}
type Mark struct {
	TargetID    int32     `json:"target_id"`
	Mark        int32     `json:"mark"`
	InitiatorID int32     `json:"created_by"`
	Date        Timestamp `json:"date"`
}
type Notification struct {
	Message     string    `json:"message"`
	RemindHours int32     `json:"remind_hours"`
	EventDate   Timestamp `json:"event_date"`
	InitiatorID int32     `json:"initiator_id"`
	ID          int32     `json:"n_id"`
	CreatedAt   Timestamp `json:"created_at"`
	Notified    bool      `json:"is_notificated"`
}
