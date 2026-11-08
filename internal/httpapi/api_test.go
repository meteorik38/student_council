package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"kan-db/internal/auth"
	"kan-db/internal/httpapi"
	"kan-db/internal/store"
	"kan-db/internal/testutil"
)

const secret = "test-secret-at-least-thirty-two-characters"
const password = "integration-password-123"

func request(t *testing.T, h http.Handler, method, path, token, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if strings.HasPrefix(path, "/api/v1/login/token") {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != status {
		t.Fatalf("%s %s: status=%d want=%d, body=%s", method, path, w.Code, status, w.Body.String())
	}
	return w
}
func fixture(t *testing.T) (*store.Store, http.Handler, map[string]string) {
	t.Helper()
	db := testutil.Database(t, true)
	ctx := context.Background()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.BootstrapAdmin(ctx, store.NewUser{ID: 1, Name: "Admin", Surname: "Test", Room: "101A", Login: "admin", Password: hash, RoleID: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO roomtable(number) VALUES('102B')`); err != nil {
		t.Fatal(err)
	}
	for _, u := range []store.NewUser{
		{ID: 2, Login: "inspector", RoleID: 2}, {ID: 3, Login: "student", RoleID: 1}, {ID: 4, Login: "neighbor", RoleID: 1}, {ID: 5, Login: "inactive", RoleID: 1}, {ID: 6, Login: "admin_alt", RoleID: 5},
	} {
		u.Name = "Test"
		u.Surname = "User"
		u.Room = "101A"
		u.Password = hash
		if err = db.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.SetActive(ctx, 5, false); err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.New(db, secret, false)
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, login := range []string{"admin", "inspector", "student", "neighbor", "inactive", "admin_alt"} {
		tokens[login], err = auth.NewTokens(secret).Issue(login)
		if err != nil {
			t.Fatal(err)
		}
	}
	return db, handler, tokens
}

func TestAPIIntegration(t *testing.T) {
	db, h, tokens := fixture(t)
	admin, inspector, student := tokens["admin"], tokens["inspector"], tokens["student"]
	t.Run("login and authorization", func(t *testing.T) {
		form := url.Values{"username": {"student"}, "password": {password}}
		w := request(t, h, "POST", "/api/v1/login/token", "", form.Encode(), 200)
		var token struct {
			AccessToken string `json:"access_token"`
			Type        string `json:"token_type"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil {
			t.Fatal(err)
		}
		if token.Type != "bearer" {
			t.Fatal("wrong token type")
		}
		request(t, h, "GET", "/api/v1/user/me", token.AccessToken, "", 200)
		form.Set("password", "wrong")
		request(t, h, "POST", "/api/v1/login/token", "", form.Encode(), 401)
		form.Set("username", "unknown")
		request(t, h, "POST", "/api/v1/login/token", "", form.Encode(), 401)
		form.Set("username", "inactive")
		form.Set("password", password)
		request(t, h, "POST", "/api/v1/login/token", "", form.Encode(), 400)
		request(t, h, "POST", "/api/v1/login/token", "", "username=student", 422)
		request(t, h, "GET", "/api/v1/user/me", "", "", 401)
		request(t, h, "GET", "/api/v1/user/me", "bad.token", "", 401)
		request(t, h, "GET", "/api/v1/user/me", tokens["inactive"], "", 400)
		request(t, h, "GET", "/api/v1/user/get/4", student, "", 403)
		request(t, h, "GET", "/api/v1/kpd/get/all/3", inspector, "", 403)
		request(t, h, "POST", "/api/v1/kpd/set", student, `{}`, 403)
		request(t, h, "POST", "/api/v1/sanitary/mark/set", student, `{}`, 403)
		request(t, h, "GET", "/api/v1/notification/all_not_notified", inspector, "", 403)
	})
	t.Run("users and legacy creation", func(t *testing.T) {
		w := request(t, h, "GET", "/api/v1/user/me", student, "", 200)
		var user store.UserView
		if err := json.Unmarshal(w.Body.Bytes(), &user); err != nil {
			t.Fatal(err)
		}
		if user.ID != 3 || user.Room != "101A" || user.Role == nil || *user.Role != "student" {
			t.Fatalf("bad profile: %+v", user)
		}
		if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "login") {
			t.Fatal("credentials leaked in profile")
		}
		request(t, h, "GET", "/api/v1/user/get/3", inspector, "", 200)
		request(t, h, "GET", "/api/v1/user/get/9999", inspector, "", 404)
		request(t, h, "GET", "/api/v1/user/get/not-an-id", inspector, "", 422)
		request(t, h, "GET", "/api/v1/user/get/2147483648", inspector, "", 422)
		body := `{"name":"Новый","sname":"Студент","stud_id":100,"room":"101A"}`
		w = request(t, h, "POST", "/api/v1/user/create", admin, body, 200)
		var credentials struct{ Login, Password string }
		if err := json.Unmarshal(w.Body.Bytes(), &credentials); err != nil {
			t.Fatal(err)
		}
		if credentials.Login == "" || credentials.Password == "" {
			t.Fatal("generated credentials not returned")
		}
		request(t, h, "POST", "/api/v1/login/token", "", url.Values{"username": {credentials.Login}, "password": {credentials.Password}}.Encode(), 200)
		u, err := db.UserByID(context.Background(), 100)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(u.Password, "$argon2id$") {
			t.Fatal("new password stored as plaintext")
		}
		request(t, h, "POST", "/api/v1/user/create", admin, body, 409)
		request(t, h, "POST", "/api/v1/user/create", admin, `{"name":"A","sname":"B","stud_id":200,"room":"missing"}`, 400)
		request(t, h, "POST", "/api/v1/user/create", admin, `{"name":"A"}`, 422)
		q := url.Values{"login": {"legacycreated"}, "password": {password}, "room_name": {"102B"}}
		w = request(t, h, "POST", "/api/v1/user/create/Old/Client/101/101A?"+q.Encode(), admin, "", 200)
		if strings.TrimSpace(w.Body.String()) != "null" {
			t.Fatal("legacy create response changed")
		}
		u, err = db.UserByID(context.Background(), 101)
		if err != nil || u.Room != "102B" {
			t.Fatalf("room_name compatibility: %+v %v", u, err)
		}
		request(t, h, "POST", "/api/v1/user/switch/room/101/101A", admin, "", 200)
		request(t, h, "POST", "/api/v1/user/switch/room/101/999Z", admin, "", 400)
		request(t, h, "POST", "/api/v1/user/switch/room/101/bad", admin, "", 418)
		request(t, h, "POST", "/api/v1/user/switch/active/101/false", admin, "", 200)
		request(t, h, "POST", "/api/v1/login/token", "", url.Values{"username": {"legacycreated"}, "password": {password}}.Encode(), 400)
		request(t, h, "POST", "/api/v1/user/switch/active/101/true", admin, "", 200)
		request(t, h, "POST", "/api/v1/user/switch/active/99999/true", admin, "", 404)
		request(t, h, "GET", "/api/v1/user/me/", student, "", 307)
	})
	t.Run("KPD and event types", func(t *testing.T) {
		w := request(t, h, "GET", "/api/v1/kpd/my/all", student, "", 200)
		if strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatal("empty history must be an array")
		}
		for _, tc := range []struct {
			token           string
			diff, eventType int
			message         string
		}{{admin, 5, 3, "Помощь"}, {inspector, -2, 6, "Уборка"}, {inspector, 3, 5, "Помощь"}, {tokens["admin_alt"], 1, 4, "Помощь"}} {
			body := fmt.Sprintf(`{"message":%q,"kpd_diff":%d,"target_stud_id":3,"images":[{"data":"tg-image-id"}]}`, tc.message, tc.diff)
			request(t, h, "POST", "/api/v1/kpd/set", tc.token, body, 200)
			var eventType int
			if err := db.Pool.QueryRow(context.Background(), `SELECT event_type_id FROM eventlogtable ORDER BY id DESC LIMIT 1`).Scan(&eventType); err != nil {
				t.Fatal(err)
			}
			if eventType != tc.eventType {
				t.Fatalf("event type=%d want=%d", eventType, tc.eventType)
			}
		}
		w = request(t, h, "GET", "/api/v1/kpd/my/all", student, "", 200)
		var events []store.Event
		if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil {
			t.Fatal(err)
		}
		if len(events) != 4 || events[0].TargetID != 3 || events[0].Images[0].Data != "tg-image-id" {
			t.Fatalf("incorrect events: %+v", events)
		}
		request(t, h, "GET", "/api/v1/kpd/get/all/3", admin, "", 200)
		u, err := db.UserByID(context.Background(), 3)
		if err != nil || u.KPD != 7 {
			t.Fatalf("balance: %d %v", u.KPD, err)
		}
		request(t, h, "POST", "/api/v1/kpd/set", admin, `{"message":"test","kpd_diff":1,"target_stud_id":999,"images":[]}`, 404)
		request(t, h, "POST", "/api/v1/kpd/set", admin, `{"message":"test","kpd_diff":1,"target_stud_id":3}`, 422)
		request(t, h, "POST", "/api/v1/kpd/set", admin, `{"message":"test","kpd_diff":1,"target_stud_id":3,"images":[null]}`, 422)
		request(t, h, "POST", "/api/v1/kpd/set", admin, `{} {}`, 422)
	})
	t.Run("sanitary isolation and aliases", func(t *testing.T) {
		room := request(t, h, "GET", "/api/v1/sanitary/room/get/101A", inspector, "", 200)
		var roomID int
		if err := json.Unmarshal(room.Body.Bytes(), &roomID); err != nil {
			t.Fatal(err)
		}
		request(t, h, "GET", "/api/v1/sanitary/room/get/999Z", inspector, "", 400)
		w := request(t, h, "POST", "/api/v1/sanitary/mark/set", inspector, `{"target_id":3,"mark":5}`, 200)
		if strings.TrimSpace(w.Body.String()) != "200" {
			t.Fatal("legacy mark response changed")
		}
		request(t, h, "POST", "/api/v1/sanitary/mark/set", inspector, `{"target_id":4,"mark":1}`, 200)
		for _, base := range []string{"/api/v1/sanitary/mark", "/api/v1/sanitarymark"} {
			for _, path := range []string{base + "/my", base + "/get/3"} {
				token := inspector
				if strings.HasSuffix(path, "/my") {
					token = student
				}
				w := request(t, h, "GET", path, token, "", 200)
				var marks []store.Mark
				if err := json.Unmarshal(w.Body.Bytes(), &marks); err != nil {
					t.Fatal(err)
				}
				if len(marks) != 1 || marks[0].TargetID != 3 || marks[0].Mark != 5 {
					t.Fatalf("other users' marks leaked: %+v", marks)
				}
			}
			w := request(t, h, "GET", fmt.Sprintf("%s/roomusers/%d", base, roomID), inspector, "", 200)
			var users []store.UserView
			if err := json.Unmarshal(w.Body.Bytes(), &users); err != nil {
				t.Fatal(err)
			}
			if len(users) < 6 {
				t.Fatal("missing room users")
			}
			for _, u := range users {
				if u.Room != "101A" || u.Role != nil {
					t.Fatal("room users contract changed")
				}
			}
		}
	})
	t.Run("notifications and UTC dates", func(t *testing.T) {
		body := `{"message":"Собрание","remind_hours":2,"event_date":"2025-01-24T15:00:00+03:00"}`
		request(t, h, "POST", "/api/v1/notification/create", admin, body, 200)
		w := request(t, h, "GET", "/api/v1/notification/all_not_notified", admin, "", 200)
		var notifications []store.Notification
		if err := json.Unmarshal(w.Body.Bytes(), &notifications); err != nil {
			t.Fatal(err)
		}
		if len(notifications) != 1 || notifications[0].Notified || notifications[0].EventDate.Hour() != 12 {
			t.Fatalf("unexpected notifications: %+v", notifications)
		}
		path := "/api/v1/notification/by_event_release_timestamp/start/2025-01-24T12:00:00/end/2025-01-24T12:00:00"
		w = request(t, h, "GET", path, admin, "", 200)
		if !strings.Contains(w.Body.String(), "Собрание") {
			t.Fatal("date range must be inclusive")
		}
		request(t, h, "GET", "/api/v1/notification/by_event_release_timestamp/start/bad/end/bad", admin, "", 422)
		request(t, h, "GET", "/api/v1/notification/by_event_release_timestamp/start/2025-02-01T00:00:00/end/2025-01-01T00:00:00", admin, "", 422)
		request(t, h, "POST", "/api/v1/notification/create", admin, `{"message":"x","remind_hours":1,"event_date":"invalid"}`, 422)
	})
	t.Run("health documentation and metrics", func(t *testing.T) {
		for _, path := range []string{"/healthz", "/readyz", "/docs", "/openapi.json"} {
			request(t, h, "GET", path, "", "", 200)
		}
		w := request(t, h, "GET", "/metrics", "", "", 200)
		if !strings.Contains(w.Body.String(), "http_requests_total") || !strings.Contains(w.Body.String(), "http_request_duration_seconds") {
			t.Fatal("HTTP metrics missing")
		}
		if strings.Contains(w.Body.String(), password) || strings.Contains(w.Body.String(), "legacycreated") {
			t.Fatal("sensitive metric labels")
		}
	})
}

