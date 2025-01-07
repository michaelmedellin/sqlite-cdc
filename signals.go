// SPDX-FileCopyrightText: © 2024 Kevin Conway <kevin@conway0.com>
// SPDX-License-Identifier: Apache-2.0

package cdc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Signal implementations tell a CDC engine when to awaken while in CDC mode
// in order to process new changes.
type Signal interface {
	// Waker returns a channel that will receive wake signals. If the channel
	// is closed by the Signal then the Signal has entered a terminal state.
	Waker() <-chan SignalEvent
	// Start implementations establish any state required to generate wake
	// signals. Long running tasks, such as continuous monitoring, should be
	// started in goroutines so that this method does not block.
	Start(ctx context.Context) error
	// Close releases any resources used by the Signal including any goroutines
	// started. Once a Signal is closed then it is in a terminal state and
	// cannot be re-used.
	Close() error
}

// SignalEvent indicates that a Signal has been triggered.
//
// An engine should only check for changes if the Wake field is true. A non-nil
// error represents a terminal error for the Signal.
type SignalEvent struct {
	Wake bool
	Err  error
}

// fSNotifySignal implements Signal using filesystem notifications to detect
// changes.
type fSNotifySignal struct {
	watcher   *fsnotify.Watcher
	wake      chan SignalEvent
	closed    chan any
	closeOnce *sync.Once
	meta      *dbMeta
	targets   map[string]bool
}

// NewFSNotifySignal creates a new Signal implementation that uses filesystem
// notifications to detect changes. This implementation uses the given database
// client to determine the path of the main SQLite database as well as any
// supplemental files such as the WAL when in WAL mode.
func NewFSNotifySignal(db *sql.DB) (Signal, error) {
	return newFSNotifySignal(db)
}

func newFSNotifySignal(db *sql.DB) (*fSNotifySignal, error) {
	meta, err := newDBMeta(db)
	if err != nil {
		return nil, fmt.Errorf("failed to get database metadata: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	if err := watcher.Add(filepath.Dir(meta.Filename)); err != nil {
		return nil, fmt.Errorf("%w: failed to add %q to fsnotify watcher", err, filepath.Dir(meta.Filename))
	}

	watchTargets := make(map[string]bool)
	watchTargets[meta.Filename] = true
	for _, f := range meta.ExtraFiles {
		watchTargets[f] = true
	}

	s := &fSNotifySignal{
		watcher:   watcher,
		wake:      make(chan SignalEvent),
		closed:    make(chan any),
		closeOnce: &sync.Once{},
		meta:      meta,
		targets:   watchTargets,
	}
	return s, nil
}

func (s *fSNotifySignal) Start(ctx context.Context) error {
	go s.watch(ctx)
	return nil
}

func (s *fSNotifySignal) watch(ctx context.Context) {
	for {
		ok, err := s.watchStep(ctx)
		if errors.Is(err, errStop) {
			return
		}
		if err != nil {
			select {
			case s.wake <- SignalEvent{Err: err}:
			case <-s.closed:
				return
			}
			continue
		}
		if !ok {
			continue
		}
		select {
		case s.wake <- SignalEvent{Wake: true}:
		case <-s.closed:
			return
		}
	}
}
func (s *fSNotifySignal) watchStep(ctx context.Context) (bool, error) {
	select {
	case <-s.closed:
		return false, errStop
	case <-ctx.Done():
		return false, s.Close()
	case event, ok := <-s.watcher.Events:
		if !ok {
			// The watcher was closed.
			return false, s.Close()
		}
		if event.Op == fsnotify.Chmod {
			return false, nil
		}
		if !s.targets[event.Name] {
			return false, nil
		}
		return true, nil
	case err, ok := <-s.watcher.Errors:
		if !ok {
			// The watcher was closed.
			return false, s.Close()
		}
		return false, fmt.Errorf("%w: fsnotify watcher error", err)
	}
}

// Waker returns a channel that will receive a signal when changes are detected.
func (s *fSNotifySignal) Waker() <-chan SignalEvent {
	return s.wake
}

// Close stops the file system watcher and cleans up resources.
func (s *fSNotifySignal) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
	})
	return s.watcher.Close()
}

type timeSignal struct {
	wake      chan SignalEvent
	closed    chan any
	closeOnce *sync.Once
	interval  time.Duration
}

