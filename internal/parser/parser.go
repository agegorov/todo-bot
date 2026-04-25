package parser

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

type ParsedTask struct {
	Title       string
	Notes       string
	Project     string // Inbox|Work|Home|Personal
	Tags        []string
	Priority    int  // 1=high 2=medium 3=low
	Deadline    time.Time
	HasDeadline bool
	DelegatedTo string
	IsRecurring bool
	RecurRule   string
}

var (
	reTag      = regexp.MustCompile(`#(\S+)`)
	reTime     = regexp.MustCompile(`(?i)в\s+(\d{1,2})[:\.](\d{2})`)
	reDate     = regexp.MustCompile(`(?i)(\d{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)`)
	reDelegate = regexp.MustCompile(`(?i)(?:попросить|делегировать|скажи|напомни)\s+([А-ЯЁа-яёA-Za-z]+)\s+`)
)

var monthMap = map[string]time.Month{
	"января": time.January, "февраля": time.February, "марта": time.March,
	"апреля": time.April, "мая": time.May, "июня": time.June,
	"июля": time.July, "августа": time.August, "сентября": time.September,
	"октября": time.October, "ноября": time.November, "декабря": time.December,
}

var weekdayMap = map[string]time.Weekday{
	"понедельник": time.Monday, "вторник": time.Tuesday, "среду": time.Wednesday,
	"среда": time.Wednesday, "четверг": time.Thursday, "пятницу": time.Friday,
	"пятница": time.Friday, "субботу": time.Saturday, "суббота": time.Saturday,
	"воскресенье": time.Sunday,
}

var recurPatterns = map[string]string{
	"каждый день":        "0 9 * * *",
	"каждое утро":        "0 9 * * *",
	"каждый вечер":       "0 19 * * *",
	"каждую неделю":      "0 9 * * 1",
	"каждый понедельник": "0 9 * * 1",
	"каждый вторник":     "0 9 * * 2",
	"каждую среду":       "0 9 * * 3",
	"каждый четверг":     "0 9 * * 4",
	"каждую пятницу":     "0 9 * * 5",
	"каждую субботу":     "0 9 * * 6",
	"каждое воскресенье": "0 9 * * 0",
}

var workKeywords = []string{
	"ревью", "pr", "встреча", "созвон", "команда", "проект", "задача", "спринт",
	"деплой", "баг", "фикс", "релиз", "стендап", "митинг", "коллега", "босс",
	"офис", "работа", "клиент", "презентация", "отчёт", "дедлайн",
}

var homeKeywords = []string{
	"купить", "молоко", "хлеб", "продукты", "магазин", "уборка", "убрать",
	"готовить", "ужин", "обед", "завтрак", "аптека", "врач", "дом", "квартира",
	"ремонт", "счёт", "оплатить", "коммуналка",
}

var highPriorityWords = []string{"срочно", "срочный", "важно", "важный", "критично", "asap", "горит"}
var lowPriorityWords = []string{"когда-нибудь", "не срочно", "потом", "когда будет время"}

