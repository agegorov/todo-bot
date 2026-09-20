package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/aegorov/todo-bot/internal/db"
)

// fakeStore — минимальная реализация ReminderStore для тестов, без реальной БД.
type fakeStore struct {
	dueReminders []db.ListDueRemindersRow
	sentIDs      []int64
	overdue      []db.ListLinkedUsersOverdueCountsRow
}

func (f *fakeStore) ListDueReminders(ctx context.Context) ([]db.ListDueRemindersRow, error) {
	return f.dueReminders, nil
}

func (f *fakeStore) MarkReminderSent(ctx context.Context, id int64) error {
	f.sentIDs = append(f.sentIDs, id)
	return nil
}

func (f *fakeStore) ListLinkedUsersOverdueCounts(ctx context.Context) ([]db.ListLinkedUsersOverdueCountsRow, error) {
	return f.overdue, nil
}

type sentReminder struct {
	chatID int64
	title  string
}

type sentMessage struct {
	chatID int64
	text   string
}

// fakeNotifier записывает всё, что бот "отправил бы" в Telegram.
type fakeNotifier struct {
	reminders []sentReminder
	messages  []sentMessage
}

func (f *fakeNotifier) SendReminder(chatID int64, taskTitle string, deadline time.Time) {
	f.reminders = append(f.reminders, sentReminder{chatID: chatID, title: taskTitle})
}

func (f *fakeNotifier) SendMessage(chatID int64, text string) {
	f.messages = append(f.messages, sentMessage{chatID: chatID, text: text})
}

func int64p(v int64) *int64 { return &v }

func TestFireReminders_SendsToOwnersChatAndAlwaysMarksSent(t *testing.T) {
	store := &fakeStore{
		dueReminders: []db.ListDueRemindersRow{
			{ID: 1, TaskTitle: "Задача A", ChatID: int64p(501)},
			// ChatID == nil: задача только из веба, у владельца нет Telegram —
			// напоминание никуда не летит, но всё равно должно быть помечено sent,
			// иначе будет вечно попадать в выборку due-напоминаний.
			{ID: 2, TaskTitle: "Задача без Telegram", ChatID: nil},
		},
	}
	notifier := &fakeNotifier{}
	s := New(store, notifier)

	s.fireReminders(context.Background())

	if len(notifier.reminders) != 1 {
		t.Fatalf("отправлено напоминаний = %d, want 1", len(notifier.reminders))
	}
	if notifier.reminders[0].chatID != 501 || notifier.reminders[0].title != "Задача A" {
		t.Errorf("напоминание отправлено %+v, want chatID=501 title=Задача A", notifier.reminders[0])
	}
	if len(store.sentIDs) != 2 || store.sentIDs[0] != 1 || store.sentIDs[1] != 2 {
		t.Errorf("помечены отправленными %v, want [1 2] (обе, включая недоставленную)", store.sentIDs)
	}
}

func TestWeeklyDigest_PerUserAndSkipsUnlinked(t *testing.T) {
	store := &fakeStore{
		overdue: []db.ListLinkedUsersOverdueCountsRow{
			{TelegramID: int64p(501), OverdueCount: 0},
			{TelegramID: int64p(502), OverdueCount: 12}, // регресс: было string(rune('0'+n)) — ломалось на двузначных
			{TelegramID: nil, OverdueCount: 3},          // не привязан к Telegram — некуда слать
		},
	}
	notifier := &fakeNotifier{}
	s := New(store, notifier)

	s.weeklyDigest(context.Background())

	if len(notifier.messages) != 2 {
		t.Fatalf("отправлено сообщений = %d, want 2 (для %d и %d telegram_id)", len(notifier.messages), 501, 502)
	}
	if notifier.messages[0].chatID != 501 || notifier.messages[0].text != "Дайджест: просроченных задач нет 🎉" {
		t.Errorf("сообщение для 501 = %+v", notifier.messages[0])
	}
	want502 := "Еженедельный дайджест: 12 просроченных задач"
	if notifier.messages[1].chatID != 502 || notifier.messages[1].text != want502 {
		t.Errorf("сообщение для 502 = %+v, want text=%q", notifier.messages[1], want502)
	}
}
