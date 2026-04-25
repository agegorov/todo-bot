package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/aegorov/todo-bot/internal/db"
	"github.com/aegorov/todo-bot/internal/parser"
)

type Server struct {
	queries *db.Queries
}

func New(q *db.Queries) *Server {
	return &Server{queries: q}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PATCH", "DELETE"},
		AllowedHeaders: []string{"Content-Type"},
	}))

	// Статические файлы
	r.Handle("/*", http.FileServer(http.Dir("web")))

	// API
	r.Route("/api", func(r chi.Router) {
		r.Get("/tasks", s.listTasks)
		r.Post("/tasks", s.createTask)
		r.Patch("/tasks/{id}/status", s.updateStatus)
		r.Delete("/tasks/{id}", s.deleteTask)
		r.Get("/projects", s.listProjects)
	})

	return r
}

// GET /api/tasks
func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.queries.ListTasksByStatus(r.Context())
	if err != nil {
		jsonError(w, err, 500)
		return
	}

	type taskResponse struct {
		ID          int64   `json:"id"`
		Title       string  `json:"title"`
		Notes       *string `json:"notes"`
		Priority    int16   `json:"priority"`
		Deadline    *string `json:"deadline"`
		Status      string  `json:"status"`
		ProjectName string  `json:"project_name"`
		ProjectColor string `json:"project_color"`
		DelegatedTo *string `json:"delegated_to"`
	}

	resp := make([]taskResponse, len(tasks))
	for i, t := range tasks {
		var dl *string
		if t.Deadline.Valid {
			s := t.Deadline.Time.Format("02 Jan 15:04")
			dl = &s
		}
		resp[i] = taskResponse{
			ID:           t.ID,
			Title:        t.Title,
			Notes:        t.Notes,
			Priority:     t.Priority,
			Deadline:     dl,
			Status:       t.Status,
			ProjectName:  t.ProjectName,
			ProjectColor: t.ProjectColor,
			DelegatedTo:  t.DelegatedTo,
		}
	}
	jsonOK(w, resp)
}

// POST /api/tasks
func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		jsonError(w, err, 400)
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
	jsonOK(w, task)
}

// PATCH /api/tasks/{id}/status
func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		jsonError(w, err, 400)
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, err, 400)
		return
	}
	if body.Status == "done" {
		if err := s.queries.CompleteTask(r.Context(), id); err != nil {
			jsonError(w, err, 500)
			return
		}
	}
	if err := s.queries.UpdateTaskStatus(r.Context(), db.UpdateTaskStatusParams{
		ID:     id,
		Status: body.Status,
	}); err != nil {
		jsonError(w, err, 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/tasks/{id}
func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		jsonError(w, err, 400)
		return
	}
	if err := s.queries.DeleteTask(r.Context(), id); err != nil {
		jsonError(w, err, 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/projects
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.queries.ListProjects(r.Context())
	if err != nil {
		jsonError(w, err, 500)
		return
	}
	jsonOK(w, projects)
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	}
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
