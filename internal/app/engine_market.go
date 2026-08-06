package app

import (
	"context"
	openapi "github.com/longbridge/openapi-go"
	lbquote "github.com/longbridge/openapi-go/quote"
	"sort"
	"time"
)

func (e *Engine) loadMarketClock(ctx context.Context, now time.Time) (MarketClock, error) {
	localNow := now.In(e.marketTZ)
	currentDate := localNow.Format("20060102")

	if err := e.ensureMarketMetadata(ctx, localNow); err != nil {
		return MarketClock{}, err
	}

	e.marketMu.RLock()
	tradingDay := e.tradingDayByDate[currentDate]
	templates := append([]sessionTemplate(nil), e.sessionTemplates...)
	e.marketMu.RUnlock()

	windows := make([]SessionWindow, 0, 4)
	for _, item := range templates {
		start, endAt := buildSessionTimes(localNow, item.Begin, item.End, e.marketTZ)
		windows = append(windows, SessionWindow{
			Name:  item.Name,
			Start: start,
			End:   endAt,
		})
	}

	sort.Slice(windows, func(i, j int) bool {
		return windows[i].Start.Before(windows[j].Start)
	})

	marketClock := MarketClock{
		Date:          currentDate,
		TimeZone:      e.marketTZ.String(),
		TradingDay:    tradingDay,
		CurrentPhase:  MarketPhaseClosed,
		Windows:       windows,
		LastRefreshed: time.Now().UTC(),
	}
	if len(windows) > 0 {
		if regularWindow, ok := findSessionWindow(windows, MarketPhaseNormal); ok {
			marketClock.OpenTime = &regularWindow.Start
			marketClock.CloseTime = &regularWindow.End
		} else {
			marketClock.OpenTime = &windows[0].Start
			marketClock.CloseTime = &windows[len(windows)-1].End
		}
	}
	if !tradingDay {
		return marketClock, nil
	}

	for _, window := range windows {
		if !localNow.Before(window.Start) && localNow.Before(window.End) {
			marketClock.Open = true
			marketClock.CurrentPhase = MarketPhase(window.Name)
			return marketClock, nil
		}
	}
	return marketClock, nil
}

func (e *Engine) ensureMarketMetadata(ctx context.Context, localNow time.Time) error {
	currentDate := localNow.Format("20060102")

	e.marketMu.RLock()
	hasTemplates := len(e.sessionTemplates) > 0
	_, hasTradingDay := e.tradingDayByDate[currentDate]
	e.marketMu.RUnlock()
	if hasTemplates && hasTradingDay {
		return nil
	}

	e.marketMu.Lock()
	defer e.marketMu.Unlock()
	if len(e.sessionTemplates) > 0 {
		if _, ok := e.tradingDayByDate[currentDate]; ok {
			return nil
		}
	}

	if len(e.sessionTemplates) == 0 {
		sessions, err := e.client.TradingSession(ctx)
		if err != nil {
			return err
		}
		templates := make([]sessionTemplate, 0, 4)
		for _, item := range sessions {
			if item == nil || item.Market != openapi.MarketUS {
				continue
			}
			for _, session := range item.TradeSession {
				if session == nil {
					continue
				}
				templates = append(templates, sessionTemplate{
					Name:  sessionName(session.TradeSession),
					Begin: session.BegTime,
					End:   session.EndTime,
				})
			}
		}
		e.sessionTemplates = templates
	}

	begin := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, e.marketTZ)
	end := begin
	days, err := e.client.TradingDays(ctx, openapi.MarketUS, &begin, &end)
	if err != nil {
		return err
	}

	tradingDay := false
	if days != nil {
		for _, item := range days.TradeDay {
			if item.Format("20060102") == currentDate {
				tradingDay = true
				break
			}
		}
	}
	e.tradingDayByDate[currentDate] = tradingDay
	return nil
}

