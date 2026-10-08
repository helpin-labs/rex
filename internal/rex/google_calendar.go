package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Google Calendar's tools. Looking at calendars, events and free time runs
// on its own; creating, changing, deleting and answering invitations follow
// the bot's approvals. Rex asks for calendar.readonly (calendars and free
// time) and calendar.events (events on calendars the user can edit).

const (
	calendarReadScope   = "https://www.googleapis.com/auth/calendar.readonly"
	calendarEventsScope = "https://www.googleapis.com/auth/calendar.events"
	calendarMaxEvents   = 100
)

var calendarService = &googleService{
	name:        "Google Calendar",
	permissions: []string{calendarReadScope, calendarEventsScope},
	tools: []googleTool{
		{
			name:        "list_calendars",
			description: "List the user's calendars with their ids, time zones and whether the user can edit them.",
			access:      "read",
			schema:      objectSchema(nil, map[string]any{}),
			call:        calendarList,
		},
		{
			name:        "list_events",
			description: "List events in a time range, in start order, with attendees, location and meeting link. Without a range, the next 7 days.",
			access:      "read",
			schema: objectSchema(nil, map[string]any{
				"calendar_id": calendarArgument,
				"time_min":    timeArgument("Start of the range."),
				"time_max":    timeArgument("End of the range."),
				"query":       map[string]any{"type": "string", "description": "Words to look for in the title, description, location or attendees."},
				"max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": calendarMaxEvents, "description": "50 by default."},
				"time_zone":   zoneArgument,
			}),
			call: calendarListEvents,
		},
		{
			name:        "find_free_time",
			description: "Find when the user (and others whose calendars they can see) is free: busy periods per calendar, and the gaps of at least duration_minutes when every calendar is free.",
			access:      "read",
			schema: objectSchema([]string{"time_min", "time_max"}, map[string]any{
				"time_min":         timeArgument("Start of the range."),
				"time_max":         timeArgument("End of the range."),
				"calendars":        map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{"type": "string"}, "description": "Calendar ids or people's addresses; the user's main calendar by default."},
				"duration_minutes": map[string]any{"type": "integer", "minimum": 5, "maximum": 1440, "description": "The shortest useful gap; 30 by default."},
				"time_zone":        zoneArgument,
			}),
			call: calendarFreeTime,
		},
		{
			name:        "create_event",
			description: "Add an event. With attendees, Google emails them an invitation unless send_updates is \"none\".",
			access:      "write",
			schema: objectSchema([]string{"summary", "start", "end"}, map[string]any{
				"calendar_id":  calendarArgument,
				"summary":      map[string]any{"type": "string", "minLength": 1, "maxLength": 500, "description": "The event's title."},
				"start":        timeArgument("A time, or a date for an all-day event."),
				"end":          timeArgument("A time, or the day after the last day for an all-day event."),
				"time_zone":    zoneArgument,
				"description":  map[string]any{"type": "string", "maxLength": 8000},
				"location":     map[string]any{"type": "string", "maxLength": 1000},
				"attendees":    map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string"}, "description": "Email addresses to invite."},
				"add_meet":     map[string]any{"type": "boolean", "description": "Add a Google Meet link."},
				"send_updates": sendUpdatesArgument,
			}),
			call: calendarCreate,
		},
		{
			name:        "update_event",
			description: "Change an event: only the fields given change. Attendees are added or removed without touching the others' answers.",
			access:      "write",
			schema: objectSchema([]string{"event_id"}, map[string]any{
				"calendar_id":      calendarArgument,
				"event_id":         map[string]any{"type": "string"},
				"summary":          map[string]any{"type": "string", "maxLength": 500},
				"start":            timeArgument(""),
				"end":              timeArgument(""),
				"time_zone":        zoneArgument,
				"description":      map[string]any{"type": "string", "maxLength": 8000},
				"location":         map[string]any{"type": "string", "maxLength": 1000},
				"add_attendees":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"remove_attendees": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"send_updates":     sendUpdatesArgument,
			}),
			call: calendarUpdate,
		},
		{
			name:        "delete_event",
			description: "Delete an event. If the user organised it, attendees are told it was cancelled unless send_updates is \"none\".",
			access:      "write",
			schema: objectSchema([]string{"event_id"}, map[string]any{
				"calendar_id":  calendarArgument,
				"event_id":     map[string]any{"type": "string"},
				"send_updates": sendUpdatesArgument,
			}),
			call: calendarDelete,
		},
		{
			name:        "respond_to_event",
			description: "Accept, decline or tentatively accept an invitation; the organiser is told.",
			access:      "write",
			schema: objectSchema([]string{"event_id", "response"}, map[string]any{
				"calendar_id": calendarArgument,
				"event_id":    map[string]any{"type": "string"},
				"response":    map[string]any{"type": "string", "enum": []string{"accepted", "declined", "tentative"}},
			}),
			call: calendarRespond,
		},
	},
}

