package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/aegorov/todo-bot/internal/db"
)

func strPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64 { return &i }
func derefStr(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

const sessionCookie = "session"
const sessionTTL = 30 * 24 * time.Hour

type Handler struct {
	cfg     *oauth2.Config
	queries *db.Queries
	baseURL string
}

type contextKey string

const UserKey contextKey = "user"

type SessionUser struct {
	ID             int64
	Email          string
	Name           string
	Avatar         string
	TelegramLinked bool
}

func New(clientID, clientSecret, baseURL string, q *db.Queries) *Handler {
	return &Handler{
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  baseURL + "/auth/callback",
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
		queries: q,
		baseURL: baseURL,
	}
}

// Login — редирект на Google
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	url := h.cfg.AuthCodeURL("state", oauth2.AccessTypeOnline)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// Callback — обработка ответа от Google
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	token, err := h.cfg.Exchange(r.Context(), code)
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusInternalServerError)
		return
	}

	info, err := googleUserInfo(r.Context(), h.cfg, token)
	if err != nil {
		http.Error(w, "failed to get user info", http.StatusInternalServerError)
		return
	}

	var avatarPtr *string
	if info.Picture != "" {
		avatarPtr = strPtr(info.Picture)
	}
	user, err := h.queries.UpsertUser(r.Context(), db.UpsertUserParams{
		GoogleID: info.ID,
		Email:    info.Email,
		Name:     info.Name,
		Avatar:   avatarPtr,
	})
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Первый вход — забираем orphan данные (созданные через бот)
	_ = h.queries.ClaimOrphanData(r.Context(), int64Ptr(user.ID))
	_ = h.queries.ClaimOrphanTasks(r.Context(), int64Ptr(user.ID))

	// Гарантируем что у пользователя есть обе системные колонки
	_, _ = h.queries.EnsureTodoColumn(r.Context(), int64Ptr(user.ID))
	_, _ = h.queries.EnsureDoneColumn(r.Context(), int64Ptr(user.ID))

	// Создаём сессию
	sessionToken := uuid.New().String()
	expires := time.Now().Add(sessionTTL)
	_, err = h.queries.CreateSession(r.Context(), db.CreateSessionParams{
		Token:     sessionToken,
		UserID:    user.ID,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	})
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionToken,
		Expires:  expires,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

// Logout — удаляем сессию
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookie)
	if err == nil {
		_ = h.queries.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:    sessionCookie,
		Value:   "",
		Expires: time.Unix(0, 0),
		Path:    "/",
	})
	http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
}

// Middleware — проверяем сессию, прокидываем user в context
func (h *Handler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
			return
		}
		sess, err := h.queries.GetSession(r.Context(), cookie.Value)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
			return
		}
		linked, _ := sess.TelegramLinked.(bool)
		user := &SessionUser{
			ID:             sess.UserID,
			Email:          sess.Email,
			Name:           sess.Name,
			Avatar:         derefStr(sess.Avatar),
			TelegramLinked: linked,
		}
		ctx := context.WithValue(r.Context(), UserKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// APIMiddleware — для API возвращает 401 вместо редиректа
func (h *Handler) APIMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		sess, err := h.queries.GetSession(r.Context(), cookie.Value)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		linked, _ := sess.TelegramLinked.(bool)
		user := &SessionUser{
			ID:             sess.UserID,
			Email:          sess.Email,
			Name:           sess.Name,
			Avatar:         derefStr(sess.Avatar),
			TelegramLinked: linked,
		}
		ctx := context.WithValue(r.Context(), UserKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func UserFromContext(ctx context.Context) *SessionUser {
	u, _ := ctx.Value(UserKey).(*SessionUser)
	return u
}

// googleUserInfo получает профиль пользователя от Google
type googleUser struct {
	ID      string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func googleUserInfo(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token) (*googleUser, error) {
	client := cfg.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var u googleUser
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("parse user info: %w", err)
	}
	return &u, nil
}