func (e *Engine) nextMarketResumeTime(ctx context.Context, marketClock MarketClock, now time.Time) (*time.Time, error) {
	localNow := now.In(e.marketTZ)
	for _, window := range marketClock.Windows {
		if localNow.Before(window.Start) {
			start := window.Start
			return &start, nil
		}
	}
	return e.nextTradingWindowStart(ctx, localNow)
}

func (e *Engine) nextTradingWindowStart(ctx context.Context, localNow time.Time) (*time.Time, error) {
	if err := e.ensureMarketMetadata(ctx, localNow); err != nil {
		return nil, err
	}

	e.marketMu.RLock()
	templates := append([]sessionTemplate(nil), e.sessionTemplates...)
	e.marketMu.RUnlock()
	if len(templates) == 0 {
		return nil, nil
	}

	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Begin < templates[j].Begin
	})

	begin := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, e.marketTZ).AddDate(0, 0, 1)
	end := begin.AddDate(0, 0, nextTradingSearchDays)
	days, err := e.client.TradingDays(ctx, openapi.MarketUS, &begin, &end)
	if err != nil {
		return nil, err
	}
	if days == nil || len(days.TradeDay) == 0 {
		return nil, nil
	}

	nextDay := days.TradeDay[0].In(e.marketTZ)
	startHour := int(templates[0].Begin) / 100
	startMinute := int(templates[0].Begin) % 100
	start := time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), startHour, startMinute, 0, 0, e.marketTZ)
	return &start, nil
}

func computeAdaptivePollDelay(now time.Time, marketClock MarketClock, activePoll time.Duration, hasPending bool, refreshDueAt *time.Time, nextResumeAt *time.Time) time.Duration {
	delay := activePoll
	if delay <= 0 {
		delay = 5 * time.Second
	}

	if !marketClock.Open {
		delay = offHoursPollFallback
		if nextResumeAt != nil {
			lead := minDuration(activePoll, time.Minute)
			wakeAt := nextResumeAt.Add(-lead)
			if !wakeAt.After(now) {
				wakeAt = now.Add(minAdaptivePollDelay)
			}
			delay = min(wakeAt.Sub(now), offHoursPollFallback)
		}
		if hasPending && pendingOrderPollInterval < delay {
			delay = pendingOrderPollInterval
		}
	}

	if refreshDueAt != nil {
		refreshAt := refreshDueAt.UTC()
		if !refreshAt.After(now) {
			refreshAt = now.Add(minAdaptivePollDelay)
		}
		refreshDelay := refreshAt.Sub(now)
		if refreshDelay < delay {
			delay = refreshDelay
		}
	}

	if delay < minAdaptivePollDelay {
		return minAdaptivePollDelay
	}
	return delay
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if a < b {
		return a
	}
	return b
}

func buildSessionTimes(now time.Time, beginMinutes int32, endMinutes int32, loc *time.Location) (time.Time, time.Time) {
	startHour := int(beginMinutes) / 100
	startMinute := int(beginMinutes) % 100
	endHour := int(endMinutes) / 100
	endMinute := int(endMinutes) % 100

	start := time.Date(now.Year(), now.Month(), now.Day(), startHour, startMinute, 0, 0, loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), endHour, endMinute, 0, 0, loc)
	if !end.After(start) {
		end = end.Add(24 * time.Hour)
	}
	return start, end
}

func sessionName(session lbquote.TradeSession) string {
	switch session {
	case lbquote.TradeSessionPreTrade:
		return string(MarketPhasePre)
	case lbquote.TradeSessionNormal:
		return string(MarketPhaseNormal)
	case lbquote.TradeSessionPostTrade:
		return string(MarketPhasePost)
	case lbquote.TradeSessionOvernight:
		return string(MarketPhaseOvernight)
	default:
		return string(MarketPhaseClosed)
	}
}

func findSessionWindow(windows []SessionWindow, phase MarketPhase) (SessionWindow, bool) {
	for _, window := range windows {
		if MarketPhase(window.Name) == phase {
			return window, true
		}
	}
	return SessionWindow{}, false
}