var (
	calendarArgument    = map[string]any{"type": "string", "description": "From list_calendars; the user's main calendar (\"primary\") by default."}
	zoneArgument        = map[string]any{"type": "string", "description": "IANA time zone for times without an offset, e.g. \"Europe/Berlin\"; this Mac's by default."}
	sendUpdatesArgument = map[string]any{"type": "string", "enum": []string{"all", "externalOnly", "none"}, "description": "Who Google emails about the change; \"all\" by default."}
)

func timeArgument(description string) map[string]any {
	return map[string]any{"type": "string", "description": strings.TrimSpace(description + " RFC 3339 (2026-10-09T15:00:00+02:00), a local time (2026-10-09T15:00) or a date (2026-10-09).")}
}

// --- times ---

var (
	dateOnly       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	localTimeForms = []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"}
)

// localZoneName is this Mac's IANA time zone, which Go reports as "Local".
func localZoneName() string {
	if zone := strings.TrimPrefix(os.Getenv("TZ"), ":"); zone != "" {
		if _, err := time.LoadLocation(zone); err == nil {
			return zone
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(filepath.ToSlash(target), "zoneinfo/"); ok {
			return zone
		}
	}
	return "UTC"
}

func loadZone(name string) (*time.Location, string, error) {
	name = firstNonEmpty(strings.TrimSpace(name), localZoneName())
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, "", fmt.Errorf("unknown time zone %q", name)
	}
	return location, name, nil
}

// parseInstant reads a moment: RFC 3339, or a local time or date in zone.
func parseInstant(value string, zone *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	if dateOnly.MatchString(value) {
		return time.ParseInLocation("2006-01-02", value, zone)
	}
	for _, form := range localTimeForms {
		if parsed, err := time.ParseInLocation(form, value, zone); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a time; use 2026-10-09T15:00:00+02:00, 2026-10-09T15:00 or 2026-10-09", value)
}

// eventTime is an event's start or end as Calendar wants it: a date for
// all-day events, otherwise a time with its zone.
func eventTime(value, zoneName string) (map[string]any, error) {
	value = strings.TrimSpace(value)
	if dateOnly.MatchString(value) {
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return nil, fmt.Errorf("%q is not a date", value)
		}
		return map[string]any{"date": value}, nil
	}
	location, zone, err := loadZone(zoneName)
	if err != nil {
		return nil, err
	}
	moment, err := parseInstant(value, location)
	if err != nil {
		return nil, err
	}
	return map[string]any{"dateTime": moment.In(location).Format(time.RFC3339), "timeZone": zone}, nil
}

type busyPeriod struct{ start, end time.Time }

