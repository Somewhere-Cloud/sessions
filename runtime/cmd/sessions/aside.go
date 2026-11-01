package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const asideUsage = "usage: sessions aside <session> [session...] [--until 2h|18:00|tomorrow 09:00|reset] [--clear]"

type asideItem struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Until is when the daemon brings the session back, in Unix ms.
	Until *int64 `json:"until,omitempty"`
}

type asideResult struct {
	Items []asideItem `json:"items"`
}

type asideRequest struct {
	clear      bool
	until      *int64
	untilReset bool
	targets    []string
}

func (a *app) cmdAside(args []string) error {
	request, err := parseAsideArgs(args, time.Now())
	if err != nil {
		return err
	}
	ids, err := a.resolveAsideTargets(request.targets)
	if err != nil {
		return err
	}
	result := asideResult{Items: make([]asideItem, 0, len(ids))}
	changed := 0
	for _, id := range ids {
		item, err := a.asideOne(id, request)
		if err != nil {
			return err
		}
		if item.Status != "skipped" {
			changed++
		}
		result.Items = append(result.Items, item)
	}
	if err := a.writeAsideResult(result); err != nil {
		return err
	}
	if changed == 0 {
		return fail(2, "no live sessions changed working-set state")
	}
	return nil
}

func parseAsideArgs(args []string, now time.Time) (asideRequest, error) {
	var request asideRequest
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--clear":
			request.clear = true
		case arg == "--until":
			if index+1 >= len(args) {
				return request, fail(1, "--until needs a time such as 2h, 18:00, tomorrow 09:00, or reset")
			}
			value := args[index+1]
			index++
			if index+1 < len(args) && clockRE.MatchString(args[index+1]) && dayWord(value) {
				value += " " + args[index+1]
				index++
			}
			if strings.EqualFold(value, "reset") {
				request.untilReset = true
				continue
			}
			at, err := parseReturnTime(value, now)
			if err != nil {
				return request, fail(1, "%v", err)
			}
			ms := at.UnixMilli()
			request.until = &ms
		case strings.HasPrefix(arg, "-"):
			return request, fail(1, "%s", asideUsage)
		default:
			request.targets = append(request.targets, arg)
		}
	}
	if len(request.targets) == 0 {
		return request, fail(1, "%s", asideUsage)
	}
	if request.clear && (request.until != nil || request.untilReset) {
		return request, fail(1, "--until sets a session aside until a time; it cannot be combined with --clear")
	}
	return request, nil
}

