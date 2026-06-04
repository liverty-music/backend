// Package main provides the sales-reminders CronJob entry point.
//
// The job runs approximately every 15 minutes. Each run scans sales phases
// for milestones (application open, 24h/1h before close, lottery-result day)
// that became due since the last scan, applies quiet-hours logic, checks the
// sent-log, and publishes SALES_PHASE.reminder.due events for each eligible
// (user, phase, stage) triple.
package main

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed IANA timezone DB; distroless/static has no system tzdata

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

const remindersFallbackShutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		logger, _ := logging.New()
		logger.Error(context.Background(), "sales-reminders job failed", err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger, _ := logging.New()
	bootLogger.Info(ctx, "starting sales-reminders job")

	var app *di.SalesRemindersJobApp
	defer func() {
		timeout := remindersFallbackShutdownTimeout
		if app != nil {
			timeout = app.ShutdownTimeout
		}
		sctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := shutdown.Shutdown(sctx); err != nil {
			bootLogger.Error(context.Background(), "error during shutdown", err)
		}
	}()

	var err error
	app, err = di.InitializeSalesRemindersJobApp(ctx)
	if err != nil {
		return err
	}

	published, err := app.SalesReminderUC.ScanDueReminders(ctx)
	if err != nil {
		return err
	}

	app.Logger.Info(ctx, "sales-reminders: scan complete",
		slog.Int("reminders_published", published),
	)
	return nil
}
