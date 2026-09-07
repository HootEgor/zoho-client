package core

import (
	"fmt"
	"sync"
	"time"
	"zohoclient/entity"
)

// orderSyncStats is what the poller records about its last completed pass, plus the running totals
// since the process started. Guarded by Core.orderSyncMu.
type orderSyncStats struct {
	lastRunAt   time.Time
	lastRunFor  time.Duration
	lastQueued  int
	lastSynced  int
	lastFailed  int
	totalSynced int64
	totalFailed int64
	lastError   string
	lastErrorAt time.Time
	// ran distinguishes "no pass has finished yet" from "a pass finished and found nothing".
	ran bool
}

// recordOrderRun stores the outcome of one poller pass. err is the failure that stopped the pass
// before it could look at any order; per-order failures are counted in failed instead.
func (c *Core) recordOrderRun(startedAt time.Time, queued, synced, failed int, err error) {
	c.orderSyncMu.Lock()
	defer c.orderSyncMu.Unlock()

	c.orderSync.ran = true
	c.orderSync.lastRunAt = startedAt
	c.orderSync.lastRunFor = time.Since(startedAt)
	c.orderSync.lastQueued = queued
	c.orderSync.lastSynced = synced
	c.orderSync.lastFailed = failed
	c.orderSync.totalSynced += int64(synced)
	c.orderSync.totalFailed += int64(failed)

	// The last error is kept until another one replaces it: a pass that succeeds does not erase
	// the reason the previous one failed, which is usually what someone reading /status wants.
	if err != nil {
		c.orderSync.lastError = err.Error()
		c.orderSync.lastErrorAt = time.Now()
	}
}

// Status assembles the health and activity snapshot served by /health and the Telegram /status
// command. It probes the databases but never calls Zoho — the token's cached expiry is read
// instead, so asking for status cannot consume an API call or block on Zoho being slow.
func (c *Core) Status() entity.ServiceStatus {
	now := time.Now()
	uptime := now.Sub(c.startedAt)

	status := entity.ServiceStatus{
		Site:          c.site.Name,
		Env:           c.env,
		DryRun:        c.dryRun,
		StartedAt:     c.startedAt,
		Uptime:        formatUptime(uptime),
		UptimeSeconds: int64(uptime.Seconds()),
		Features: entity.FeatureStatus{
			Payments:     c.site.Payments,
			CustomerSync: c.site.CustomerSync,
			B2B:          c.site.B2B,
			SmartSender:  c.smartSender != nil,
		},
		Orders: c.orderStatus(),
	}

	status.Components = c.components()
	status.Status = entity.StatusOK
	if status.Degraded() {
		status.Status = entity.StatusDegraded
	}

	return status
}

// components probes every dependency. The two database pings run in parallel: each is bounded at
// two seconds and the whole report has to fit inside the API server's five-second timeout.
func (c *Core) components() []entity.ComponentStatus {
	var (
		wg               sync.WaitGroup
		database, mongoS entity.ComponentStatus
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		database = c.databaseStatus()
	}()
	go func() {
		defer wg.Done()
		mongoS = c.mongoStatus()
	}()
	wg.Wait()

	return []entity.ComponentStatus{database, mongoS, c.zohoStatus(), c.orderSyncStatus()}
}

func (c *Core) databaseStatus() entity.ComponentStatus {
	s := entity.ComponentStatus{Name: "database"}
	if c.repo == nil {
		s.State, s.Detail = entity.ComponentDown, "repository not set"
		return s
	}
	if err := c.repo.Ping(); err != nil {
		s.State, s.Detail = entity.ComponentDown, err.Error()
		return s
	}
	s.State, s.Detail = entity.ComponentUp, c.repo.PoolStats()
	return s
}

func (c *Core) mongoStatus() entity.ComponentStatus {
	s := entity.ComponentStatus{Name: "mongo"}
	if c.mongoRepo == nil {
		// Order version history is optional; without it the service syncs orders exactly as
		// before, so its absence is a configuration choice rather than a fault.
		s.State, s.Detail = entity.ComponentDisabled, "order version history is off"
		return s
	}
	if err := c.mongoRepo.Ping(); err != nil {
		s.State, s.Detail = entity.ComponentDown, err.Error()
		return s
	}
	s.State = entity.ComponentUp
	return s
}

// zohoStatus reports the cached OAuth token rather than Zoho's reachability, which cannot be
// established without spending an API call. A service that has not synced anything yet holds no
// token, and that is unknown, not down — the first sync will fetch one.
func (c *Core) zohoStatus() entity.ComponentStatus {
	s := entity.ComponentStatus{Name: "zoho"}
	if c.zoho == nil {
		s.State, s.Detail = entity.ComponentDown, "zoho service not set"
		return s
	}

	expiry, valid := c.zoho.TokenStatus()
	if !valid {
		s.State, s.Detail = entity.ComponentUnknown, "no valid access token cached"
		return s
	}
	s.State = entity.ComponentUp
	s.Detail = fmt.Sprintf("token valid for %s", formatUptime(time.Until(expiry)))
	return s
}

// orderSyncStatus turns the last poller pass into a component: a pass that ended in an error is
// the earliest sign that the sync has stopped working, and it is the one dependency failure a
// database ping cannot see.
func (c *Core) orderSyncStatus() entity.ComponentStatus {
	c.orderSyncMu.RLock()
	defer c.orderSyncMu.RUnlock()

	s := entity.ComponentStatus{Name: "order-sync"}
	switch {
	case !c.orderSync.ran:
		s.State, s.Detail = entity.ComponentUnknown, "no poll completed yet"
	case c.orderSync.lastErrorAt.After(c.orderSync.lastRunAt):
		s.State, s.Detail = entity.ComponentDown, c.orderSync.lastError
	default:
		s.State = entity.ComponentUp
		s.Detail = fmt.Sprintf("last poll %s ago", formatUptime(time.Since(c.orderSync.lastRunAt)))
	}
	return s
}

func (c *Core) orderStatus() entity.OrderSyncStatus {
	c.orderSyncMu.RLock()
	defer c.orderSyncMu.RUnlock()

	s := entity.OrderSyncStatus{
		LastQueued:   c.orderSync.lastQueued,
		LastSynced:   c.orderSync.lastSynced,
		LastFailed:   c.orderSync.lastFailed,
		TotalSynced:  c.orderSync.totalSynced,
		TotalFailed:  c.orderSync.totalFailed,
		LastError:    c.orderSync.lastError,
		PollInterval: c.site.PollInterval.String(),
	}
	if c.orderSync.ran {
		lastRunAt := c.orderSync.lastRunAt
		s.LastRunAt = &lastRunAt
		s.LastRunMs = c.orderSync.lastRunFor.Milliseconds()
	}
	if !c.orderSync.lastErrorAt.IsZero() {
		lastErrorAt := c.orderSync.lastErrorAt
		s.LastErrorAt = &lastErrorAt
	}
	return s
}

// formatUptime renders a duration the way someone reading a status line wants it — days and hours
// for a long-running process, seconds for one that just started — rather than Go's default, which
// spells out every unit down to the nanosecond.
func formatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}