func (a *app) resolveAsideTargets(targets []string) ([]string, error) {
	ids := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		id, err := a.resolveSessionID(target)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func (a *app) asideOne(id string, request asideRequest) (asideItem, error) {
	path := "/api/sessions/" + escapeID(id) + "/set-aside"
	body := map[string]any{"setAside": !request.clear}
	if request.until != nil {
		body["until"] = *request.until
	}
	if request.untilReset {
		body["untilLimitReset"] = true
	}
	response, err := a.api.request(context.Background(), http.MethodPut, path, body, 0)
	if err != nil {
		return asideItem{}, fail(2, "%s → %s", path, err)
	}
	var payload struct {
		Error         string `json:"error"`
		SetAsideUntil *int64 `json:"setAsideUntil"`
	}
	_ = json.Unmarshal(response.body, &payload)
	if response.status >= 400 {
		if response.status == http.StatusConflict && payload.Error != "" {
			return asideItem{ID: id, Status: "skipped", Reason: payload.Error}, nil
		}
		if payload.Error != "" {
			return asideItem{}, fail(2, "%s", payload.Error)
		}
		return asideItem{}, fail(2, "%s → %d %s", path, response.status, prefixBytes(response.body, 200))
	}
	name := ""
	if current, resolveErr := a.resolveStatusSession(id); resolveErr == nil {
		name = strings.TrimSpace(current.Name)
	}
	status := "set-aside"
	if request.clear {
		status = "brought-back"
	}
	return asideItem{ID: id, Status: status, Name: name, Until: payload.SetAsideUntil}, nil
}

func (a *app) writeAsideResult(result asideResult) error {
	if a.wantJSON {
		return writeJSON(a.stdout, result, true)
	}
	for _, item := range result.Items {
		if item.Status == "skipped" {
			fmt.Fprintf(a.stdout, "skipped       %s  %s\n", prefixString(item.ID, 8), item.Reason)
			continue
		}
		line := fmt.Sprintf("%-13s %s  %s", item.Status, prefixString(item.ID, 8), item.Name)
		if item.Until != nil {
			line += "  (back " + returnTimeLabel(*item.Until, time.Now()) + ")"
		}
		fmt.Fprintln(a.stdout, strings.TrimRight(line, " "))
	}
	return nil
}

var (
	clockRE      = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	daysRE       = regexp.MustCompile(`^(\d+)d(.*)$`)
	weekdayNames = map[string]time.Weekday{
		"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
		"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
	}
)

func dayWord(value string) bool {
	lower := strings.ToLower(value)
	if lower == "today" || lower == "tomorrow" {
		return true
	}
	if len(lower) >= 3 {
		_, ok := weekdayNames[lower[:3]]
		return ok
	}
	_, err := time.ParseInLocation("2006-01-02", value, time.Local)
	return err == nil
}

// parseReturnTime reads when a set-aside session comes back: a duration
// (90m, 2h, 3d), a clock time (18:00, the next one), a day with an optional
// clock time (tomorrow, tomorrow 09:00, mon 09:00, 2026-11-03 09:00; 09:00 when
// omitted), or an RFC 3339 time.
func parseReturnTime(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if delay, ok := parseReturnDelay(value); ok {
		return now.Add(delay), nil
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at, nil
	}
	fields := strings.Fields(value)
	clock := "09:00"
	switch {
	case len(fields) == 1 && clockRE.MatchString(fields[0]):
		at, err := atClock(now, fields[0])
		if err == nil && !at.After(now) {
			at = at.AddDate(0, 0, 1)
		}
		return at, err
	case len(fields) == 2 && clockRE.MatchString(fields[1]):
		clock = fields[1]
	case len(fields) != 1:
		return time.Time{}, fmt.Errorf("cannot read return time %q; use 2h, 18:00, tomorrow 09:00, mon 09:00, or 2026-11-03 09:00", value)
	}
	day, err := returnDay(fields[0], now)
	if err != nil {
		return time.Time{}, err
	}
	return atClock(day, clock)
}

func parseReturnDelay(value string) (time.Duration, bool) {
	var days time.Duration
	rest := value
	if match := daysRE.FindStringSubmatch(value); match != nil {
		count, _ := strconv.Atoi(match[1])
		days, rest = time.Duration(count)*24*time.Hour, match[2]
	}
	if rest == "" {
		return days, days > 0
	}
	delay, err := time.ParseDuration(rest)
	if err != nil || delay+days <= 0 {
		return 0, false
	}
	return delay + days, true
}

func returnDay(word string, now time.Time) (time.Time, error) {
	lower := strings.ToLower(word)
	switch lower {
	case "today":
		return now, nil
	case "tomorrow":
		return now.AddDate(0, 0, 1), nil
	}
	if len(lower) >= 3 {
		if weekday, ok := weekdayNames[lower[:3]]; ok {
			ahead := (int(weekday) - int(now.Weekday()) + 7) % 7
			if ahead == 0 {
				ahead = 7
			}
			return now.AddDate(0, 0, ahead), nil
		}
	}
	day, err := time.ParseInLocation("2006-01-02", word, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("cannot read day %q; use today, tomorrow, a weekday such as mon, or 2026-11-03", word)
	}
	return day, nil
}

func atClock(day time.Time, clock string) (time.Time, error) {
	match := clockRE.FindStringSubmatch(clock)
	hour, _ := strconv.Atoi(match[1])
	minute, _ := strconv.Atoi(match[2])
	if hour > 23 || minute > 59 {
		return time.Time{}, fmt.Errorf("cannot read clock time %q; use 24-hour HH:MM", clock)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, day.Location()), nil
}

// returnTimeLabel names a return time the way a person reads it: a clock time
// today, "tomorrow 09:00", or a short date.
func returnTimeLabel(ms int64, now time.Time) string {
	at := time.UnixMilli(ms).In(now.Location())
	switch {
	case sameDay(at, now):
		return at.Format("15:04")
	case sameDay(at, now.AddDate(0, 0, 1)):
		return "tomorrow " + at.Format("15:04")
	case at.Sub(now) < 6*24*time.Hour:
		return at.Format("Mon 15:04")
	}
	return at.Format("Jan 2 15:04")
}

func sameDay(left, right time.Time) bool {
	return left.Year() == right.Year() && left.YearDay() == right.YearDay()
}
