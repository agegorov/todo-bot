package bot

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/aegorov/todo-bot/internal/db"
)

func helpText() string {
	return `*Todo Bot* 🤖

Просто отправь текст или голосовое — я создам задачу.

*Команды:*
/list — все открытые задачи
/today — задачи на сегодня
/overdue — просроченные
/done <id> — закрыть задачу
/projects — список проектов
/help — эта справка

*Примеры:*
• "купить молоко завтра утром"
• "ревью PR от Пети до пятницы #работа"
• "каждый понедельник стендап в 10:00"
• 🎤 голосовое сообщение во время пробежки`
}

var priorityEmoji = map[int16]string{1: "🔴", 2: "🟡", 3: "🟢"}

func formatTaskCreated(t *db.Task) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ Задача #%d создана\n", t.ID))
	sb.WriteString(fmt.Sprintf("*%s*\n", t.Title))
	if t.Deadline.Valid {
		sb.WriteString(fmt.Sprintf("📅 %s\n", t.Deadline.Time.Format("02 Jan 15:04")))
	}
	if emoji, ok := priorityEmoji[t.Priority]; ok {
		sb.WriteString(fmt.Sprintf("Приоритет: %s\n", emoji))
	}
	if t.DelegatedTo != nil {
		sb.WriteString(fmt.Sprintf("👤 Делегировано: %s\n", *t.DelegatedTo))
	}
	return sb.String()
}

// taskRow — общий интерфейс для всех *Row типов из sqlc.
type taskRow struct {
	ID          int64
	Title       string
	Priority    int16
	Deadline    pgtype.Timestamptz
	DelegatedTo *string
}

func toRows[T any](tasks []T, fn func(T) taskRow) []taskRow {
	rows := make([]taskRow, len(tasks))
	for i, t := range tasks {
		rows[i] = fn(t)
	}
	return rows
}

func openTaskRow(t db.ListOpenTasksRow) taskRow {
	return taskRow{ID: t.ID, Title: t.Title, Priority: t.Priority, Deadline: t.Deadline, DelegatedTo: t.DelegatedTo}
}

func todayTaskRow(t db.ListTodayTasksRow) taskRow {
	return taskRow{ID: t.ID, Title: t.Title, Priority: t.Priority, Deadline: t.Deadline, DelegatedTo: t.DelegatedTo}
}

func overdueTaskRow(t db.ListOverdueTasksRow) taskRow {
	return taskRow{ID: t.ID, Title: t.Title, Priority: t.Priority, Deadline: t.Deadline, DelegatedTo: t.DelegatedTo}
}

func formatRows(title string, rows []taskRow) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*%s* (%d)\n\n", title, len(rows)))
	for _, t := range rows {
		emoji := priorityEmoji[t.Priority]
		deadline := ""
		if t.Deadline.Valid {
			deadline = " · 📅 " + t.Deadline.Time.Format("02 Jan")
		}
		sb.WriteString(fmt.Sprintf("%s `#%d` %s%s\n", emoji, t.ID, t.Title, deadline))
	}
	return sb.String()
}

func formatOpenTasks(title string, tasks []db.ListOpenTasksRow) string {
	return formatRows(title, toRows(tasks, openTaskRow))
}

func formatTodayTasks(title string, tasks []db.ListTodayTasksRow) string {
	return formatRows(title, toRows(tasks, todayTaskRow))
}

func formatOverdueTasks(title string, tasks []db.ListOverdueTasksRow) string {
	return formatRows(title, toRows(tasks, overdueTaskRow))
}
