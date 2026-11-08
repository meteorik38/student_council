package httpapi

import (
	"net/http"
	"unicode/utf8"

	"kan-db/internal/store"
)

func (a *API) myEvents(w http.ResponseWriter, r *http.Request, u store.User) {
	a.writeEvents(w, r, u.ID)
}
func (a *API) events(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := idParam(w, r, "user_id")
	if ok {
		a.writeEvents(w, r, id)
	}
}
func (a *API) writeEvents(w http.ResponseWriter, r *http.Request, id int32) {
	events, err := a.store.Events(r.Context(), id)
	if err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, events)
}
func (a *API) addKPD(w http.ResponseWriter, r *http.Request, u store.User) {
	var d struct {
		Message  *string `json:"message"`
		Diff     *int32  `json:"kpd_diff"`
		TargetID *int32  `json:"target_stud_id"`
		Images   *[]struct {
			Data *string `json:"data"`
		} `json:"images"`
	}
	if !decode(w, r, &d) {
		return
	}
	if d.Message == nil || d.Diff == nil || d.TargetID == nil || *d.TargetID <= 0 || d.Images == nil || !validString(*d.Message, 1<<20) {
		fail(w, 422, "message, kpd_diff, positive target_stud_id and images are required")
		return
	}
	images := make([]store.Image, 0, len(*d.Images))
	for _, image := range *d.Images {
		if image.Data == nil || !validString(*image.Data, 256) {
			fail(w, 422, "Each image requires data of at most 256 characters")
			return
		}
		images = append(images, store.Image{Data: *image.Data})
	}
	eventType := int32(5)
	if *d.Diff <= 0 || utf8.RuneCountInString(*d.Message) < 5 {
		eventType = 6
	} else if u.RoleID != nil {
		switch *u.RoleID {
		case 4:
			eventType = 3
		case 5:
			eventType = 4
		}
	}
	e := store.Event{Message: *d.Message, Diff: *d.Diff, TargetID: *d.TargetID, InitiatorID: u.ID, Images: images}
	if err := a.store.AddKPD(r.Context(), e, eventType); err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, nil)
}
func (a *API) room(w http.ResponseWriter, r *http.Request, _ store.User) {
	name := r.PathValue("room_name")
	if !validRoom(name) {
		fail(w, 418, "Неправильная сигнатура комнаты: например, 999L или 888S")
		return
	}
	id, err := a.store.RoomID(r.Context(), name)
	if err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, id)
}
func (a *API) addMark(w http.ResponseWriter, r *http.Request, u store.User) {
	var d struct {
		TargetID *int32 `json:"target_id"`
		Mark     *int32 `json:"mark"`
	}
	if !decode(w, r, &d) {
		return
	}
	if d.TargetID == nil || *d.TargetID <= 0 || d.Mark == nil {
		fail(w, 422, "positive target_id and mark are required")
		return
	}
	if err := a.store.AddMark(r.Context(), *d.TargetID, u.ID, *d.Mark); err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, 200)
}
func (a *API) myMarks(w http.ResponseWriter, r *http.Request, u store.User) { a.writeMarks(w, r, u.ID) }
func (a *API) marks(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := idParam(w, r, "stud_id")
	if ok {
		a.writeMarks(w, r, id)
	}
}
func (a *API) writeMarks(w http.ResponseWriter, r *http.Request, id int32) {
	marks, err := a.store.Marks(r.Context(), id)
	if err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, marks)
}
func (a *API) roomUsers(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := idParam(w, r, "room_id")
	if !ok {
		return
	}
	users, err := a.store.RoomUsers(r.Context(), id)
	if err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, users)
}
func (a *API) createNotification(w http.ResponseWriter, r *http.Request, u store.User) {
	var d struct {
		Message     *string          `json:"message"`
		RemindHours *int32           `json:"remind_hours"`
		EventDate   *store.Timestamp `json:"event_date"`
	}
	if !decode(w, r, &d) {
		return
	}
	if d.Message == nil || d.RemindHours == nil || d.EventDate == nil || !validString(*d.Message, 1<<20) {
		fail(w, 422, "message, remind_hours and event_date are required")
		return
	}
	n := store.Notification{Message: *d.Message, RemindHours: *d.RemindHours, EventDate: *d.EventDate, InitiatorID: u.ID}
	if err := a.store.CreateNotification(r.Context(), n); err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, nil)
}
func (a *API) pendingNotifications(w http.ResponseWriter, r *http.Request, _ store.User) {
	items, err := a.store.Notifications(r.Context(), nil, nil)
	if err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
func (a *API) notificationsByDate(w http.ResponseWriter, r *http.Request, _ store.User) {
	start, err := store.ParseTime(r.PathValue("start"))
	if err != nil {
		fail(w, 422, "Invalid start datetime")
		return
	}
	end, err := store.ParseTime(r.PathValue("end"))
	if err != nil {
		fail(w, 422, "Invalid end datetime")
		return
	}
	if start.After(end) {
		fail(w, 422, "start must not be later than end")
		return
	}
	items, err := a.store.Notifications(r.Context(), &start, &end)
	if err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
