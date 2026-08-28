package daemon

import (
	"context"
	"errors"
	"log"
	"time"
)

// triggerPoll is how often the daemon looks for a trigger that has come due.
// A schedule is measured in minutes at the finest, so this only bounds how
// quickly a due trigger is noticed.
const triggerPoll = 2 * time.Second

// triggerWorker fires triggers whose time has come. It only starts flows; the
// cards they reach are answered by the chat workers like any other turn, so a
// long-running routine cannot block the next one from being noticed.
func (d *Daemon) triggerWorker(ctx context.Context) {
	ticker := time.NewTicker(triggerPoll)
	defer ticker.Stop()
	for {
		if err := d.fireDueTrigger(ctx); err != nil && !errors.Is(err, context.Canceled) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) fireDueTrigger(ctx context.Context) error {
	trigger, err := d.store.ClaimTriggerFire(ctx)
	if err != nil || trigger == nil {
		return err
	}
	delivered, err := d.store.FireTrigger(context.WithoutCancel(ctx), trigger.ID)
	if err != nil {
		return err
	}
	// A trigger that reaches nothing is a board someone is still wiring up, not
	// a failure. It is worth saying once per firing, and no more than that.
	if delivered == 0 {
		log.Printf("trigger %d (%s) fired but reaches no card", trigger.ID, trigger.Title)
	}
	return nil
}
