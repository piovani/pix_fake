package dict

import (
	"context"
	"encoding/base64"
	"strconv"
	"time"
)

type EventType string

const (
	EventRecoveryAnalysed  EventType = "FUNDS_RECOVERY_ANALYSED"  // to the creator: every report was analysed
	EventRecoveryCompleted EventType = "FUNDS_RECOVERY_COMPLETED" // to the creator: every refund was closed
	EventRecoveryUpdated   EventType = "FUNDS_RECOVERY_INFORMATION_UPDATED"
	EventRecoveryCancelled EventType = "FUNDS_RECOVERY_CANCELLED"
)

// EntityFundsRecovery is the only kind of entity events are about.
const EntityFundsRecovery = "FUNDS_RECOVERY"

// Event is an event notification, about a funds recovery, for one participant.
type Event struct {
	ID          int64 // numbered by the store, in the order they happened
	Type        EventType
	EntityID    string
	Participant string
	Time        time.Time
}

const (
	eventsPerPage = 200
	eventWindow   = 7 * 24 * time.Hour
)

type ListEventsInput struct {
	Participant string
	Cursor      string
}

type EventsPage struct {
	Events     []Event
	HasMore    bool
	NextCursor string
}

func eventCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func parseEventCursor(c string) (int64, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, false
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	return id, err == nil && id >= 0
}

// ListEventNotifications lists the caller's events of the last 7 days, oldest first, 200 a page.
func (s *Service) ListEventNotifications(ctx context.Context, caller string, in ListEventsInput) (EventsPage, error) {
	var v violations
	validateRegexp("Participant", in.Participant, ispbRe, &v)
	var after int64
	if in.Cursor != "" {
		var ok bool
		if after, ok = parseEventCursor(in.Cursor); !ok {
			v.add("Cursor", in.Cursor, "Invalid cursor")
		}
	}
	if len(v) > 0 {
		return EventsPage{}, invalid(KindBadRequest, "Request has invalid parameters", v)
	}
	if in.Participant != caller {
		return EventsPage{}, newError(KindForbidden, "Participant is not allowed to list the events of another participant")
	}
	events, err := s.repo.ListEvents(ctx, caller, after, s.now().UTC().Add(-eventWindow), eventsPerPage+1)
	if err != nil {
		return EventsPage{}, err
	}
	page := EventsPage{Events: events}
	if len(events) > eventsPerPage {
		page.Events, page.HasMore = events[:eventsPerPage], true
		page.NextCursor = eventCursor(page.Events[eventsPerPage-1].ID)
	}
	return page, nil
}
