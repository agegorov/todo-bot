package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/aegorov/todo-bot/internal/auth"
	"github.com/aegorov/todo-bot/internal/db"
	"github.com/aegorov/todo-bot/internal/parser"
)

type Server struct {
	queries *db.Queries
	auth    *auth.Handler
}

func New(q *db.Queries, a *auth.Handler) *Server {
	return &Server{queries: q, auth: a}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PATCH", "PUT", "DELETE"},
		AllowedHeaders:   []string{"Content-Type"},
		AllowCredentials: true,
	}))

	// Auth routes (публичные)
	r.Get("/auth/google", s.auth.Login)
	r.Get("/auth/callback", s.auth.Callback)
	r.Get("/auth/logout", s.auth.Logout)

	// Страница логина
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/login.html")
	})

	// Текущий пользователь (для фронта)
	r.With(s.auth.APIMiddleware).Get("/api/me", s.getMe)

	// Защищённые API маршруты
	r.With(s.auth.APIMiddleware).Route("/api", func(r chi.Router) {
		r.Get("/tasks", s.listTasks)
		r.Post("/tasks", s.createTask)
		r.Put("/tasks/{id}", s.updateTask)
		r.Patch("/tasks/{id}/column", s.moveTask)
		r.Delete("/tasks/{id}", s.deleteTask)

		r.Get("/columns", s.listColumns)
		r.Post("/columns", s.createColumn)
		r.Put("/columns/{id}", s.updateColumn)
		r.Delete("/columns/{id}", s.deleteColumn)
		r.Patch("/columns/{id}/position", s.reorderColumn)

		r.Get("/projects", s.listProjects)
	})

	// Статика (с проверкой авторизации через middleware)
	r.With(s.auth.Middleware).Handle("/*", http.FileServer(http.Dir("web")))

	return r
}

// ── Me ────────────────────────────────────────────────────────────────────────

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		jsonError(w, nil, 401)
		return
	}
	jsonOK(w, map[string]any{
		"id":     u.ID,
		"email":  u.Email,
		"name":   u.Name,
		"avatar": u.Avatar,
	})
}

