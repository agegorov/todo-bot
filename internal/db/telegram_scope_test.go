package db_test

// Интеграционные тесты для запросов, обслуживающих мультипользовательский
// Telegram-бот (после снятия owner-гейта): видимость задач по scope и защита
// /done от закрытия чужой задачи. Требуют реальной Postgres с накатанными
// миграциями (internal/db/migrations) — без TODOBOT_TEST_DATABASE_URL пропускаются.
//
// Пример запуска локально:
//   createdb todobot_test
//   for f in internal/db/migrations/*.sql; do psql todobot_test -f "$f"; done
//   TODOBOT_TEST_DATABASE_URL=postgres://localhost:5432/todobot_test go test ./internal/db/...

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aegorov/todo-bot/internal/db"
)

// withTx открывает соединение и оборачивает тест в транзакцию, которая
// откатывается по завершении — тестовые данные никогда не остаются в БД.
func withTx(t *testing.T) *db.Queries {
	t.Helper()
	url := os.Getenv("TODOBOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TODOBOT_TEST_DATABASE_URL не задан — пропускаю интеграционный тест")
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	return db.New(pool).WithTx(tx)
}

func int64p(v int64) *int64 { return &v }

func containsID(rows []int64, id int64) bool {
	for _, r := range rows {
		if r == id {
			return true
		}
	}
	return false
}