// freeSlots returns the gaps of at least minimum between from and to that
// no busy period covers.
func freeSlots(busy []busyPeriod, from, to time.Time, minimum time.Duration) []busyPeriod {
	sort.Slice(busy, func(i, j int) bool { return busy[i].start.Before(busy[j].start) })
	var free []busyPeriod
	cursor := from
	for _, period := range busy {
		if period.start.After(cursor) {
			end := period.start
			if end.After(to) {
				end = to
			}
			if end.Sub(cursor) >= minimum {
				free = append(free, busyPeriod{cursor, end})
			}
		}
		if period.end.After(cursor) {
			cursor = period.end
		}
		if !cursor.Before(to) {
			return free
		}
	}
	if to.Sub(cursor) >= minimum {
		free = append(free, busyPeriod{cursor, to})
	}
	return free
}

// --- events ---

type calendarTime struct {
	Date     string `json:"date,omitempty"`
	DateTime string `json:"dateTime,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

func (t calendarTime) String() string { return firstNonEmpty(t.DateTime, t.Date) }

type calendarAttendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
	Self           bool   `json:"self,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
}

type calendarEvent struct {
	ID          string             `json:"id"`
	Status      string             `json:"status"`
	Summary     string             `json:"summary"`
	Description string             `json:"description"`
	Location    string             `json:"location"`
	HTMLLink    string             `json:"htmlLink"`
	HangoutLink string             `json:"hangoutLink"`
	Start       calendarTime       `json:"start"`
	End         calendarTime       `json:"end"`
	Attendees   []calendarAttendee `json:"attendees"`
	Organizer   struct {
		Email string `json:"email"`
		Self  bool   `json:"self"`
	} `json:"organizer"`
	Recurring string `json:"recurringEventId"`
}

// summary is an event as a tool reports it.
func (e calendarEvent) summary() map[string]any {
	item := map[string]any{"id": e.ID, "summary": firstNonEmpty(e.Summary, "(no title)"), "start": e.Start.String(), "end": e.End.String(), "link": e.HTMLLink}
	if e.Start.Date != "" {
		item["all_day"] = true
	}
	if e.Status == "cancelled" || e.Status == "tentative" {
		item["status"] = e.Status
	}
	if e.Location != "" {
		item["location"] = e.Location
	}
	if e.HangoutLink != "" {
		item["meet_link"] = e.HangoutLink
	}
	if e.Description != "" {
		item["description"], _ = excerptRunes(htmlToText(e.Description), 1500)
	}
	if e.Organizer.Email != "" && !e.Organizer.Self {
		item["organizer"] = e.Organizer.Email
	}
	if e.Recurring != "" {
		item["recurring"] = true
	}
	if len(e.Attendees) > 0 {
		attendees := make([]map[string]any, 0, len(e.Attendees))
		for _, attendee := range e.Attendees {
			if attendee.Self {
				item["your_response"] = attendee.ResponseStatus
			}
			entry := map[string]any{"email": attendee.Email, "response": attendee.ResponseStatus}
			if attendee.DisplayName != "" {
				entry["name"] = attendee.DisplayName
			}
			if attendee.Optional {
				entry["optional"] = true
			}
			attendees = append(attendees, entry)
		}
		item["attendees"] = attendees
	}
	return item
}

func calendarPath(calendarID string) string {
	return calendarAPI + "/calendars/" + url.PathEscape(firstNonEmpty(strings.TrimSpace(calendarID), "primary"))
}

func calendarEventPath(calendarID, eventID string) (string, error) {
	if strings.TrimSpace(eventID) == "" {
		return "", errors.New("event_id is required; find it with list_events")
	}
	return calendarPath(calendarID) + "/events/" + url.PathEscape(eventID), nil
}

func sendUpdates(value string, attendees bool) (string, error) {
	switch value {
	case "":
		if attendees {
			return "all", nil
		}
		return "none", nil
	case "all", "externalOnly", "none":
		return value, nil
	}
	return "", fmt.Errorf("send_updates must be all, externalOnly or none")
}