// ── Tasks ─────────────────────────────────────────────────────────────────────

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	tasks, err := s.queries.ListTasksForBoard(r.Context(), &u.ID)
	if err != nil {
		jsonError(w, err, 500)
		return
	}
	type taskResp struct {
		ID           int64    `json:"id"`
		Title        string   `json:"title"`
		Notes        *string  `json:"notes"`
		Priority     int16    `json:"priority"`
		Deadline     *string  `json:"deadline"`
		ColumnID     int64    `json:"column_id"`
		ProjectName  string   `json:"project_name"`
		ProjectColor string   `json:"project_color"`
		DelegatedTo  *string  `json:"delegated_to"`
		Tags         []string `json:"tags"`
	}
	resp := make([]taskResp, len(tasks))
	for i, t := range tasks {
		var dl *string
		if t.Deadline.Valid {
			s := t.Deadline.Time.Format("02 Jan 15:04")
			dl = &s
		}
		var tags []string
		switch v := t.Tags.(type) {
		case []string:
			tags = v
		case []interface{}:
			for _, tag := range v {
				if s, ok := tag.(string); ok {
					tags = append(tags, s)
				}
			}
		}
		if tags == nil {
			tags = []string{}
		}
		resp[i] = taskResp{
			ID: t.ID, Title: t.Title, Notes: t.Notes,
			Priority: t.Priority, Deadline: dl, ColumnID: t.ColumnID,
			ProjectName: t.ProjectName, ProjectColor: t.ProjectColor,
			DelegatedTo: t.DelegatedTo, Tags: tags,
		}
	}
	jsonOK(w, resp)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	var body struct {
		Text     string `json:"text"`
		ColumnID int64  `json:"column_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		jsonError(w, nil, 400)
		return
	}

	parsed := parser.Parse(body.Text, time.Now())
	projects, _ := s.queries.ListProjects(r.Context())
	projectID := int64(1)
	for _, p := range projects {
		if p.Name == parsed.Project {
			projectID = p.ID
			break
		}
	}

	var deadline pgtype.Timestamptz
	if parsed.HasDeadline {
		deadline = pgtype.Timestamptz{Time: parsed.Deadline, Valid: true}
	}
	var notes, delegated, recurRule *string
	if parsed.Notes != "" {
		notes = &parsed.Notes
	}
	if parsed.DelegatedTo != "" {
		delegated = &parsed.DelegatedTo
	}
	if parsed.RecurRule != "" {
		recurRule = &parsed.RecurRule
	}

	task, err := s.queries.CreateTask(r.Context(), db.CreateTaskParams{
		ProjectID:   projectID,
		Title:       parsed.Title,
		Notes:       notes,
		Priority:    int16(parsed.Priority),
		Deadline:    deadline,
		DelegatedTo: delegated,
		IsRecurring: parsed.IsRecurring,
		RecurRule:   recurRule,
	})
	if err != nil {
		jsonError(w, err, 500)
		return
	}

	colID := body.ColumnID
	if colID == 0 {
		cols, _ := s.queries.ListColumns(r.Context(), &u.ID)
		if len(cols) > 0 {
			colID = cols[0].ID
		}
	}
	if colID > 0 {
		_ = s.queries.MoveTaskToColumn(r.Context(), db.MoveTaskToColumnParams{
			ID: task.ID, ColumnID: colID,
			UserID: &u.ID,
		})
	}

	// Назначаем user_id задаче
	_ = s.queries.ClaimOrphanTasks(r.Context(), &u.ID)

	for _, tagName := range parsed.Tags {
		tag, err := s.queries.UpsertTag(r.Context(), tagName)
		if err != nil {
			continue
		}
		_ = s.queries.AttachTag(r.Context(), db.AttachTagParams{TaskID: task.ID, TagID: tag.ID})
	}

	jsonOK(w, task)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		Title    string   `json:"title"`
		Notes    string   `json:"notes"`
		Priority int16    `json:"priority"`
		Deadline string   `json:"deadline"`
		Tags     []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" {
		jsonError(w, nil, 400)
		return
	}
	var deadline pgtype.Timestamptz
	if body.Deadline != "" {
		t, err := time.Parse("2006-01-02T15:04", body.Deadline)
		if err == nil {
			deadline = pgtype.Timestamptz{Time: t, Valid: true}
		}
	}
	priority := body.Priority
	if priority == 0 {
		priority = 2
	}
	var notes *string
	if body.Notes != "" {
		notes = &body.Notes
	}
	if err := s.queries.UpdateTask(r.Context(), db.UpdateTaskParams{
		ID: id, Title: body.Title, Notes: notes,
		Priority: priority, Deadline: deadline,
		UserID: &u.ID,
	}); err != nil {
		jsonError(w, err, 500)
		return
	}
	_ = s.queries.DeleteTaskTags(r.Context(), id)
	for _, tagName := range body.Tags {
		tagName = strings.TrimSpace(strings.TrimPrefix(tagName, "#"))
		if tagName == "" {
			continue
		}
		tag, err := s.queries.UpsertTag(r.Context(), tagName)
		if err != nil {
			continue
		}
		_ = s.queries.AttachTag(r.Context(), db.AttachTagParams{TaskID: id, TagID: tag.ID})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) moveTask(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		ColumnID int64 `json:"column_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, err, 400)
		return
	}
	if err := s.queries.MoveTaskToColumn(r.Context(), db.MoveTaskToColumnParams{
		ID: id, ColumnID: body.ColumnID,
		UserID: &u.ID,
	}); err != nil {
		jsonError(w, err, 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := s.queries.DeleteTask(r.Context(), id); err != nil {
		jsonError(w, err, 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Columns ───────────────────────────────────────────────────────────────────

func (s *Server) listColumns(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	cols, err := s.queries.ListColumns(r.Context(), &u.ID)
	if err != nil {
		jsonError(w, err, 500)
		return
	}
	jsonOK(w, cols)
}

func (s *Server) createColumn(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		jsonError(w, nil, 400)
		return
	}
	if body.Color == "" {
		body.Color = "#94a3b8"
	}
	col, err := s.queries.CreateColumn(r.Context(), db.CreateColumnParams{
		Name:   body.Name,
		Color:  body.Color,
		UserID: &u.ID,
	})
	if err != nil {
		jsonError(w, err, 500)
		return
	}
	jsonOK(w, col)
}

func (s *Server) updateColumn(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		jsonError(w, nil, 400)
		return
	}
	if err := s.queries.UpdateColumn(r.Context(), db.UpdateColumnParams{
		ID: id, Name: body.Name, Color: body.Color,
		UserID: &u.ID,
	}); err != nil {
		jsonError(w, err, 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteColumn(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := s.queries.DeleteColumn(r.Context(), db.DeleteColumnParams{
		ID:     id,
		UserID: &u.ID,
	}); err != nil {
		jsonError(w, err, 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reorderColumn(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		Position int32 `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, err, 400)
		return
	}
	if err := s.queries.ReorderColumns(r.Context(), db.ReorderColumnsParams{
		ID: id, Position: body.Position,
		UserID: &u.ID,
	}); err != nil {
		jsonError(w, err, 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Projects ──────────────────────────────────────────────────────────────────

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.queries.ListProjects(r.Context())
	if err != nil {
		jsonError(w, err, 500)
		return
	}
	jsonOK(w, projects)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	msg := "error"
	if err != nil {
		msg = err.Error()
	}
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
