package httpapi

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
)

var openAPI []byte

func serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(openAPI)
}

var docsTemplate = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>KAN DB API</title><style>body{font:16px system-ui;max-width:1100px;margin:40px auto;padding:0 20px;color:#183047}table{border-collapse:collapse;width:100%}td,th{padding:12px;text-align:left;border-bottom:1px solid #ddd}code{font-size:13px;overflow-wrap:anywhere}a{color:#1267bb}</style>
<h1>KAN DB API</h1><p>Учёт студентов, КПД, санитарных проверок и уведомлений.</p>
<p>Вход: <code>POST /api/v1/login/token</code>, форма <code>username=…&amp;password=…</code> с типом <code>application/x-www-form-urlencoded</code>.
Для защищённых запросов передавайте <code>Authorization: Bearer &lt;access_token&gt;</code>. Токен действует 48 часов.</p>
<p><a href="/openapi.json">Спецификация OpenAPI: параметры, модели, права и ответы</a></p>
<table><thead><tr><th>Метод</th><th>Адрес</th><th>Описание</th></tr></thead><tbody>
{{range .}}<tr><td>{{.Method}}</td><td><code>{{.Path}}</code></td><td>{{.Summary}}</td></tr>{{end}}
</tbody></table><p>Миграции, настройка и особенности совместимости описаны в README проекта.</p></html>`))

func serveDocs(w http.ResponseWriter, r *http.Request) {
	var spec struct {
		Paths map[string]map[string]struct {
			Summary string `json:"summary"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(openAPI, &spec); err != nil {
		fail(w, 500, "Documentation unavailable")
		return
	}
	type row struct{ Method, Path, Summary string }
	rows := make([]row, 0)
	for path, methods := range spec.Paths {
		for method, operation := range methods {
			rows = append(rows, row{method, path, operation.Summary})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Path == rows[j].Path {
			return rows[i].Method < rows[j].Method
		}
		return rows[i].Path < rows[j].Path
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = docsTemplate.Execute(w, rows)
}