// NewTimeSignal returns a new time-based Signal that emits a wake event on an
// interval. This can be used in environments where the file watching
// implementation is unsupported or unreliable.
func NewTimeSignal(interval time.Duration) (Signal, error) {
	return newTimeSignal(interval), nil
}

func newTimeSignal(interval time.Duration) *timeSignal {
	return &timeSignal{
		wake:      make(chan SignalEvent, 1),
		closed:    make(chan any),
		closeOnce: &sync.Once{},
		interval:  interval,
	}
}

func (s *timeSignal) Start(ctx context.Context) error {
	go s.watch(ctx)
	return nil
}

func (s *timeSignal) watch(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		ok, err := s.watchStep(ctx, ticker)
		if errors.Is(err, errStop) {
			return
		}
		if err != nil {
			select {
			case s.wake <- SignalEvent{Err: err}:
			case <-s.closed:
				return
			}
			continue
		}
		if !ok {
			continue
		}
		select {
		case s.wake <- SignalEvent{Wake: true}:
		case <-s.closed:
			return
		}
	}
}

func (s *timeSignal) watchStep(ctx context.Context, ticker *time.Ticker) (bool, error) {
	select {
	case <-s.closed:
		return false, errStop
	case <-ctx.Done():
		return false, s.Close()
	case <-ticker.C:
		return true, nil
	}
}

func (s *timeSignal) Waker() <-chan SignalEvent {
	return s.wake
}

func (s *timeSignal) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
	})
	return nil
}

type channelSignal struct {
	input     <-chan SignalEvent
	wake      chan SignalEvent
	closed    chan any
	closeOnce *sync.Once
}

// NewChannelSignal returns a Signal implementation that proxies events from
// the given channel to the waker channel. This is useful for awakening based
// on external triggers.
func NewChannelSignal(input <-chan SignalEvent) (Signal, error) {
	return newChannelSignal(input), nil
}

func newChannelSignal(input <-chan SignalEvent) *channelSignal {
	return &channelSignal{
		input:     input,
		wake:      make(chan SignalEvent),
		closed:    make(chan any),
		closeOnce: &sync.Once{},
	}
}

func (s *channelSignal) Start(ctx context.Context) error {
	go s.watch(ctx)
	return nil
}

func (s *channelSignal) watch(ctx context.Context) {
	for {
		event, err := s.watchStep(ctx)
		if errors.Is(err, errStop) {
			return
		}
		if err != nil {
			select {
			case s.wake <- SignalEvent{Err: err}:
			case <-s.closed:
				return
			}
			continue
		}
		select {
		case s.wake <- event:
		case <-s.closed:
			return
		}
	}
}

func (s *channelSignal) watchStep(ctx context.Context) (SignalEvent, error) {
	select {
	case <-s.closed:
		return SignalEvent{}, errStop
	case <-ctx.Done():
		_ = s.Close()
		return SignalEvent{}, errStop
	case event := <-s.input:
		return event, nil
	}
}

func (s *channelSignal) Waker() <-chan SignalEvent {
	return s.wake
}

func (s *channelSignal) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
	})
	return nil
}

type multiSignal struct {
	signals   []Signal
	wake      chan SignalEvent
	closed    chan any
	closeOnce *sync.Once
}

// NewMultiSignal returns a Signal implementation that combines multiple
// signals into a single channel.
func NewMultiSignal(signals ...Signal) (Signal, error) {
	return newMultiSignal(signals...), nil
}

func newMultiSignal(signals ...Signal) *multiSignal {
	return &multiSignal{
		signals:   signals,
		wake:      make(chan SignalEvent, len(signals)),
		closed:    make(chan any),
		closeOnce: &sync.Once{},
	}
}

func (s *multiSignal) Start(ctx context.Context) error {
	for _, signal := range s.signals {
		if err := signal.Start(ctx); err != nil {
			_ = s.Close()
			return err
		}
	}
	for _, signal := range s.signals {
		waker := signal.Waker()
		go func() {
			for event := range waker {
				if event.Err != nil {
					select {
					case s.wake <- event:
					case <-s.closed:
						return
					}
					continue
				}
				select {
				case s.wake <- event:
				case <-s.closed:
					return
				}
			}
		}()
	}
	return nil
}

func (s *multiSignal) Waker() <-chan SignalEvent {
	return s.wake
}

func (s *multiSignal) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		for _, signal := range s.signals {
			_ = signal.Close()
		}
	})
	return nil
}

var errStop = fmt.Errorf("stop signal received")
