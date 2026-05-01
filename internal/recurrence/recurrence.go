// Package recurrence содержит логику клонирования периодических задач при их закрытии.
package recurrence

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/aegorov/todo-bot/internal/db"
)

// NextDeadline возвращает следующий дедлайн исходя из правила и базовой даты.
// Если правило неизвестно — возвращает нулевое время.
func NextDeadline(rule string, base time.Time) time.Time {
	switch rule {
	case "daily":
		return base.AddDate(0, 0, 1)
	case "weekly":
		return base.AddDate(0, 0, 7)
	case "biweekly":
		return base.AddDate(0, 0, 14)
	case "monthly":
		return base.AddDate(0, 1, 0)
	case "yearly":
		return base.AddDate(1, 0, 0)
	}
	return time.Time{}
}

// IsValidRule — известное ли это правило.
func IsValidRule(rule string) bool {
	return !NextDeadline(rule, time.Now()).IsZero()
}

// Spawn создаёт следующую итерацию периодической задачи.
// Копирует поля, переносит теги, кладёт в TO DO колонку. Возвращает ID новой задачи.
// Если задача не повторяющаяся или нет правила — ничего не делает (id=0, err=nil).
func Spawn(ctx context.Context, q *db.Queries, task db.Task, userID int64) (int64, error) {
	if !task.IsRecurring || task.RecurRule == nil {
		return 0, nil
	}
	rule := *task.RecurRule
	if !IsValidRule(rule) {
		return 0, nil
	}

	// Якорь — старый дедлайн, иначе now()
	anchor := time.Now()
	if task.Deadline.Valid {
		anchor = task.Deadline.Time
	}
	next := NextDeadline(rule, anchor)
	if next.IsZero() {
		return 0, nil
	}

	deadline := pgtype.Timestamptz{Time: next, Valid: true}

	uid := userID
	newTask, err := q.CreateTask(ctx, db.CreateTaskParams{
		ProjectID:      task.ProjectID,
		Title:          task.Title,
		Notes:          task.Notes,
		Priority:       task.Priority,
		Deadline:       deadline,
		DelegatedTo:    task.DelegatedTo,
		IsRecurring:    true,
		RecurRule:      task.RecurRule,
		UserID:         &uid,
		TelegramUserID: task.TelegramUserID,
	})
	if err != nil {
		return 0, err
	}

	// Перенос тегов
	if tags, err := q.ListTagsForTask(ctx, task.ID); err == nil {
		for _, t := range tags {
			_ = q.AttachTag(ctx, db.AttachTagParams{TaskID: newTask.ID, TagID: t.ID})
		}
	}

	// Кладём в TO DO колонку
	if todoCol, err := q.EnsureTodoColumn(ctx, &uid); err == nil {
		_ = q.MoveTaskToColumn(ctx, db.MoveTaskToColumnParams{
			ID: newTask.ID, ColumnID: todoCol.ID, UserID: &uid,
		})
	}

	// Напоминание за час до дедлайна (как делает бот при создании)
	remind := next.Add(-time.Hour)
	if remind.After(time.Now()) {
		_, _ = q.CreateReminder(ctx, db.CreateReminderParams{
			TaskID:   newTask.ID,
			RemindAt: pgtype.Timestamptz{Time: remind, Valid: true},
		})
	}

	return newTask.ID, nil
}
