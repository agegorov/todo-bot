package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/aegorov/todo-bot/internal/db"
)

type Notifier interface {
	SendReminder(chatID int64, taskTitle string, deadline time.Time)
	SendMessage(chatID int64, text string)
}

// ReminderStore — то, что нужно планировщику от db.Queries. Выделено интерфейсом,
// чтобы fireReminders/weeklyDigest можно было прогнать в тестах без реальной БД.
type ReminderStore interface {
	ListDueReminders(ctx context.Context) ([]db.ListDueRemindersRow, error)
	MarkReminderSent(ctx context.Context, id int64) error
	ListLinkedUsersOverdueCounts(ctx context.Context) ([]db.ListLinkedUsersOverdueCountsRow, error)
}

type Scheduler struct {
	cron    *cron.Cron
	queries ReminderStore
	bot     Notifier
}

func New(q ReminderStore, n Notifier) *Scheduler {
	c := cron.New(cron.WithSeconds())
	return &Scheduler{cron: c, queries: q, bot: n}
}

func (s *Scheduler) Start() {
	// Проверяем напоминания каждую минуту
	s.cron.AddFunc("0 * * * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.fireReminders(ctx)
	})

	// Еженедельный дайджест — пятница 18:00
	s.cron.AddFunc("0 0 18 * * 5", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.weeklyDigest(ctx)
	})

	s.cron.Start()
	log.Println("scheduler: started")
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}

func (s *Scheduler) fireReminders(ctx context.Context) {
	reminders, err := s.queries.ListDueReminders(ctx)
	if err != nil {
		log.Printf("scheduler: list reminders: %v", err)
		return
	}
	for _, r := range reminders {
		// ChatID может быть nil: задача создана только через веб, у её владельца
		// нет привязанного Telegram — доставить напоминание некуда, но помечаем
		// как отправленное, чтобы не гонять её в каждом тике.
		if r.ChatID != nil {
			s.bot.SendReminder(*r.ChatID, r.TaskTitle, r.TaskDeadline.Time)
		}
		if err := s.queries.MarkReminderSent(ctx, r.ID); err != nil {
			log.Printf("scheduler: mark sent %d: %v", r.ID, err)
		}
	}
}

func (s *Scheduler) weeklyDigest(ctx context.Context) {
	rows, err := s.queries.ListLinkedUsersOverdueCounts(ctx)
	if err != nil {
		log.Printf("scheduler: weekly digest: %v", err)
		return
	}
	for _, row := range rows {
		if row.TelegramID == nil {
			continue
		}
		if row.OverdueCount == 0 {
			s.bot.SendMessage(*row.TelegramID, "Дайджест: просроченных задач нет 🎉")
			continue
		}
		s.bot.SendMessage(*row.TelegramID, fmt.Sprintf("Еженедельный дайджест: %d просроченных задач", row.OverdueCount))
	}
}
