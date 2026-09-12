//go:build darwin || linux

package interop_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

type nativeToolSchedule struct{ Interval, Lifetime time.Duration }

func nativeToolScheduleFor(rounds int, raw string) (nativeToolSchedule, error) {
	var s nativeToolSchedule
	if raw == "" {
		raw = "0"
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms < 0 || ms > 15000 || rounds < 8 || rounds > 128 {
		return s, errors.New("invalid bounded native tool schedule")
	}
	s.Interval = time.Duration(ms) * time.Millisecond
	s.Lifetime = 8*time.Minute + time.Duration(rounds-1)*s.Interval
	return s, nil
}

func (s nativeToolSchedule) wait(ctx context.Context, start time.Time, round int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Interval == 0 || round == 1 {
		return nil
	}
	if start.IsZero() || round < 1 || round > 128 {
		return errors.New("native tool schedule lacks an owned origin or round")
	}
	delay := time.Until(start.Add(time.Duration(round-1) * s.Interval))
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func TestNativeToolSoakScheduleBounds(t *testing.T) {
	for _, raw := range []string{"", "0", "20", "15000", "-1", "15001", "invalid", "9223372036854775807"} {
		for _, rounds := range []int{7, 8, 128, 129} {
			s, err := nativeToolScheduleFor(rounds, raw)
			valid := (rounds == 8 || rounds == 128) && (raw == "" || raw == "0" || raw == "20" || raw == "15000")
			if (err == nil) != valid {
				t.Fatal("paced native schedule admitted invalid bounds")
			}
			if valid && (s.Lifetime != 8*time.Minute+time.Duration(rounds-1)*s.Interval || s.Lifetime > 40*time.Minute) {
				t.Fatal("paced native schedule lost its shared finite budget")
			}
		}
	}
}

func TestNativeToolSoakScheduleWaitIsCancelable(t *testing.T) {
	s, err := nativeToolScheduleFor(8, "15000")
	if err != nil {
		t.Fatal("owned paced schedule")
	}
	start := time.Now()
	if err := s.wait(t.Context(), start, 1); err != nil {
		t.Fatal("first native pair was delayed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	err = s.wait(ctx, start, 2)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("paced native pair ignored its due time or cancellation")
	}
	if err := s.wait(t.Context(), time.Now().Add(-time.Minute), 2); err != nil {
		t.Fatal("already due native pair did not proceed")
	}
	if err := s.wait(t.Context(), time.Time{}, 2); err == nil {
		t.Fatal("paced pair admitted an absent origin")
	}
}