// Parse извлекает задачу из произвольного текста на русском.
func Parse(text string, now time.Time) *ParsedTask {
	task := &ParsedTask{
		Priority: 2,
		Project:  "Inbox",
	}

	lower := strings.ToLower(text)

	// --- Теги ---
	for _, m := range reTag.FindAllStringSubmatch(text, -1) {
		task.Tags = append(task.Tags, m[1])
	}
	clean := reTag.ReplaceAllString(text, "")

	// --- Повторяющиеся задачи ---
	for pattern, cron := range recurPatterns {
		if strings.Contains(lower, pattern) {
			task.IsRecurring = true
			task.RecurRule = cron
			clean = strings.ReplaceAll(clean, pattern, "")
			break
		}
	}

	// --- Делегирование ---
	if m := reDelegate.FindStringSubmatch(clean); m != nil {
		task.DelegatedTo = m[1]
		clean = reDelegate.ReplaceAllString(clean, "")
	}

	// --- Приоритет ---
	for _, w := range highPriorityWords {
		if strings.Contains(lower, w) {
			task.Priority = 1
			clean = strings.ReplaceAll(clean, w, "")
			break
		}
	}
	for _, w := range lowPriorityWords {
		if strings.Contains(lower, w) {
			task.Priority = 3
			break
		}
	}

	// --- Дедлайн: время ---
	hour, min := 9, 0
	if m := reTime.FindStringSubmatch(clean); m != nil {
		hour = atoi(m[1])
		min = atoi(m[2])
		clean = reTime.ReplaceAllString(clean, "")
	}

	// --- Дедлайн: дата ---
	deadline := parseDeadline(lower, now, hour, min)
	if !deadline.IsZero() {
		task.Deadline = deadline
		task.HasDeadline = true
		// убираем ключевые слова дат из заголовка
		clean = removeDateKeywords(clean)
	}

	// --- Проект ---
	task.Project = detectProject(lower, task.Tags)

	// --- Заголовок — что осталось ---
	task.Title = cleanTitle(clean)
	if task.Title == "" {
		task.Title = cleanTitle(text)
	}

	return task
}

func parseDeadline(lower string, now time.Time, hour, min int) time.Time {
	at := func(d time.Time) time.Time {
		return time.Date(d.Year(), d.Month(), d.Day(), hour, min, 0, 0, d.Location())
	}

	switch {
	case strings.Contains(lower, "сегодня"):
		return at(now)
	case strings.Contains(lower, "завтра"):
		return at(now.AddDate(0, 0, 1))
	case strings.Contains(lower, "послезавтра"):
		return at(now.AddDate(0, 0, 2))
	case strings.Contains(lower, "на следующей неделе"):
		return at(now.AddDate(0, 0, 7))
	}

	// "в понедельник" и т.д.
	for word, wd := range weekdayMap {
		if strings.Contains(lower, word) {
			d := nextWeekday(now, wd)
			return at(d)
		}
	}

	// "15 мая", "3 июня"
	if m := reDate.FindStringSubmatch(lower); m != nil {
		day := atoi(m[1])
		month := monthMap[m[2]]
		year := now.Year()
		d := time.Date(year, month, day, hour, min, 0, 0, now.Location())
		if d.Before(now) {
			d = d.AddDate(1, 0, 0)
		}
		return d
	}

	return time.Time{}
}

func nextWeekday(from time.Time, wd time.Weekday) time.Time {
	d := from.AddDate(0, 0, 1)
	for d.Weekday() != wd {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

func detectProject(lower string, tags []string) string {
	for _, t := range tags {
		switch strings.ToLower(t) {
		case "работа", "work":
			return "Work"
		case "дом", "home":
			return "Home"
		case "личное", "personal":
			return "Personal"
		}
	}
	for _, w := range workKeywords {
		if strings.Contains(lower, w) {
			return "Work"
		}
	}
	for _, w := range homeKeywords {
		if strings.Contains(lower, w) {
			return "Home"
		}
	}
	return "Inbox"
}

var dateWords = []string{
	"сегодня", "завтра", "послезавтра", "на следующей неделе",
	"в понедельник", "в вторник", "в среду", "в четверг", "в пятницу", "в субботу", "в воскресенье",
}

func removeDateKeywords(s string) string {
	lower := strings.ToLower(s)
	for _, w := range dateWords {
		idx := strings.Index(lower, w)
		if idx >= 0 {
			s = s[:idx] + s[idx+len(w):]
			lower = strings.ToLower(s)
		}
	}
	return s
}

func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	// убираем лишние пробелы
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r)
	})
	title := strings.Join(fields, " ")
	// капитализируем первую букву
	if len(title) > 0 {
		runes := []rune(title)
		runes[0] = unicode.ToUpper(runes[0])
		title = string(runes)
	}
	return title
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}