// TestListOpenTasksForTelegram_ScopesToOwnerOrOrphan проверяет, что /list (и /today,
// /overdue — тот же WHERE) отдают либо задачи привязанного веб-аккаунта, либо
// собственные orphan-задачи отправителя, и никогда — чужие.
func TestListOpenTasksForTelegram_ScopesToOwnerOrOrphan(t *testing.T) {
	ctx := context.Background()
	q := withTx(t)

	user, err := q.UpsertUser(ctx, db.UpsertUserParams{
		GoogleID: "g-scope-test", Email: "scope@example.com", Name: "Scope",
	})
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	own, err := q.CreateTask(ctx, db.CreateTaskParams{
		ProjectID: 1, Title: "Своя задача", Priority: 2, UserID: &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask own: %v", err)
	}

	orphan, err := q.CreateTask(ctx, db.CreateTaskParams{
		ProjectID: 1, Title: "Чужая orphan-задача", Priority: 2, TelegramUserID: int64p(999999),
	})
	if err != nil {
		t.Fatalf("CreateTask orphan: %v", err)
	}

	// Привязанный юзер видит свою задачу и не видит чужую orphan.
	linked, err := q.ListOpenTasksForTelegram(ctx, db.ListOpenTasksForTelegramParams{
		UserID: &user.ID, TelegramUserID: 123456,
	})
	if err != nil {
		t.Fatalf("ListOpenTasksForTelegram (linked): %v", err)
	}
	var linkedIDs []int64
	for _, r := range linked {
		linkedIDs = append(linkedIDs, r.ID)
	}
	if !containsID(linkedIDs, own.ID) {
		t.Errorf("привязанный юзер не увидел свою задачу #%d среди %v", own.ID, linkedIDs)
	}
	if containsID(linkedIDs, orphan.ID) {
		t.Errorf("привязанный юзер увидел чужую orphan-задачу #%d", orphan.ID)
	}

	// Отправитель-владелец orphan-задачи видит её, но не задачу другого юзера.
	ownOrphan, err := q.ListOpenTasksForTelegram(ctx, db.ListOpenTasksForTelegramParams{
		UserID: nil, TelegramUserID: 999999,
	})
	if err != nil {
		t.Fatalf("ListOpenTasksForTelegram (orphan owner): %v", err)
	}
	var ownOrphanIDs []int64
	for _, r := range ownOrphan {
		ownOrphanIDs = append(ownOrphanIDs, r.ID)
	}
	if !containsID(ownOrphanIDs, orphan.ID) {
		t.Errorf("владелец orphan-задачи не увидел #%d среди %v", orphan.ID, ownOrphanIDs)
	}
	if containsID(ownOrphanIDs, own.ID) {
		t.Errorf("владелец orphan-задачи увидел чужую привязанную задачу #%d", own.ID)
	}

	// Посторонний непривязанный Telegram-юзер не видит ничьих задач.
	stranger, err := q.ListOpenTasksForTelegram(ctx, db.ListOpenTasksForTelegramParams{
		UserID: nil, TelegramUserID: 111111,
	})
	if err != nil {
		t.Fatalf("ListOpenTasksForTelegram (stranger): %v", err)
	}
	for _, r := range stranger {
		if r.ID == own.ID || r.ID == orphan.ID {
			t.Errorf("посторонний увидел задачу #%d, не принадлежащую ему", r.ID)
		}
	}
}

// TestCompleteOrphanTaskForTelegram_EnforcesOwnership — ключевая проверка для этой
// правки: после снятия owner-гейта из бота /done не должен позволять закрыть чужую
// orphan-задачу, просто подобрав id.
func TestCompleteOrphanTaskForTelegram_EnforcesOwnership(t *testing.T) {
	ctx := context.Background()
	q := withTx(t)

	task, err := q.CreateTask(ctx, db.CreateTaskParams{
		ProjectID: 1, Title: "Orphan для /done", Priority: 2, TelegramUserID: int64p(777),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	affected, err := q.CompleteOrphanTaskForTelegram(ctx, db.CompleteOrphanTaskForTelegramParams{
		ID: task.ID, TelegramUserID: 888, // не тот отправитель
	})
	if err != nil {
		t.Fatalf("CompleteOrphanTaskForTelegram (stranger): %v", err)
	}
	if affected != 0 {
		t.Fatalf("посторонний закрыл чужую задачу #%d: affected=%d, want 0", task.ID, affected)
	}

	affected, err = q.CompleteOrphanTaskForTelegram(ctx, db.CompleteOrphanTaskForTelegramParams{
		ID: task.ID, TelegramUserID: 777, // владелец
	})
	if err != nil {
		t.Fatalf("CompleteOrphanTaskForTelegram (owner): %v", err)
	}
	if affected != 1 {
		t.Fatalf("владелец не смог закрыть свою задачу #%d: affected=%d, want 1", task.ID, affected)
	}

	// Повторное закрытие уже закрытой задачи ничего не делает.
	affected, err = q.CompleteOrphanTaskForTelegram(ctx, db.CompleteOrphanTaskForTelegramParams{
		ID: task.ID, TelegramUserID: 777,
	})
	if err != nil {
		t.Fatalf("CompleteOrphanTaskForTelegram (already done): %v", err)
	}
	if affected != 0 {
		t.Fatalf("повторное закрытие вернуло affected=%d, want 0", affected)
	}
}

// TestListDueReminders_ChatIDPrefersLinkedUser проверяет, что напоминание уходит на
// telegram_id привязанного веб-аккаунта, а не в чат, где была написана задача.
func TestListDueReminders_ChatIDPrefersLinkedUser(t *testing.T) {
	ctx := context.Background()
	q := withTx(t)

	tgID := int64(424242)
	user, err := q.UpsertUser(ctx, db.UpsertUserParams{
		GoogleID: "g-remind-test", Email: "remind@example.com", Name: "Remind",
	})
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := q.SetUserTelegramID(ctx, db.SetUserTelegramIDParams{ID: user.ID, TelegramID: &tgID}); err != nil {
		t.Fatalf("SetUserTelegramID: %v", err)
	}

	task, err := q.CreateTask(ctx, db.CreateTaskParams{
		ProjectID: 1, Title: "С напоминанием", Priority: 2, UserID: &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := q.CreateReminder(ctx, db.CreateReminderParams{
		TaskID:   task.ID,
		RemindAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}

	due, err := q.ListDueReminders(ctx)
	if err != nil {
		t.Fatalf("ListDueReminders: %v", err)
	}
	var found *db.ListDueRemindersRow
	for i := range due {
		if due[i].TaskID == task.ID {
			found = &due[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("напоминание для задачи #%d не найдено среди due-напоминаний", task.ID)
	}
	if found.ChatID == nil || *found.ChatID != tgID {
		t.Errorf("ChatID = %v, want %d (telegram_id привязанного юзера)", found.ChatID, tgID)
	}
}