func calendarList(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct{}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			ID         string `json:"id"`
			Summary    string `json:"summary"`
			Override   string `json:"summaryOverride"`
			TimeZone   string `json:"timeZone"`
			AccessRole string `json:"accessRole"`
			Primary    bool   `json:"primary"`
		} `json:"items"`
	}
	if err := api.do(ctx, http.MethodGet, calendarAPI+"/users/me/calendarList?maxResults=250", nil, &list); err != nil {
		return nil, err
	}
	calendars := []map[string]any{}
	for _, item := range list.Items {
		entry := map[string]any{"id": item.ID, "name": firstNonEmpty(item.Override, item.Summary), "time_zone": item.TimeZone,
			"can_edit": item.AccessRole == "owner" || item.AccessRole == "writer"}
		if item.Primary {
			entry["primary"] = true
		}
		calendars = append(calendars, entry)
	}
	return map[string]any{"calendars": calendars}, nil
}

func calendarListEvents(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		CalendarID string `json:"calendar_id"`
		TimeMin    string `json:"time_min"`
		TimeMax    string `json:"time_max"`
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
		TimeZone   string `json:"time_zone"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	location, zone, err := loadZone(args.TimeZone)
	if err != nil {
		return nil, err
	}
	from, to := time.Now(), time.Time{}
	if args.TimeMin != "" {
		if from, err = parseInstant(args.TimeMin, location); err != nil {
			return nil, err
		}
	}
	to = from.AddDate(0, 0, 7)
	if args.TimeMax != "" {
		if to, err = parseInstant(args.TimeMax, location); err != nil {
			return nil, err
		}
	}
	if !to.After(from) {
		return nil, errors.New("time_max must be after time_min")
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 50
	}
	query := url.Values{
		"timeMin": {from.Format(time.RFC3339)}, "timeMax": {to.Format(time.RFC3339)}, "timeZone": {zone},
		"singleEvents": {"true"}, "orderBy": {"startTime"}, "maxResults": {fmt.Sprint(min(args.MaxResults, calendarMaxEvents))},
	}
	if q := strings.TrimSpace(args.Query); q != "" {
		query.Set("q", q)
	}
	var list struct {
		Items         []calendarEvent `json:"items"`
		NextPageToken string          `json:"nextPageToken"`
	}
	if err := api.do(ctx, http.MethodGet, calendarPath(args.CalendarID)+"/events?"+query.Encode(), nil, &list); err != nil {
		return nil, err
	}
	events := make([]map[string]any, 0, len(list.Items))
	for _, event := range list.Items {
		events = append(events, event.summary())
	}
	result := map[string]any{"time_zone": zone, "from": from.In(location).Format(time.RFC3339), "to": to.In(location).Format(time.RFC3339), "events": events}
	if list.NextPageToken != "" {
		result["more"] = "There are more events in this range; narrow it to see them."
	}
	return result, nil
}

func calendarFreeTime(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		TimeMin   string   `json:"time_min"`
		TimeMax   string   `json:"time_max"`
		Calendars []string `json:"calendars"`
		Duration  int      `json:"duration_minutes"`
		TimeZone  string   `json:"time_zone"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	location, zone, err := loadZone(args.TimeZone)
	if err != nil {
		return nil, err
	}
	from, err := parseInstant(args.TimeMin, location)
	if err != nil {
		return nil, err
	}
	to, err := parseInstant(args.TimeMax, location)
	if err != nil {
		return nil, err
	}
	if !to.After(from) {
		return nil, errors.New("time_max must be after time_min")
	}
	if len(args.Calendars) == 0 {
		args.Calendars = []string{"primary"}
	}
	if args.Duration <= 0 {
		args.Duration = 30
	}
	items := make([]map[string]string, 0, len(args.Calendars))
	for _, id := range args.Calendars {
		items = append(items, map[string]string{"id": strings.TrimSpace(id)})
	}
	var answer struct {
		Calendars map[string]struct {
			Busy []struct {
				Start time.Time `json:"start"`
				End   time.Time `json:"end"`
			} `json:"busy"`
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"calendars"`
	}
	body := map[string]any{"timeMin": from.Format(time.RFC3339), "timeMax": to.Format(time.RFC3339), "timeZone": zone, "items": items}
	if err := api.do(ctx, http.MethodPost, calendarAPI+"/freeBusy", body, &answer); err != nil {
		return nil, err
	}
	format := func(moment time.Time) string { return moment.In(location).Format(time.RFC3339) }
	var all []busyPeriod
	calendars := map[string]any{}
	for id, calendar := range answer.Calendars {
		if len(calendar.Errors) > 0 {
			calendars[id] = map[string]any{"error": "can't see this calendar (" + calendar.Errors[0].Reason + ")"}
			continue
		}
		busy := make([]map[string]string, 0, len(calendar.Busy))
		for _, period := range calendar.Busy {
			busy = append(busy, map[string]string{"start": format(period.Start), "end": format(period.End)})
			all = append(all, busyPeriod{period.Start, period.End})
		}
		calendars[id] = map[string]any{"busy": busy}
	}
	free := []map[string]string{}
	for _, slot := range freeSlots(all, from, to, time.Duration(args.Duration)*time.Minute) {
		if len(free) == 50 {
			break
		}
		free = append(free, map[string]string{"start": format(slot.start), "end": format(slot.end)})
	}
	return map[string]any{"time_zone": zone, "calendars": calendars, "free": free,
		"note": "Free gaps count every hour of the range, nights included; pick ones that suit the people involved."}, nil
}

func calendarCreate(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		CalendarID  string   `json:"calendar_id"`
		Summary     string   `json:"summary"`
		Start       string   `json:"start"`
		End         string   `json:"end"`
		TimeZone    string   `json:"time_zone"`
		Description string   `json:"description"`
		Location    string   `json:"location"`
		Attendees   []string `json:"attendees"`
		AddMeet     bool     `json:"add_meet"`
		SendUpdates string   `json:"send_updates"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Summary) == "" {
		return nil, errors.New("summary is required")
	}
	start, err := eventTime(args.Start, args.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	end, err := eventTime(args.End, args.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("end: %w", err)
	}
	body := map[string]any{"summary": strings.TrimSpace(args.Summary), "start": start, "end": end}
	if args.Description != "" {
		body["description"] = args.Description
	}
	if args.Location != "" {
		body["location"] = args.Location
	}
	attendees, err := attendeeList(args.Attendees)
	if err != nil {
		return nil, err
	}
	if len(attendees) > 0 {
		body["attendees"] = attendees
	}
	updates, err := sendUpdates(args.SendUpdates, len(attendees) > 0)
	if err != nil {
		return nil, err
	}
	query := url.Values{"sendUpdates": {updates}}
	if args.AddMeet {
		body["conferenceData"] = map[string]any{"createRequest": map[string]any{"requestId": randomHex(12), "conferenceSolutionKey": map[string]string{"type": "hangoutsMeet"}}}
		query.Set("conferenceDataVersion", "1")
	}
	var created calendarEvent
	if err := api.do(ctx, http.MethodPost, calendarPath(args.CalendarID)+"/events?"+query.Encode(), body, &created); err != nil {
		return nil, err
	}
	return map[string]any{"status": "created", "event": created.summary(), "invitations": updates}, nil
}

// attendeeList checks addresses for an event's attendees.
func attendeeList(addresses []string) ([]calendarAttendee, error) {
	var attendees []calendarAttendee
	for _, value := range addresses {
		if strings.TrimSpace(value) == "" {
			continue
		}
		parsed, err := parseRecipients([]string{value})
		if err != nil {
			return nil, err
		}
		for _, address := range strings.Split(parsed, ", ") {
			email := address
			if open := strings.LastIndex(address, "<"); open >= 0 {
				email = strings.TrimSuffix(address[open+1:], ">")
			}
			attendees = append(attendees, calendarAttendee{Email: email})
		}
	}
	return attendees, nil
}

func calendarUpdate(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		CalendarID      string   `json:"calendar_id"`
		EventID         string   `json:"event_id"`
		Summary         *string  `json:"summary"`
		Start           string   `json:"start"`
		End             string   `json:"end"`
		TimeZone        string   `json:"time_zone"`
		Description     *string  `json:"description"`
		Location        *string  `json:"location"`
		AddAttendees    []string `json:"add_attendees"`
		RemoveAttendees []string `json:"remove_attendees"`
		SendUpdates     string   `json:"send_updates"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	path, err := calendarEventPath(args.CalendarID, args.EventID)
	if err != nil {
		return nil, err
	}
	patch := map[string]any{}
	if args.Summary != nil {
		patch["summary"] = *args.Summary
	}
	if args.Description != nil {
		patch["description"] = *args.Description
	}
	if args.Location != nil {
		patch["location"] = *args.Location
	}
	for field, value := range map[string]string{"start": args.Start, "end": args.End} {
		if value == "" {
			continue
		}
		when, err := eventTime(value, args.TimeZone)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		patch[field] = when
	}
	var current calendarEvent
	if len(args.AddAttendees)+len(args.RemoveAttendees) > 0 {
		if err := api.do(ctx, http.MethodGet, path, nil, &current); err != nil {
			return nil, err
		}
		added, err := attendeeList(args.AddAttendees)
		if err != nil {
			return nil, err
		}
		removed, err := attendeeList(args.RemoveAttendees)
		if err != nil {
			return nil, err
		}
		attendees := []calendarAttendee{}
		for _, attendee := range current.Attendees {
			if !hasAttendee(removed, attendee.Email) {
				attendees = append(attendees, attendee)
			}
		}
		for _, attendee := range added {
			if !hasAttendee(attendees, attendee.Email) {
				attendees = append(attendees, attendee)
			}
		}
		patch["attendees"] = attendees
	}
	if len(patch) == 0 {
		return nil, errors.New("nothing to change")
	}
	updates, err := sendUpdates(args.SendUpdates, true)
	if err != nil {
		return nil, err
	}
	var updated calendarEvent
	if err := api.do(ctx, http.MethodPatch, path+"?sendUpdates="+updates, patch, &updated); err != nil {
		return nil, err
	}
	return map[string]any{"status": "updated", "event": updated.summary()}, nil
}

func hasAttendee(attendees []calendarAttendee, email string) bool {
	for _, attendee := range attendees {
		if strings.EqualFold(attendee.Email, email) {
			return true
		}
	}
	return false
}

func calendarDelete(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		CalendarID  string `json:"calendar_id"`
		EventID     string `json:"event_id"`
		SendUpdates string `json:"send_updates"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	path, err := calendarEventPath(args.CalendarID, args.EventID)
	if err != nil {
		return nil, err
	}
	updates, err := sendUpdates(args.SendUpdates, true)
	if err != nil {
		return nil, err
	}
	if err := api.do(ctx, http.MethodDelete, path+"?sendUpdates="+updates, nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{"status": "deleted"}, nil
}

func calendarRespond(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		CalendarID string `json:"calendar_id"`
		EventID    string `json:"event_id"`
		Response   string `json:"response"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	if args.Response != "accepted" && args.Response != "declined" && args.Response != "tentative" {
		return nil, errors.New("response must be accepted, declined or tentative")
	}
	path, err := calendarEventPath(args.CalendarID, args.EventID)
	if err != nil {
		return nil, err
	}
	var event calendarEvent
	if err := api.do(ctx, http.MethodGet, path, nil, &event); err != nil {
		return nil, err
	}
	answered := false
	for index := range event.Attendees {
		if event.Attendees[index].Self {
			event.Attendees[index].ResponseStatus = args.Response
			answered = true
		}
	}
	if !answered {
		return nil, errors.New("the user is not invited to this event, so there is nothing to answer")
	}
	var updated calendarEvent
	if err := api.do(ctx, http.MethodPatch, path+"?sendUpdates=all", map[string]any{"attendees": event.Attendees}, &updated); err != nil {
		return nil, err
	}
	return map[string]any{"status": args.Response, "event": updated.summary()}, nil
}
