package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embedded tzdata so Asia/Dhaka resolves on a scratch container with
	// no OS tz files.
	_ "time/tzdata"
)

// historyLookback is how far back we ask the store for readings. The chart
// itself shows the current calendar month, but we also pull a few extra
// days so the very first day of the month can have its spending computed
// against the last reading of the previous month.
const historyLookback = 40 * 24 * time.Hour

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := LoadConfig()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(2)
	}

	tz, err := time.LoadLocation("Asia/Dhaka")
	if err != nil {
		slog.Error("load tz", "error", err)
		os.Exit(2)
	}

	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		slog.Error("open store", "error", err)
		os.Exit(2)
	}
	defer store.Close()

	client := NewNescoClient(cfg.NescoBaseURL, cfg.HTTPTimeout)
	poster := NewDiscordPoster(cfg.DiscordWebhookURL, cfg.HTTPTimeout)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("nesco-monitor starting",
		"account", cfg.AccountNo,
		"threshold", cfg.LowBalanceThreshold,
		"db", cfg.DBPath,
		"tz", tz.String(),
		"poll_at", pollTimeString(),
	)

	// Always poll at startup so the channel comes alive immediately
	// instead of waiting until the next 01:10.
	runOnce(ctx, cfg, client, store, poster, tz, "startup")

	for {
		next := NextTick(time.Now(), tz)
		wait := time.Until(next)
		slog.Info("waiting for next daily tick",
			"next", next.Format(time.RFC3339),
			"in", wait.Round(time.Second).String(),
		)

		select {
		case <-ctx.Done():
			slog.Info("shutting down", "reason", ctx.Err())
			return
		case <-time.After(wait):
			runOnce(ctx, cfg, client, store, poster, tz, "scheduled")
		}
	}
}

// runOnce executes one poll → persist → chart → post cycle. Transient
// failures are logged but do not kill the daemon — the next 01:10 retries.
func runOnce(
	ctx context.Context,
	cfg *Config,
	client *NescoClient,
	store *Store,
	poster *DiscordPoster,
	tz *time.Location,
	trigger string,
) {
	pollCtx, cancel := context.WithTimeout(ctx, 2*cfg.HTTPTimeout+5*time.Second)
	defer cancel()

	slog.Info("polling NESCO", "trigger", trigger, "account", cfg.AccountNo)
	snap, err := client.GetBalance(pollCtx, cfg.AccountNo)
	if err != nil {
		slog.Error("fetch balance", "error", err)
		return
	}
	slog.Info("got balance", "balance", snap.Balance, "meter", snap.MeterNo)

	prev, prevTime, hasPrev, err := store.PreviousReading(pollCtx, cfg.AccountNo, snap.FetchedAt)
	if err != nil {
		slog.Warn("previous reading lookup failed", "error", err)
	}

	// Skip storing a reading identical to the most recent one. This drops
	// restart-induced off-schedule duplicates and NESCO cache re-serves, which
	// would otherwise pollute the daily cadence and distort the chart's gap
	// math. A genuine unchanged balance loses no information by being skipped.
	if !shouldStoreReading(hasPrev, prev, snap.Balance) {
		slog.Info("skipping duplicate reading (balance unchanged)",
			"balance", snap.Balance, "trigger", trigger)
	} else if err := store.Insert(pollCtx, snap); err != nil {
		// Persistence is best-effort. We can still post the current reading.
		slog.Error("persist snapshot", "error", err)
	}

	// Upsert scraped recharges (idempotent on order id). The panel returns the
	// full history, so this backfills on first run and self-heals thereafter.
	if err := store.UpsertRecharges(pollCtx, cfg.AccountNo, snap.Recharges); err != nil {
		slog.Error("persist recharges", "error", err)
	}

	history, err := store.History(pollCtx, cfg.AccountNo, historyLookback)
	if err != nil {
		slog.Error("history query failed", "error", err)
		return
	}

	recharges, err := store.RechargesSince(pollCtx, cfg.AccountNo, historyLookback)
	if err != nil {
		slog.Error("recharges query failed", "error", err)
	}

	// The chart always renders the current calendar month (with gaps for
	// missing days), so we no longer skip the post on sparse data — even
	// the very first poll produces a chart, just one with no measured
	// spending yet.
	chartPNG, err := RenderMonthlyPNG(history, recharges, tz)
	if err != nil {
		slog.Error("render chart", "error", err)
		return
	}

	if err := poster.PostBalance(pollCtx, snap, prev, hasPrev, prevTime, chartPNG, cfg.LowBalanceThreshold, tz); err != nil {
		slog.Error("post to discord", "error", err)
		return
	}
	slog.Info("posted daily update", "history_points", len(history))
}

// shouldStoreReading reports whether a freshly fetched balance is worth
// persisting. Readings identical to the previous one (restart-induced
// off-schedule polls, NESCO cache re-serves) add no information and are
// skipped to keep the daily series clean.
func shouldStoreReading(hasPrev bool, prevBalance, current float64) bool {
	return !hasPrev || prevBalance != current
}
