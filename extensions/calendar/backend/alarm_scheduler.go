package backend

// AlarmScheduler arms desktop notifications for VALARM-bearing events.
// Phase 1G.
//
// Lifecycle:
//   - Created in CalendarBridge.ensureInit (lazy; disabled extensions
//     don't allocate it).
//   - Start subscribes to calendar:sync-complete (so newly-arrived alarms
//     get armed) and system:wake (so missed alarms during sleep are swept
//     to 'fired' without firing-after-the-fact, and future ones re-armed).
//   - time.AfterFunc timers cover the 24h horizon. On wrap-around (>24h
//     out), Reevaluate doesn't arm anything for that alarm yet — the next
//     scheduled tick or sync re-pass will pick it up.
//
// The scheduler does NOT walk recurrences or parse VALARMs itself. That
// happens at sync time via ExtractAlarms (alarm.go), which writes rows
// into event_alarms. The scheduler only reads pending rows and arms
// time.AfterFunc callbacks.

import (
	"context"
	"fmt"
	"sync"
	"time"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

const alarmHorizon = 24 * time.Hour

type AlarmScheduler struct {
	store  *Store
	notif  coreapi.Notifications
	events coreapi.EventBus
	log    coreapi.Logger
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	timers map[string]*time.Timer // alarmID → timer

	unsubs []func()
}

func NewAlarmScheduler(store *Store, notif coreapi.Notifications, events coreapi.EventBus, log coreapi.Logger) *AlarmScheduler {
	return &AlarmScheduler{
		store:  store,
		notif:  notif,
		events: events,
		log:    log,
		timers: make(map[string]*time.Timer),
	}
}

// warn logs a formatted warning via the extension's coreapi.Logger. Nil-
// safe: a nil logger means construction time skipped wiring (e.g., in a
// future test), and we simply drop the message rather than panic.
func (s *AlarmScheduler) warn(format string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.Warn(fmt.Sprintf(format, args...))
}

// Start begins listening for events and arms any currently-pending
// alarms in the 24h horizon. Safe to call once. Returns a cancel func
// the caller (bridge ensureInit) can ignore — Stop is the canonical
// teardown path.
func (s *AlarmScheduler) Start(ctx context.Context) context.CancelFunc {
	s.mu.Lock()
	if s.ctx == nil {
		s.ctx, s.cancel = context.WithCancel(ctx)
	}
	s.mu.Unlock()

	// Sweep alarms that should have fired in the past so they don't
	// notify retroactively when we arm.
	if err := s.store.MarkPastAlarmsFired(time.Now().Unix()); err != nil {
		s.warn("mark past alarms fired: %v", err)
	}

	// Initial arm pass + event subscriptions. Ignore Subscribe errors so a
	// missing EventBus doesn't block scheduling — the periodic re-eval
	// from sync.go's calendar:sync-complete event still keeps timers fresh.
	if s.events != nil {
		syncUnsub, _ := s.events.Subscribe("calendar:sync-complete", func(_ any) {
			if err := s.Reevaluate(); err != nil {
				s.warn("reevaluate after sync: %v", err)
			}
		})
		wakeUnsub, _ := s.events.Subscribe("system:wake", func(_ any) {
			// Sweep past alarms first; user was asleep, don't fire-after.
			if err := s.store.MarkPastAlarmsFired(time.Now().Unix()); err != nil {
				s.warn("mark past on wake: %v", err)
			}
			if err := s.Reevaluate(); err != nil {
				s.warn("reevaluate on wake: %v", err)
			}
		})
		s.mu.Lock()
		s.unsubs = append(s.unsubs, syncUnsub, wakeUnsub)
		s.mu.Unlock()
	}

	if err := s.Reevaluate(); err != nil {
		s.warn("initial reevaluate: %v", err)
	}

	return s.cancel
}

// Stop cancels all timers and event subscriptions.
func (s *AlarmScheduler) Stop() {
	s.mu.Lock()
	for id, t := range s.timers {
		t.Stop()
		delete(s.timers, id)
	}
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
		s.ctx = nil
	}
	s.mu.Unlock()
}

// Reevaluate scans pending alarms in [now, now+24h], stops any timers no
// longer matching pending rows (deletes / dismissals), and arms a
// time.AfterFunc for each new row. Idempotent — safe to call repeatedly.
func (s *AlarmScheduler) Reevaluate() error {
	now := time.Now().Unix()
	pending, err := s.store.PendingAlarmsInRange(now, now+int64(alarmHorizon.Seconds()))
	if err != nil {
		return fmt.Errorf("list pending: %w", err)
	}

	wantArmed := make(map[string]struct{}, len(pending))
	for _, a := range pending {
		wantArmed[a.ID] = struct{}{}
	}

	s.mu.Lock()
	// Drop any timer that's no longer in the pending set.
	for id, t := range s.timers {
		if _, ok := wantArmed[id]; !ok {
			t.Stop()
			delete(s.timers, id)
		}
	}
	// Arm new timers.
	for _, a := range pending {
		if _, ok := s.timers[a.ID]; ok {
			continue
		}
		delay := time.Until(time.Unix(a.TriggerUnix, 0))
		if delay < 0 {
			delay = 0
		}
		alarmID := a.ID
		s.timers[alarmID] = time.AfterFunc(delay, func() {
			s.dispatch(alarmID)
		})
	}
	s.mu.Unlock()
	return nil
}

// dispatch fires one alarm: re-reads the row to confirm pending status
// (user may have dismissed in the UI between arming and firing), shows
// the notification, marks fired.
func (s *AlarmScheduler) dispatch(alarmID string) {
	s.mu.Lock()
	delete(s.timers, alarmID)
	s.mu.Unlock()

	a, err := s.store.GetAlarm(alarmID)
	if err != nil || a == nil || a.Status != "pending" {
		return
	}

	// Only DISPLAY actions surface as desktop notifications in 1G.
	if a.Action != "display" {
		_ = s.store.MarkAlarmFired(alarmID, time.Now().Unix())
		return
	}

	ev, err := s.store.GetEvent(a.EventID)
	if err != nil || ev == nil {
		_ = s.store.MarkAlarmFired(alarmID, time.Now().Unix())
		return
	}

	title := ev.Summary
	if title == "" {
		title = "(no title)"
	}
	body := formatAlarmBody(*ev, a, time.Unix(a.InstanceUnix, 0))

	if s.notif != nil {
		_ = s.notif.Show(coreapi.NotifyRequest{
			Title: title,
			Body:  body,
			OnClick: coreapi.NotifyClickAction{
				Kind:        "open-extension",
				ExtensionID: "calendar",
				Path:        "/event/" + ev.ID,
			},
		})
	}

	_ = s.store.MarkAlarmFired(alarmID, time.Now().Unix())
}

// formatAlarmBody renders a single-line notification body in the host's
// system locale. Backend-only formatting — svelte-i18n isn't reachable from
// Go. Future scope: pipe a locale string from the user's UI preference.
func formatAlarmBody(ev Event, _ *Alarm, instanceStart time.Time) string {
	when := instanceStart.Local().Format("Mon Jan 2, 3:04 PM")
	if ev.IsAllDay {
		when = instanceStart.Local().Format("Mon Jan 2") + " (all day)"
	}
	if ev.Location != "" {
		return when + " · " + ev.Location
	}
	return when
}
