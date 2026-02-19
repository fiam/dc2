package dc2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fiam/dc2/pkg/dc2/api"
)

const (
	instanceMarketTypeOnDemand = "on-demand"
	instanceMarketTypeSpot     = "spot"

	attributeNameInstanceMarketType = "InstanceMarketType"

	stateReasonSpotTerminationCode    = "Server.SpotInstanceTermination"
	stateReasonSpotTerminationMessage = "Server.SpotInstanceTermination: Instance terminated due to spot interruption"
)

func normalizeMarketType(raw string) (string, error) {
	marketType := strings.ToLower(strings.TrimSpace(raw))
	switch marketType {
	case "", instanceMarketTypeOnDemand:
		return instanceMarketTypeOnDemand, nil
	case instanceMarketTypeSpot:
		return instanceMarketTypeSpot, nil
	default:
		return "", api.InvalidParameterValueError("InstanceMarketOptions.MarketType", raw)
	}
}

type spotReclaimPlan struct {
	After  time.Duration
	Notice time.Duration
}

func (d *Dispatcher) resolveSpotReclaimPlan(req *api.RunInstancesRequest, marketType string) spotReclaimPlan {
	plan := spotReclaimPlan{
		After:  d.opts.SpotReclaimAfter,
		Notice: d.opts.SpotReclaimNotice,
	}
	if d.testProfile != nil {
		override := d.testProfile.SpotReclaim(d.runInstancesMatchInput(req))
		if override.After != nil {
			plan.After = *override.After
		}
		if override.Notice != nil {
			plan.Notice = *override.Notice
		}
	}
	if !strings.EqualFold(marketType, instanceMarketTypeSpot) {
		return spotReclaimPlan{}
	}
	if plan.After <= 0 {
		return spotReclaimPlan{}
	}
	if plan.Notice < 0 {
		plan.Notice = 0
	}
	if plan.Notice > plan.After {
		plan.Notice = plan.After
	}
	return plan
}

func (d *Dispatcher) scheduleSpotReclaim(instanceID string, plan spotReclaimPlan) {
	if plan.After <= 0 {
		return
	}

	reclaimAt := time.Now().UTC().Add(plan.After)
	notice := plan.Notice
	warnAt := reclaimAt.Add(-notice)
	runtimeID := string(executorInstanceID(instanceID))

	reclaimCtx, cancel := context.WithCancel(context.Background())
	d.spotReclaimMu.Lock()
	if existingCancel, found := d.spotReclaimCancels[instanceID]; found {
		existingCancel()
	}
	d.spotReclaimCancels[instanceID] = cancel
	d.spotReclaimMu.Unlock()

	go func() {
		defer d.cancelSpotReclaim(instanceID)
		defer func() {
			if err := d.imds.ClearSpotInstanceAction(runtimeID); err != nil {
				slog.Warn(
					"failed to clear spot interruption action",
					slog.String("instance_id", instanceID),
					slog.Any("error", err),
				)
			}
		}()

		if notice > 0 {
			if !waitUntil(reclaimCtx, warnAt) {
				return
			}
			if err := d.imds.SetSpotInstanceAction(runtimeID, "terminate", reclaimAt); err != nil {
				slog.Warn(
					"failed to set spot interruption action",
					slog.String("instance_id", instanceID),
					slog.Any("error", err),
				)
			}
		}

		if !waitUntil(reclaimCtx, reclaimAt) {
			return
		}
		if err := d.reclaimSpotInstance(instanceID, reclaimAt); err != nil {
			slog.Warn(
				"failed to reclaim spot instance",
				slog.String("instance_id", instanceID),
				slog.Any("error", err),
			)
		}
	}()
}

func (d *Dispatcher) cancelSpotReclaim(instanceID string) {
	d.spotReclaimMu.Lock()
	defer d.spotReclaimMu.Unlock()
	cancel, found := d.spotReclaimCancels[instanceID]
	if !found {
		return
	}
	delete(d.spotReclaimCancels, instanceID)
	cancel()
}

func (d *Dispatcher) cancelAllSpotReclaims() {
	d.spotReclaimMu.Lock()
	defer d.spotReclaimMu.Unlock()
	for instanceID, cancel := range d.spotReclaimCancels {
		cancel()
		delete(d.spotReclaimCancels, instanceID)
	}
}

func (d *Dispatcher) reclaimSpotInstance(instanceID string, reclaimAt time.Time) error {
	d.dispatchMu.Lock()
	defer d.dispatchMu.Unlock()

	ctx := context.Background()
	if _, err := d.findInstance(ctx, instanceID); err != nil {
		var invalidParamErr *api.Error
		if errors.As(err, &invalidParamErr) && invalidParamErr.Code == api.ErrorCodeInvalidParameterValue {
			return nil
		}
		return err
	}

	_, err := d.terminateInstancesWithStateReason(
		ctx,
		[]string{instanceID},
		"Server.SpotInstanceTermination",
		stateReasonSpotTerminationCode,
		stateReasonSpotTerminationMessage,
		false,
	)
	if err != nil {
		return fmt.Errorf("terminating reclaimed spot instance %s: %w", instanceID, err)
	}
	slog.Info(
		"reclaimed spot instance",
		slog.String("instance_id", instanceID),
		slog.Time("termination_time", reclaimAt),
	)
	return nil
}

func waitUntil(ctx context.Context, when time.Time) bool {
	delay := time.Until(when)
	if delay <= 0 {
		return true
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