func TestLegacyPasswordsRequireOptInAndUpgrade(t *testing.T) {
	db, h, _ := fixture(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `UPDATE usertable SET password=$1 WHERE id=3`, password); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"username": {"student"}, "password": {password}}.Encode()
	request(t, h, "POST", "/api/v1/login/token", "", form, 401)
	legacy, err := httpapi.New(db, secret, true)
	if err != nil {
		t.Fatal(err)
	}
	request(t, legacy, "POST", "/api/v1/login/token", "", form, 200)
	u, err := db.UserByID(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.Password, "$argon2id$") {
		t.Fatal("legacy password was not upgraded")
	}
	request(t, h, "POST", "/api/v1/login/token", "", form, 200)
}

func TestPublicEndpointsWithoutDatabase(t *testing.T) {
	h, err := httpapi.New(nil, secret, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/healthz", "/docs", "/openapi.json", "/metrics"} {
		request(t, h, "GET", path, "", "", 200)
	}
	w := request(t, h, "GET", "/openapi.json", "", "", 200)
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	for path, methods := range spec.Paths {
		for method := range methods {
			if path == "/api/v1/login/token" {
				continue
			}
			path = strings.NewReplacer("{stud_id}", "3", "{user_id}", "3", "{room_id}", "1", "{name}", "A", "{sname}", "B", "{room}", "101A", "{room_name}", "101A", "{activate}", "true", "{start}", "2025-01-01T00:00:00", "{end}", "2025-01-02T00:00:00").Replace(path)
			request(t, h, strings.ToUpper(method), path, "", "", 401)
		}
	}
}
