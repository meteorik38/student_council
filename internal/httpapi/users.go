package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"kan-db/internal/auth"
	"kan-db/internal/store"
)

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	typ, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var err error
	if typ == "multipart/form-data" {
		err = r.ParseMultipartForm(1 << 20)
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
	} else if typ == "application/x-www-form-urlencoded" {
		err = r.ParseForm()
	} else {
		fail(w, 415, "Use application/x-www-form-urlencoded")
		return
	}
	if err != nil {
		fail(w, 422, "Invalid login form")
		return
	}
	login, password := r.PostForm.Get("username"), r.PostForm.Get("password")
	if login == "" || !validString(login, 32) || password == "" || len(password) > 1024 {
		fail(w, 422, "username and password are required (max 32 characters / 1024 bytes)")
		return
	}
	if grant := r.PostForm.Get("grant_type"); grant != "" && grant != "password" {
		fail(w, 422, "grant_type must be password")
		return
	}
	if !a.hashSlot(w) {
		return
	}
	defer func() { <-a.hashSlots }()
	u, err := a.store.UserByLogin(r.Context(), login)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		dbError(w, err)
		return
	}
	stored := u.Password
	if errors.Is(err, pgx.ErrNoRows) {
		stored = a.dummyHash
	}
	valid, legacy := auth.VerifyPassword(password, stored, a.allowLegacy)
	if !valid || err != nil {
		unauthorized(w, "Incorrect username or password")
		return
	}
	if !u.Active {
		fail(w, 400, "Inactive user")
		return
	}
	if legacy {
		hash, err := auth.HashPassword(password)
		if err != nil {
			fail(w, 500, "Could not hash password")
			return
		}
		if err = a.store.UpgradePassword(r.Context(), u.ID, u.Password, hash); err != nil {
			dbError(w, err)
			return
		}
	}
	token, err := a.tokens.Issue(u.Login)
	if err != nil {
		fail(w, 500, "Could not issue access token")
		return
	}
	writeJSON(w, 200, map[string]string{"access_token": token, "token_type": "bearer"})
}
func (a *API) me(w http.ResponseWriter, r *http.Request, u store.User) { writeJSON(w, 200, u.View()) }
func (a *API) user(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := idParam(w, r, "stud_id")
	if !ok {
		return
	}
	u, err := a.store.UserByID(r.Context(), id)
	if err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, u.View())
}

type userInput struct {
	Name     *string `json:"name"`
	Surname  *string `json:"sname"`
	ID       *int32  `json:"stud_id"`
	Room     *string `json:"room"`
	RoleID   *int32  `json:"role_id"`
	TGID     *string `json:"tg_id"`
	KPD      int32   `json:"kpd_score"`
	Login    string  `json:"login"`
	Password string  `json:"password"`
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request, _ store.User) {
	var data userInput
	if !decode(w, r, &data) {
		return
	}
	a.saveUser(w, r, data, true)
}
func (a *API) createLegacyUser(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := idParam(w, r, "stud_id")
	if !ok {
		return
	}
	name, surname, room := r.PathValue("name"), r.PathValue("sname"), r.PathValue("room")
	q := r.URL.Query()
	if value := q.Get("room_name"); value != "" {
		room = value
	}
	data := userInput{Name: &name, Surname: &surname, ID: &id, Room: &room, Login: q.Get("login"), Password: q.Get("password")}
	if q.Has("tg_id") {
		value := q.Get("tg_id")
		data.TGID = &value
	}
	for _, key := range []string{"role_id", "kpd_score"} {
		if q.Has(key) {
			v, err := strconv.ParseInt(q.Get(key), 10, 32)
			if err != nil {
				fail(w, 422, "Invalid "+key)
				return
			}
			value := int32(v)
			if key == "role_id" {
				data.RoleID = &value
			} else {
				data.KPD = value
			}
		}
	}
	a.saveUser(w, r, data, false)
}
func (a *API) saveUser(w http.ResponseWriter, r *http.Request, d userInput, returnCredentials bool) {
	if d.ID == nil || *d.ID <= 0 || d.Name == nil || d.Surname == nil || d.Room == nil {
		fail(w, 422, "name, sname, positive stud_id and room are required")
		return
	}
	if *d.Name == "" || *d.Surname == "" || *d.Room == "" || !validString(*d.Name, 32) || !validString(*d.Surname, 32) || !validString(*d.Room, 8) || !validString(d.Login, 32) || len(d.Password) > 1024 || (d.TGID != nil && !validString(*d.TGID, 32)) {
		fail(w, 422, "Invalid user field length")
		return
	}
	role := int32(1)
	if d.RoleID != nil {
		role = *d.RoleID
	}
	if role <= 0 {
		fail(w, 422, "role_id must be positive")
		return
	}
	var err error
	if d.Login == "" {
		d.Login, err = auth.RandomString(6)
		if err != nil {
			fail(w, 500, "Could not generate login")
			return
		}
	}
	if d.Password == "" {
		d.Password, err = auth.RandomString(39)
		if err != nil {
			fail(w, 500, "Could not generate password")
			return
		}
	}
	if !a.hashSlot(w) {
		return
	}
	defer func() { <-a.hashSlots }()
	hash, err := auth.HashPassword(d.Password)
	if err != nil {
		fail(w, 500, "Could not hash password")
		return
	}
	err = a.store.CreateUser(r.Context(), store.NewUser{ID: *d.ID, Name: *d.Name, Surname: *d.Surname, Room: *d.Room, Login: d.Login, Password: hash, RoleID: role, TGID: d.TGID, KPD: d.KPD})
	if err != nil {
		dbError(w, err)
		return
	}
	if returnCredentials {
		writeJSON(w, 200, map[string]any{"stud_id": *d.ID, "login": d.Login, "password": d.Password})
	} else {
		writeJSON(w, 200, nil)
	}
}
func (a *API) setActive(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := idParam(w, r, "stud_id")
	if !ok {
		return
	}
	var active bool
	switch strings.ToLower(r.PathValue("activate")) {
	case "true", "1", "yes", "on", "t", "y":
		active = true
	case "false", "0", "no", "off", "f", "n":
		active = false
	default:
		fail(w, 422, "Invalid activate value")
		return
	}
	if err := a.store.SetActive(r.Context(), id, active); err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, nil)
}
func validRoom(name string) bool {
	r := []rune(name)
	return len(r) == 4 && unicode.IsDigit(r[0]) && unicode.IsDigit(r[1]) && unicode.IsDigit(r[2]) && unicode.IsLetter(r[3])
}
func (a *API) setRoom(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := idParam(w, r, "stud_id")
	if !ok {
		return
	}
	room := r.PathValue("room_name")
	if !validRoom(room) {
		fail(w, 418, "Неправильная сигнатура комнаты: например, 999L или 888S")
		return
	}
	if err := a.store.SetRoom(r.Context(), id, room); err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, nil)
}
