// SPDX-FileCopyrightText: 2024 Kevin Conway <kevin@conway0.com>
// SPDX-License-Identifier: Apache-2.0

package cdc

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFSNotifySignal(t *testing.T) {
	t.Parallel()

	// Create a test database
	db := testDB(t)
	t.Cleanup(func() { db.Close() })

	// Create our signal
	signal, err := NewFSNotifySignal(db)
	require.NoError(t, err)
	t.Cleanup(func() { signal.Close() })

	// Start watching
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, signal.Start(ctx))

	// Get the waker channel for receiving signals
	waker := signal.Waker()

	// Get database metadata to find the files
	meta, err := newDBMeta(db)
	require.NoError(t, err)

	// Test main database file changes
	t.Run("main db file", func(t *testing.T) {
		t.Parallel()
		_, err := db.Exec("CREATE TABLE test (col INT)")
		require.NoError(t, err)
		assert.True(t, wait(t, waker), "should receive wake signal for main db file change")
	})

	for _, f := range meta.ExtraFiles {
		t.Run(f, func(t *testing.T) {
			t.Parallel()
			if _, err := os.Stat(f); err != nil {
				t.Skipf("skipping test for extra file %q because it doesn't exist", f)
			}
			_, err := db.Exec("INSERT INTO test (col) VALUES (1)")
			require.NoError(t, err)
			assert.True(t, wait(t, waker), "should receive wake signal for extra file change: %s", f)
		})
	}
}

func TestTimeSignal(t *testing.T) {
	t.Parallel()

	interval := 10 * time.Millisecond
	signal, err := NewTimeSignal(interval)
	require.NoError(t, err)
	t.Cleanup(func() { signal.Close() })

	// Start watching
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, signal.Start(ctx))

	// Get the waker channel for receiving signals
	waker := signal.Waker()

	// Should receive multiple signals
	for x := 0; x < 3; x = x + 1 {
		assert.True(t, wait(t, waker), "should receive wake signal on interval")
	}

	// Test cancellation
	cancel()
	time.Sleep(interval * 2)
	require.False(t, wait(t, waker), "should not receive wake signal after cancellation")
}

func TestChannelSignal(t *testing.T) {
	t.Parallel()

	input := make(chan SignalEvent, 3)
	signal, err := NewChannelSignal(input)
	require.NoError(t, err)
	t.Cleanup(func() { signal.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, signal.Start(ctx))

	waker := signal.Waker()

	// Send wake events
	input <- SignalEvent{Wake: true}
	input <- SignalEvent{Wake: true}
	input <- SignalEvent{Wake: true}

	// Should receive all signals
	for x := 0; x < 3; x = x + 1 {
		assert.True(t, wait(t, waker), "should receive wake signal from input channel")
	}

	// Test cancellation
	cancel()
	require.False(t, wait(t, waker), "should not receive wake signal after cancellation")
}

func TestMultiSignal(t *testing.T) {
	t.Parallel()

	// Create multiple input channels
	input1 := make(chan SignalEvent, 1)
	signal1, err := NewChannelSignal(input1)
	require.NoError(t, err)

	input2 := make(chan SignalEvent, 1)
	signal2, err := NewChannelSignal(input2)
	require.NoError(t, err)

	input3 := make(chan SignalEvent, 1)
	signal3, err := NewChannelSignal(input3)
	require.NoError(t, err)

	// Create multi signal
	multi, err := NewMultiSignal(signal1, signal2, signal3)
	require.NoError(t, err)
	t.Cleanup(func() { multi.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, multi.Start(ctx))

	waker := multi.Waker()

	// Test signals from first channel
	input1 <- SignalEvent{Wake: true}
	assert.True(t, wait(t, waker), "should receive wake signal from first channel")

	// Test signals from second channel
	input2 <- SignalEvent{Wake: true}
	assert.True(t, wait(t, waker), "should receive wake signal from second channel")

	// Test signals from third channel
	input3 <- SignalEvent{Wake: true}
	assert.True(t, wait(t, waker), "should receive wake signal from third channel")

	// Test multiple concurrent signals
	input1 <- SignalEvent{Wake: true}
	input2 <- SignalEvent{Wake: true}
	input3 <- SignalEvent{Wake: true}

	for x := 0; x < 3; x = x + 1 {
		assert.True(t, wait(t, waker), "should receive wake signal from concurrent inputs")
	}

	// Test cancellation
	cancel()
	require.False(t, wait(t, waker), "should not receive wake signal after cancellation")
}

func wait(tb tOrB, ch <-chan SignalEvent) bool {
	tb.Helper()
	select {
	case event := <-ch:
		require.NoError(tb, event.Err)
		return event.Wake
	case <-time.After(time.Second):
		return false
	}
}
