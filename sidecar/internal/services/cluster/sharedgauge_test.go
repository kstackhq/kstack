// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// scriptedGauge is a gauge read the test answers one call at a time. Each call reports
// itself on calls with its own answer channel, so an answer meant for one read can never
// reach another, then returns the answer or ctx's error.
type scriptedGauge struct {
	calls chan gaugeCall
}

type gaugeCall struct {
	ctx    context.Context
	answer chan gaugeReading[int]
}

func newScriptedGauge() *scriptedGauge {
	return &scriptedGauge{calls: make(chan gaugeCall, 8)}
}

func (s *scriptedGauge) read(ctx context.Context) (int, error) {
	call := gaugeCall{ctx: ctx, answer: make(chan gaugeReading[int], 1)}
	s.calls <- call
	select {
	case a := <-call.answer:
		return a.value, a.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// answer waits for the next read and answers it with value.
func (s *scriptedGauge) answer(t *testing.T, value int) {
	t.Helper()
	testutil.Recv(t, s.calls, "the gauge to read").answer <- gaugeReading[int]{value: value}
}

// fail waits for the next read and fails it with err.
func (s *scriptedGauge) fail(t *testing.T, err error) {
	t.Helper()
	testutil.Recv(t, s.calls, "the gauge to read").answer <- gaugeReading[int]{err: err}
}

// nextReading is the subscription's next reading, within testutil's failsafe.
func nextReading(t *testing.T, sub *gaugeSubscription[int]) (int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testutil.Timeout)
	defer cancel()
	value, err := sub.Next(ctx)
	require.NoError(t, ctx.Err(), "waiting for a reading")
	return value, err
}

// mustReading is the subscription's next reading, which must be a value.
func mustReading(t *testing.T, sub *gaugeSubscription[int]) int {
	t.Helper()
	v, err := nextReading(t, sub)
	require.NoError(t, err)
	return v
}

// An hour between reads, so a test sees only the reads it starts.
const noTick = time.Hour

// A join to an idle loop asks it for a read, and the joiner's first reading is that one,
// not the one before it joined. One joiner only: a second joining after the loop has begun
// the first's read is owed a read of its own.
func TestSharedGaugeReadsForAJoinerToAnIdleLoop(t *testing.T) {
	s := newScriptedGauge()
	g := newSharedGauge(s.read)

	first := g.subscribe(noTick)
	t.Cleanup(first.Close)
	s.answer(t, 7)
	assert.Equal(t, 7, mustReading(t, first))

	second := g.subscribe(noTick)
	t.Cleanup(second.Close)
	s.answer(t, 8)
	assert.Equal(t, 8, mustReading(t, second))
	assert.Equal(t, 8, mustReading(t, first))

	// A negative assertion: a read the join did not ask for would start at once.
	testutil.NoRecv(t, s.calls, quietWindow, "a second read for one join")
}

// A join during an open read is served by the read after it, not the open one, which may
// have listed before the join; every joiner shares that read.
func TestSharedGaugeReadsAfreshForAJoinerDuringARead(t *testing.T) {
	s := newScriptedGauge()
	g := newSharedGauge(s.read)

	first := g.subscribe(noTick)
	t.Cleanup(first.Close)
	open := testutil.Recv(t, s.calls, "the gauge to read")

	second := g.subscribe(noTick)
	t.Cleanup(second.Close)
	third := g.subscribe(noTick)
	t.Cleanup(third.Close)
	open.answer <- gaugeReading[int]{value: 7}
	assert.Equal(t, 7, mustReading(t, first))

	s.answer(t, 8)
	assert.Equal(t, 8, mustReading(t, second))
	assert.Equal(t, 8, mustReading(t, third))
	assert.Equal(t, 8, mustReading(t, first))

	// A negative assertion: a second read for the joiners would start at once.
	testutil.NoRecv(t, s.calls, quietWindow, "a read per joiner")
}

// Every tick's reading reaches every subscriber.
func TestSharedGaugeSendsEachReadingToEverySubscriber(t *testing.T) {
	s := newScriptedGauge()
	g := newSharedGauge(s.read)

	first := g.subscribe(time.Millisecond)
	t.Cleanup(first.Close)
	open := testutil.Recv(t, s.calls, "the gauge to read")

	// Joined while read 1 is open, so the join's read is 2 and the ticks' are 3 on: the
	// test answers every read, so the ticker can never start one the test did not expect.
	second := g.subscribe(time.Millisecond)
	t.Cleanup(second.Close)
	open.answer <- gaugeReading[int]{value: 1}
	assert.Equal(t, 1, mustReading(t, first))

	for value := 2; value <= 4; value++ {
		s.answer(t, value)
		assert.Equal(t, value, mustReading(t, first))
		assert.Equal(t, value, mustReading(t, second))
	}
}

// The loop runs while anyone holds it: the last subscriber leaving cancels its read, and
// the next subscriber starts a new loop.
func TestSharedGaugeStopsWithItsLastSubscriber(t *testing.T) {
	s := newScriptedGauge()
	g := newSharedGauge(s.read)

	first := g.subscribe(noTick)
	second := g.subscribe(noTick)
	readCtx := testutil.Recv(t, s.calls, "the gauge to read").ctx

	first.Close()
	assert.NoError(t, readCtx.Err(), "the loop stopped while a subscriber still held it")

	second.Close()
	testutil.Wait(t, readCtx.Done(), "the loop to stop with its last subscriber")

	again := g.subscribe(noTick)
	t.Cleanup(again.Close)
	s.answer(t, 3)
	assert.Equal(t, 3, mustReading(t, again))
}

// A failed read ends the run for everyone in it, and a later subscriber starts a fresh one.
func TestSharedGaugeFailureEndsItsRun(t *testing.T) {
	s := newScriptedGauge()
	g := newSharedGauge(s.read)
	boom := errors.New("store down")

	failed := g.subscribe(noTick)
	t.Cleanup(failed.Close)
	s.fail(t, boom)
	_, err := nextReading(t, failed)
	assert.ErrorIs(t, err, boom)

	fresh := g.subscribe(noTick)
	t.Cleanup(fresh.Close)
	s.answer(t, 4)
	assert.Equal(t, 4, mustReading(t, fresh))
}

// A subscriber that joins during the read that fails gets the failure too, not a bare
// close: the error is the run's last word, whenever the subscriber joined.
func TestSharedGaugeFailureReachesAJoinerDuringTheRead(t *testing.T) {
	s := newScriptedGauge()
	g := newSharedGauge(s.read)
	boom := errors.New("store down")

	first := g.subscribe(noTick)
	t.Cleanup(first.Close)
	open := testutil.Recv(t, s.calls, "the gauge to read")

	joiner := g.subscribe(noTick)
	t.Cleanup(joiner.Close)
	open.answer <- gaugeReading[int]{err: boom}

	_, err := nextReading(t, first)
	assert.ErrorIs(t, err, boom)
	_, err = nextReading(t, joiner)
	assert.ErrorIs(t, err, boom)
}

// Next reports the subscriber's own ctx ending, and leaves the loop running for the rest.
func TestSharedGaugeNextEndsWithItsContext(t *testing.T) {
	s := newScriptedGauge()
	g := newSharedGauge(s.read)

	sub := g.subscribe(noTick)
	t.Cleanup(sub.Close)
	readCtx := testutil.Recv(t, s.calls, "the gauge to read").ctx

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := sub.Next(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NoError(t, readCtx.Err(), "the loop stopped with a subscriber's ctx")
}

// A second Close is a no-op: it must not take another subscriber's seat.
func TestSharedGaugeCloseIsIdempotent(t *testing.T) {
	s := newScriptedGauge()
	g := newSharedGauge(s.read)

	first := g.subscribe(noTick)
	second := g.subscribe(noTick)
	t.Cleanup(second.Close)
	readCtx := testutil.Recv(t, s.calls, "the gauge to read").ctx

	first.Close()
	first.Close()
	assert.NoError(t, readCtx.Err(), "the loop stopped while a subscriber still held it")
}
